package wui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/logging"
	"chinachu-go/internal/mirakurun"
	"chinachu-go/internal/scheduler"
	"chinachu-go/internal/storage"
	"chinachu-go/internal/system"
)

type Paths struct {
	Config       string
	Rules        string
	Schedule     string
	Reserves     string
	Recording    string
	Recorded     string
	WebRoot      string
	LogDir       string
	SchedulerPID string
	OperatorPID  string
	Scheduler    func(context.Context, bool) error
}

func Run(ctx context.Context, paths Paths) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	servers := buildHTTPServers(paths, cfg)
	if len(servers) == 0 {
		return fmt.Errorf("no WUI listener configured")
	}
	errCh := make(chan serverError, len(servers))
	for _, srv := range servers {
		if err := logging.AppendLine(filepath.Join(logDir(paths), "wui"), "%s Listening on %s", srv.label, srv.server.Addr); err != nil {
			return err
		}
		go func(s runningServer) {
			var err error
			if s.tls {
				err = s.server.ListenAndServeTLS(cfg.WUITlsCertPath, cfg.WUITlsKeyPath)
			} else {
				err = s.server.ListenAndServe()
			}
			errCh <- serverError{server: s, err: err}
		}(srv)
	}
	select {
	case <-ctx.Done():
		if err := shutdownServers(paths, servers); err != nil {
			return err
		}
		return ctx.Err()
	case serverErr := <-errCh:
		if serverErr.err == http.ErrServerClosed {
			_ = logging.AppendLine(filepath.Join(logDir(paths), "wui"), "%s Closed", serverErr.server.label)
			return nil
		}
		_ = shutdownServers(paths, servers)
		_ = logging.AppendLine(filepath.Join(logDir(paths), "wui"), "ERROR: %v", serverErr.err)
		return serverErr.err
	}
}

func NewHandler(paths Paths, cfg *config.Config) http.Handler {
	return newHandler(paths, cfg, true)
}

func newHandler(paths Paths, cfg *config.Config, auth bool) http.Handler {
	mux := http.NewServeMux()
	server := &server{paths: paths, cfg: cfg, webRoot: findWebRoot(paths.WebRoot)}
	mux.HandleFunc("/api/", server.handleAPI)
	mux.HandleFunc("/", server.handleStatic)
	var handler http.Handler = mux
	if auth {
		handler = server.withAuth(handler)
	}
	return server.withCommonHeaders(handler)
}

type server struct {
	paths   Paths
	cfg     *config.Config
	webRoot string
}

type runningServer struct {
	server *http.Server
	label  string
	tls    bool
}

type serverError struct {
	server runningServer
	err    error
}

func buildHTTPServers(paths Paths, cfg *config.Config) []runningServer {
	servers := []runningServer{}
	if cfg.WUIPort != nil {
		tls := cfg.WUITlsKeyPath != "" && cfg.WUITlsCertPath != ""
		label := "HTTP Server"
		if tls {
			label = "HTTPS Server"
		}
		servers = append(servers, runningServer{
			server: &http.Server{
				Addr:              listenAddress(cfg.WUIHost, *cfg.WUIPort),
				Handler:           newHandler(paths, cfg, true),
				ReadHeaderTimeout: 10 * time.Second,
			},
			label: label,
			tls:   tls,
		})
	}
	if cfg.WUIOpenServer {
		port := cfg.WUIOpenPort
		if port == 0 {
			port = 20772
		}
		servers = append(servers, runningServer{
			server: &http.Server{
				Addr:              listenAddress(cfg.WUIOpenHost, port),
				Handler:           newHandler(paths, cfg, false),
				ReadHeaderTimeout: 10 * time.Second,
			},
			label: "HTTP Open Server",
		})
	}
	return servers
}

func shutdownServers(paths Paths, servers []runningServer) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.server.Shutdown(shutdownCtx); err != nil {
			_ = logging.AppendLine(filepath.Join(logDir(paths), "wui"), "ERROR: %v", err)
			return err
		}
		_ = logging.AppendLine(filepath.Join(logDir(paths), "wui"), "%s Closed", srv.label)
	}
	return nil
}

func listenAddress(host string, port int) string {
	if host == "" {
		host = "0.0.0.0"
	}
	if port == 0 {
		port = 20772
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

func (s *server) withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Chinachu (Go)")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-UA-Compatible", "IE=Edge,chrome=1")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

func (s *server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.WUIUsers) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
		decoded, err := base64.StdEncoding.DecodeString(auth)
		if err != nil || !stringIn(s.cfg.WUIUsers, string(decoded)) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Chinachu"`)
			http.Error(w, "401 Authorization Required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.webRoot == "" {
		http.NotFound(w, r)
		return
	}
	http.FileServer(http.Dir(s.webRoot)).ServeHTTP(w, r)
}

func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	apiType := apiExtension(path)
	path = trimLastExtension(path)
	parts := splitPath(path)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case len(parts) == 1 && parts[0] == "status":
		writeJSON(w, http.StatusOK, s.status())
	case len(parts) == 1 && parts[0] == "scheduler":
		s.handleScheduler(w, r, apiType)
	case len(parts) == 2 && parts[0] == "scheduler" && parts[1] == "force":
		s.handleSchedulerForce(w, r)
	case len(parts) == 1 && parts[0] == "storage":
		s.handleStorage(w, r)
	case len(parts) == 2 && parts[0] == "log":
		s.handleLog(w, r, parts[1], false)
	case len(parts) == 3 && parts[0] == "log" && parts[2] == "stream":
		s.handleLog(w, r, parts[1], true)
	case len(parts) == 1 && parts[0] == "config":
		s.handleConfig(w, r)
	case len(parts) == 1 && parts[0] == "rules":
		s.handleRules(w, r)
	case len(parts) == 2 && parts[0] == "rules":
		s.handleRule(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "rules":
		s.handleRuleAction(w, r, parts[1], parts[2])
	case len(parts) == 1 && parts[0] == "schedule":
		s.handleJSONFile(w, r, s.paths.Schedule, "[]")
	case len(parts) == 2 && parts[0] == "schedule" && parts[1] == "programs":
		s.handleSchedulePrograms(w, r)
	case len(parts) == 2 && parts[0] == "schedule" && parts[1] == "broadcasting":
		s.handleScheduleBroadcasting(w, r)
	case len(parts) == 2 && parts[0] == "schedule":
		s.handleScheduleChannel(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "schedule" && parts[2] == "programs":
		s.handleScheduleChannelPrograms(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "schedule" && parts[2] == "broadcasting":
		s.handleScheduleChannelBroadcasting(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "reserves":
		s.handleJSONFile(w, r, s.paths.Reserves, "[]")
	case len(parts) >= 2 && parts[0] == "reserves":
		s.handleReserveProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recording":
		s.handleJSONFile(w, r, s.paths.Recording, "[]")
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "preview":
		s.handleProgramPreview(w, r, s.paths.Recording, parts[1])
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "watch":
		s.handleProgramWatch(w, r, s.paths.Recording, parts[1], apiType, true)
	case len(parts) >= 2 && parts[0] == "recording":
		s.handleRecordingProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recorded":
		s.handleRecorded(w, r)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		s.handleRecordedFile(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "preview":
		s.handleProgramPreview(w, r, s.paths.Recorded, parts[1])
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "watch":
		s.handleProgramWatch(w, r, s.paths.Recorded, parts[1], apiType, false)
	case len(parts) >= 2 && parts[0] == "recorded":
		s.handleRecordedProgram(w, r, parts[1:])
	case len(parts) == 2 && parts[0] == "program":
		s.handleProgram(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "channel" && parts[2] == "logo":
		s.handleChannelLogo(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "channel" && parts[2] == "watch":
		s.handleChannelWatch(w, r, parts[1], apiType)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) handleJSONFile(w http.ResponseWriter, r *http.Request, path, empty string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var v any
	if err := storage.ReadJSON(path, &v, empty); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := os.Stat(s.paths.Config); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "410 Gone", http.StatusGone)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		raw := r.URL.Query().Get("json")
		if raw == "" {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		var obj any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		if err := storage.WriteFileAtomic(s.paths.Config, []byte(raw)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(raw))
		}
		return
	}
	data, err := os.ReadFile(s.paths.Config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *server) handleSchedulePrograms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	programs := []chinachu.Program{}
	for _, channel := range schedules {
		programs = append(programs, channel.Programs...)
	}
	writeJSON(w, http.StatusOK, programs)
}

func (s *server) handleScheduleBroadcasting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, broadcastingPrograms(schedules, time.Now()))
}

func (s *server) handleScheduleChannel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if channel == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

func (s *server) handleScheduleChannelPrograms(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if channel == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, channel.Programs)
}

func (s *server) handleScheduleChannelBroadcasting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if channel == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, broadcastingPrograms([]chinachu.ChannelSchedule{*channel}, time.Now()))
}

func (s *server) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var recordedSize int64
	for _, program := range recorded {
		if program.Recorded == "" {
			continue
		}
		info, err := os.Stat(filepath.FromSlash(program.Recorded))
		if err == nil && info.Mode().IsRegular() {
			recordedSize += info.Size()
		}
	}
	recordedDir := s.cfg.RecordedDir
	if recordedDir == "" {
		recordedDir = "."
	}
	usage, err := system.GetDiskUsage(recordedDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recorded": recordedSize,
		"size":     usage.Size,
		"used":     usage.Used,
		"avail":    usage.Avail,
	})
}

func (s *server) handleScheduler(w http.ResponseWriter, r *http.Request, apiType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPut {
		if err := s.runScheduler(r.Context(), false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	logPath := filepath.Join(s.logDir(), "scheduler")
	switch apiType {
	case "txt":
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	default:
		result, ok, err := s.schedulerResultFromLog(logPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			if r.Method != http.MethodHead {
				_ = json.NewEncoder(w).Encode(result)
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *server) handleSchedulerForce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = s.runScheduler(ctx, false)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{})
}

func (s *server) handleLog(w http.ResponseWriter, r *http.Request, name string, stream bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if name != "wui" && name != "operator" && name != "scheduler" {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.logDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if stream {
		_, _ = w.Write([]byte(strings.Repeat(" ", 1023)))
	}
	_, _ = w.Write(data)
}

func (s *server) runScheduler(ctx context.Context, simulation bool) error {
	if s.paths.Scheduler != nil {
		return s.paths.Scheduler(ctx, simulation)
	}
	_, err := scheduler.Run(ctx, scheduler.Paths{
		Config:   s.paths.Config,
		Rules:    s.paths.Rules,
		Schedule: s.paths.Schedule,
		Reserves: s.paths.Reserves,
		PID:      s.pidPath("scheduler"),
		Log:      filepath.Join(s.logDir(), "scheduler"),
	}, simulation)
	return err
}

func (s *server) schedulerResultFromLog(path string) (map[string]any, bool, error) {
	result := map[string]any{
		"time":      int64(0),
		"conflicts": []chinachu.Program{},
		"reserves":  []chinachu.Program{},
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, false, nil
		}
		return result, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, false, err
	}
	schedules, err := s.readSchedule()
	if err != nil {
		return result, false, err
	}
	conflicts := []chinachu.Program{}
	reserves := []chinachu.Program{}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "RUNNING SCHEDULER." {
			break
		}
		kind, id, ok := parseSchedulerLogProgram(line)
		if !ok {
			continue
		}
		program := chinachu.GetProgramByID(id, schedules, nil)
		if program == nil {
			continue
		}
		if kind == "CONFLICT" {
			conflicts = append(conflicts, *program)
		}
		if kind == "RESERVE" {
			reserves = append(reserves, *program)
		}
	}
	reversePrograms(conflicts)
	reversePrograms(reserves)
	result["time"] = info.ModTime().UnixMilli()
	result["conflicts"] = conflicts
	result["reserves"] = reserves
	return result, true, nil
}

func (s *server) handleRules(w http.ResponseWriter, r *http.Request) {
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(s.paths.Rules, &rules, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, rules)
	case http.MethodPost:
		rule, err := decodeJSONObject(r.Body)
		if err != nil || len(rule) == 0 {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		normalizeRuleEnabled(rule)
		rules = append(rules, rule)
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	default:
		w.Header().Set("Allow", "HEAD, GET, POST")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRule(w http.ResponseWriter, r *http.Request, num string) {
	index, ok := parseIndex(num)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(s.paths.Rules, &rules, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index < 0 || index >= len(rules) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, rules[index])
	case http.MethodPut:
		rule, err := decodeJSONObject(r.Body)
		if err != nil || len(rule) == 0 {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		normalizeRuleEnabled(rule)
		rules[index] = rule
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		rules = append(rules[:index], rules[index+1:]...)
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "HEAD, GET, PUT, DELETE")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRuleAction(w http.ResponseWriter, r *http.Request, num, action string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	index, ok := parseIndex(num)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(s.paths.Rules, &rules, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index < 0 || index >= len(rules) {
		http.NotFound(w, r)
		return
	}
	switch action {
	case "enable":
		delete(rules[index], "isDisabled")
	case "disable":
		rules[index]["isDisabled"] = json.RawMessage("true")
	default:
		http.NotFound(w, r)
		return
	}
	if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *server) handleRecorded(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		kept := recorded[:0]
		for _, program := range recorded {
			if program.Recorded == "" {
				continue
			}
			if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err == nil {
				kept = append(kept, program)
			}
		}
		recorded = kept
		if err := storage.WriteJSONAtomic(s.paths.Recorded, recorded, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, recorded)
}

func (s *server) handleReserveProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := findProgram(reserves, id)
	if index == -1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, reserves[index])
	case http.MethodDelete:
		if !reserves[index].IsManualReserved {
			http.Error(w, "409 Conflict", http.StatusConflict)
			return
		}
		reserves = removeProgram(reserves, id)
		if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	case http.MethodPut:
		if action == "skip" {
			reserves[index].IsSkip = true
		} else if action == "unskip" {
			reserves[index].IsSkip = false
		} else {
			http.NotFound(w, r)
			return
		}
		if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE, PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordingProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	var recording []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recording, &recording, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := findProgram(recording, id)
	if index == -1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, recording[index])
	case http.MethodDelete:
		if !recording[index].IsManualReserved {
			var reserves []chinachu.Program
			if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if reserveIndex := findProgram(reserves, id); reserveIndex != -1 {
				reserves[reserveIndex].IsSkip = true
				if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		recording[index].Abort = true
		if err := storage.WriteJSONAtomic(s.paths.Recording, recording, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordedProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := findProgram(recorded, id)
	if index == -1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeJSON(w, http.StatusOK, withRemovedFlag(recorded[index]))
	case http.MethodDelete:
		if recorded[index].Recorded != "" {
			_ = os.Remove(filepath.FromSlash(recorded[index].Recorded))
		}
		recorded = removeProgram(recorded, id)
		if err := storage.WriteJSONAtomic(s.paths.Recorded, recorded, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordedFile(w http.ResponseWriter, r *http.Request, id, apiType string) {
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := findProgram(recorded, id)
	if index == -1 {
		http.NotFound(w, r)
		return
	}
	path := filepath.FromSlash(recorded[index].Recorded)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "410 Gone", http.StatusGone)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch apiType {
		case "m2ts":
			file, err := os.Open(path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer file.Close()
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.m2ts"`, id))
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
		case "json", "":
			writeJSON(w, http.StatusOK, fileStatJSON(info))
		default:
			http.Error(w, "415 Unsupported Media Type", http.StatusUnsupportedMediaType)
		}
	case http.MethodDelete:
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if apiType == "m2ts" {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleProgramWatch(w http.ResponseWriter, r *http.Request, path, id, apiType string, requirePID bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var programs []chinachu.Program
	if err := storage.ReadJSON(path, &programs, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	index := findProgram(programs, id)
	if index == -1 {
		http.NotFound(w, r)
		return
	}
	program := programs[index]
	if requirePID && !programHasPID(program) {
		http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	if program.Recorded == "" {
		http.Error(w, "410 Gone", http.StatusGone)
		return
	}
	filePath := filepath.FromSlash(program.Recorded)
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "410 Gone", http.StatusGone)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch apiType {
	case "xspf":
		ext := r.URL.Query().Get("ext")
		if ext == "" {
			ext = "m2ts"
		}
		prefix := r.URL.Query().Get("prefix")
		target := prefix + "watch." + ext
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "application/xspf+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xspf"`, id))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			writeXSPF(w, target, program.Title)
		}
	case "m2ts":
		file, err := os.Open(filePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "video/MP2T")
		http.ServeContent(w, r, filepath.Base(filePath), info.ModTime(), file)
	case "mp4":
		http.Error(w, "501 Not Implemented", http.StatusNotImplemented)
	default:
		http.Error(w, "415 Unsupported Media Type", http.StatusUnsupportedMediaType)
	}
}

func (s *server) handleProgramPreview(w http.ResponseWriter, r *http.Request, path, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	apiType := apiExtension(r.URL.Path)
	if apiType != "png" && apiType != "jpg" && apiType != "txt" {
		http.Error(w, "415 Unsupported Media Type", http.StatusUnsupportedMediaType)
		return
	}
	var programs []chinachu.Program
	if err := storage.ReadJSON(path, &programs, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if findProgram(programs, id) == -1 {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "403 Forbidden", http.StatusForbidden)
}

func (s *server) handleProgram(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if program := chinachu.GetProgramByID(id, schedules, nil); program != nil {
		if r.Method == http.MethodPut {
			s.reserveProgram(w, r, *program)
			return
		}
		writeJSON(w, http.StatusOK, program)
		return
	}
	if r.Method == http.MethodPut {
		http.NotFound(w, r)
		return
	}
	for _, file := range []string{s.paths.Reserves, s.paths.Recording, s.paths.Recorded} {
		var programs []chinachu.Program
		if err := storage.ReadJSON(file, &programs, "[]"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if i := findProgram(programs, id); i != -1 {
			writeJSON(w, http.StatusOK, programs[i])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *server) reserveProgram(w http.ResponseWriter, r *http.Request, program chinachu.Program) {
	var reserves []chinachu.Program
	if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if findProgram(reserves, program.ID) != -1 {
		http.Error(w, "409 Conflict", http.StatusConflict)
		return
	}
	program.IsManualReserved = true
	program.OneSeg = r.URL.Query().Get("mode") == "1seg"
	reserves = append(reserves, program)
	if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *server) handleChannelLogo(w http.ResponseWriter, r *http.Request, id, apiType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if apiType != "png" {
		http.Error(w, "415 Unsupported Media Type", http.StatusUnsupportedMediaType)
		return
	}
	channel, ok := s.findChannel(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	serviceID, err := strconv.ParseInt(channel.ID, 36, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := client.LogoImage(r.Context(), serviceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, body)
	}
}

func (s *server) handleChannelWatch(w http.ResponseWriter, r *http.Request, id, apiType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		http.Error(w, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	channel, ok := s.findChannel(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch apiType {
	case "xspf":
		ext := r.URL.Query().Get("ext")
		if ext == "" {
			ext = "m2ts"
		}
		prefix := r.URL.Query().Get("prefix")
		target := prefix + "watch." + ext
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "application/xspf+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xspf"`, channel.ID))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			writeXSPF(w, target, channel.Name)
		}
	case "m2ts":
		serviceID, err := strconv.ParseInt(channel.ID, 36, 64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, err := client.ServiceStream(r.Context(), serviceID, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Type", "video/MP2T")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, body)
		}
	case "mp4":
		http.Error(w, "501 Not Implemented", http.StatusNotImplemented)
	default:
		http.Error(w, "415 Unsupported Media Type", http.StatusUnsupportedMediaType)
	}
}

func (s *server) findChannel(id string) (chinachu.ChannelSchedule, bool) {
	schedules, err := s.readSchedule()
	if err != nil {
		return chinachu.ChannelSchedule{}, false
	}
	for _, channel := range schedules {
		if channel.ID == id {
			return channel, true
		}
	}
	return chinachu.ChannelSchedule{}, false
}

func (s *server) logDir() string {
	return logDir(s.paths)
}

func logDir(paths Paths) string {
	if paths.LogDir != "" {
		return paths.LogDir
	}
	return "log"
}

func (s *server) readSchedule() ([]chinachu.ChannelSchedule, error) {
	var schedules []chinachu.ChannelSchedule
	err := storage.ReadJSON(s.paths.Schedule, &schedules, "[]")
	return schedules, err
}

func (s *server) findScheduleChannel(id string) (*chinachu.ChannelSchedule, error) {
	schedules, err := s.readSchedule()
	if err != nil {
		return nil, err
	}
	for i := range schedules {
		if schedules[i].ID == id {
			return &schedules[i], nil
		}
	}
	return nil, nil
}

func broadcastingPrograms(schedules []chinachu.ChannelSchedule, now time.Time) []chinachu.Program {
	nowMS := now.UnixMilli()
	programs := []chinachu.Program{}
	for _, channel := range schedules {
		for _, program := range channel.Programs {
			if nowMS < program.Start || nowMS > program.End {
				continue
			}
			programs = append(programs, program)
		}
	}
	return programs
}

func (s *server) status() map[string]any {
	operatorPID := readPID(s.pidPath("operator"))
	schedulerPID := readPID(s.pidPath("scheduler"))
	return map[string]any{
		"connectedCount": 0,
		"feature": map[string]any{
			"previewer":         false,
			"streamer":          true,
			"filer":             true,
			"configurator":      true,
			"normalizationForm": s.cfg.NormalizationForm,
			"goImplementation":  true,
			"partialCompatible": true,
		},
		"system": map[string]any{"core": runtime.NumCPU()},
		"operator": map[string]any{
			"alive": operatorPID != nil,
			"pid":   operatorPID,
		},
		"scheduler": map[string]any{
			"alive": schedulerPID != nil,
			"pid":   schedulerPID,
		},
		"wui": map[string]any{
			"alive": true,
			"pid":   os.Getpid(),
		},
	}
}

func (s *server) pidPath(name string) string {
	switch name {
	case "operator":
		if s.paths.OperatorPID != "" {
			return s.paths.OperatorPID
		}
		return filepath.Join("data", "operator.pid")
	case "scheduler":
		if s.paths.SchedulerPID != "" {
			return s.paths.SchedulerPID
		}
		return filepath.Join("data", "scheduler.pid")
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSONObject(body io.Reader) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	err := json.NewDecoder(body).Decode(&value)
	return value, err
}

func normalizeRuleEnabled(rule map[string]json.RawMessage) {
	if raw, ok := rule["isEnabled"]; ok {
		var enabled bool
		if json.Unmarshal(raw, &enabled) == nil && !enabled {
			rule["isDisabled"] = json.RawMessage("true")
		}
		delete(rule, "isEnabled")
	}
}

func parseIndex(value string) (int, bool) {
	index, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return index, true
}

func readPID(path string) *int {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return nil
	}
	return &pid
}

func withRemovedFlag(program chinachu.Program) map[string]any {
	b, _ := json.Marshal(program)
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	if program.Recorded != "" {
		if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err != nil {
			v["isRemoved"] = true
		} else {
			v["isRemoved"] = false
		}
	}
	return v
}

func fileStatJSON(info os.FileInfo) map[string]any {
	modTimeMS := info.ModTime().UnixMilli()
	return map[string]any{
		"dev":     0,
		"ino":     0,
		"mode":    uint32(info.Mode()),
		"ulink":   0,
		"uid":     0,
		"gid":     0,
		"rdev":    0,
		"size":    info.Size(),
		"blksize": 0,
		"blocks":  0,
		"atime":   modTimeMS,
		"mtime":   modTimeMS,
		"ctime":   modTimeMS,
	}
}

func programHasPID(program chinachu.Program) bool {
	return program.PID > 0
}

func writeXSPF(w io.Writer, target, title string) {
	fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(w, "<playlist version=\"1\" xmlns=\"http://xspf.org/ns/0/\">\n")
	fmt.Fprintf(w, "<trackList>\n")
	fmt.Fprintf(w, "<track>\n<location>%s</location>\n<title>%s</title>\n</track>\n", xmlEscape(target), xmlEscape(title))
	fmt.Fprintf(w, "</trackList>\n")
	fmt.Fprintf(w, "</playlist>\n")
}

func parseSchedulerLogProgram(line string) (string, string, bool) {
	for _, kind := range []string{"RESERVE", "CONFLICT"} {
		prefix := kind + ": "
		if !strings.Contains(line, prefix) {
			continue
		}
		rest := line[strings.Index(line, prefix)+len(prefix):]
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "", "", false
		}
		return kind, fields[0], true
	}
	return "", "", false
}

func reversePrograms(programs []chinachu.Program) {
	for i, j := 0, len(programs)-1; i < j; i, j = i+1, j-1 {
		programs[i], programs[j] = programs[j], programs[i]
	}
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func findWebRoot(configured string) string {
	candidates := []string{configured, "web", filepath.Join("..", "Chinachu", "web")}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return ""
}

func splitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := []string{}
	for _, part := range raw {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func apiExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash {
		return path[dot+1:]
	}
	return ""
}

func trimLastExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash {
		return path[:dot]
	}
	return path
}

func findProgram(programs []chinachu.Program, id string) int {
	for i := range programs {
		if programs[i].ID == id {
			return i
		}
	}
	return -1
}

func removeProgram(programs []chinachu.Program, id string) []chinachu.Program {
	out := programs[:0]
	for _, program := range programs {
		if program.ID != id {
			out = append(out, program)
		}
	}
	return out
}

func stringIn(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
