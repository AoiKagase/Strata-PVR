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
	"chinachu-go/internal/storage"
)

type Paths struct {
	Config    string
	Rules     string
	Schedule  string
	Reserves  string
	Recording string
	Recorded  string
	WebRoot   string
}

func Run(ctx context.Context, paths Paths) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	addr := listenAddress(cfg)
	server := &http.Server{
		Addr:              addr,
		Handler:           NewHandler(paths, cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if cfg.WUITlsKeyPath != "" && cfg.WUITlsCertPath != "" {
			errCh <- server.ListenAndServeTLS(cfg.WUITlsCertPath, cfg.WUITlsKeyPath)
			return
		}
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func NewHandler(paths Paths, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	server := &server{paths: paths, cfg: cfg, webRoot: findWebRoot(paths.WebRoot)}
	mux.HandleFunc("/api/", server.handleAPI)
	mux.HandleFunc("/", server.handleStatic)
	return server.withCommonHeaders(server.withAuth(mux))
}

type server struct {
	paths   Paths
	cfg     *config.Config
	webRoot string
}

func listenAddress(cfg *config.Config) string {
	host := cfg.WUIOpenHost
	port := cfg.WUIOpenPort
	if cfg.WUIPort != nil {
		port = *cfg.WUIPort
		host = cfg.WUIHost
	}
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
	case len(parts) == 1 && parts[0] == "config":
		s.handleJSONFile(w, r, s.paths.Config, "{}")
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
	case len(parts) == 1 && parts[0] == "reserves":
		s.handleJSONFile(w, r, s.paths.Reserves, "[]")
	case len(parts) >= 2 && parts[0] == "reserves":
		s.handleReserveProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recording":
		s.handleJSONFile(w, r, s.paths.Recording, "[]")
	case len(parts) >= 2 && parts[0] == "recording":
		s.handleRecordingProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recorded":
		s.handleRecorded(w, r)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		s.handleRecordedFile(w, r, parts[1], apiType)
	case len(parts) >= 2 && parts[0] == "recorded":
		s.handleRecordedProgram(w, r, parts[1:])
	case len(parts) == 2 && parts[0] == "program":
		s.handleProgram(w, r, parts[1])
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

func (s *server) readSchedule() ([]chinachu.ChannelSchedule, error) {
	var schedules []chinachu.ChannelSchedule
	err := storage.ReadJSON(s.paths.Schedule, &schedules, "[]")
	return schedules, err
}

func (s *server) status() map[string]any {
	return map[string]any{
		"connectedCount": 0,
		"feature": map[string]any{
			"previewer":         false,
			"streamer":          false,
			"filer":             true,
			"configurator":      true,
			"normalizationForm": s.cfg.NormalizationForm,
			"goImplementation":  true,
			"partialCompatible": true,
		},
		"system": map[string]any{"core": runtime.NumCPU()},
		"operator": map[string]any{
			"alive": false,
			"pid":   nil,
		},
		"wui": map[string]any{
			"alive": true,
			"pid":   os.Getpid(),
		},
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
