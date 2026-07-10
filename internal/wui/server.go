package wui

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/logging"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/scheduler"
	"strata-pvr/internal/schedulestore"
	"strata-pvr/internal/storage"
	"strata-pvr/internal/system"
)

type Paths struct {
	Config         string
	Database       string
	WebRoot        string
	LogDir         string
	SchedulerPID   string
	OperatorPID    string
	Scheduler      func(context.Context, bool) error
	databaseHandle *sql.DB
}

const legacyRecordingPreviewTailBytes int64 = 3200000

var runFFmpegPreview = func(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = input
	return cmd.Output()
}

var runFFmpegStream = func(ctx context.Context, input io.Reader, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = input
	return startFFmpegStream(cmd)
}

var runFFmpegFileStream = func(ctx context.Context, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	return startFFmpegStream(cmd)
}

func startFFmpegStream(cmd *exec.Cmd) (io.ReadCloser, func() error, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	wait := func() error {
		if err := cmd.Wait(); err != nil {
			if text := strings.TrimSpace(stderr.String()); text != "" {
				return fmt.Errorf("%w: %s", err, text)
			}
			return err
		}
		return nil
	}
	return stdout, wait, nil
}

var runFFprobeFormat = func(ctx context.Context, filePath string) ([]byte, error) {
	return exec.CommandContext(ctx, "ffprobe", "-v", "0", "-show_format", "-of", "json", filePath).Output()
}

var serverStartedAt = time.Now()

func Run(ctx context.Context, paths Paths) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	if paths.Database != "" {
		db, err := database.Open(ctx, paths.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		paths.databaseHandle = db
	}
	servers, err := buildHTTPServers(paths, cfg)
	if err != nil {
		return err
	}
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
			err = s.server.ListenAndServe()
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
	server := &server{
		paths: paths, cfg: cfg, db: paths.databaseHandle, webRoot: findWebRoot(paths.WebRoot), metrics: newMetricHistory(),
		authCache: make(map[[sha256.Size]byte]time.Time), authWorkers: make(chan struct{}, 2),
		hls: newHLSSessionManager(paths),
	}
	server.cleanupPreviewCache(context.Background())
	mux.HandleFunc("/api/", server.handleAPI)
	mux.HandleFunc("/", server.handleStatic)
	var handler http.Handler = mux
	if server.db != nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverContext := database.WithHandle(r.Context(), server.db)
			mux.ServeHTTP(w, r.WithContext(serverContext))
		})
	}
	if auth {
		handler = server.withAuth(handler)
	}
	handler = server.withHostRequired(handler)
	handler = server.withAccessLog(handler)
	return server.withCommonHeaders(handler)
}

type server struct {
	paths       Paths
	cfg         *config.Config
	db          *sql.DB
	webRoot     string
	metrics     *metricHistory
	configMu    sync.Mutex
	authMu      sync.Mutex
	authCache   map[[sha256.Size]byte]time.Time
	authWorkers chan struct{}
	hls         *hlsSessionManager
}

type metricHistory struct {
	mu       sync.Mutex
	samples  []metricSample
	lastCPU  *system.CPUTimes
	lastTime time.Time
}

type metricSample struct {
	Time            time.Time `json:"time"`
	CPUPercent      *float64  `json:"cpuPercent,omitempty"`
	MemoryPercent   *float64  `json:"memoryPercent,omitempty"`
	MemoryUsed      uint64    `json:"memoryUsed"`
	MemoryTotal     uint64    `json:"memoryTotal"`
	StorageRecorded uint64    `json:"storageRecorded"`
	StorageUsed     uint64    `json:"storageUsed"`
	StorageTotal    uint64    `json:"storageTotal"`
	StorageAvail    uint64    `json:"storageAvail"`
}

const (
	metricHistoryWindow  = 6 * time.Hour
	metricSampleInterval = 30 * time.Second
)

func newMetricHistory() *metricHistory {
	return &metricHistory{samples: []metricSample{}}
}

func (h *metricHistory) sample(recordedSize int64, storageUsage system.DiskUsage) metricSample {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	if len(h.samples) > 0 && now.Sub(h.lastTime) < metricSampleInterval {
		return h.samples[len(h.samples)-1]
	}

	var cpuPercent *float64
	if cpuTimes, err := system.GetCPUTimes(); err == nil {
		if h.lastCPU != nil && cpuTimes.Total > h.lastCPU.Total {
			totalDelta := cpuTimes.Total - h.lastCPU.Total
			idleDelta := cpuTimes.Idle - h.lastCPU.Idle
			if totalDelta > 0 && idleDelta <= totalDelta {
				value := clampPercent((float64(totalDelta-idleDelta) / float64(totalDelta)) * 100)
				cpuPercent = &value
			}
		}
		h.lastCPU = &cpuTimes
	}

	var memoryPercent *float64
	var memoryUsed uint64
	var memoryTotal uint64
	if memoryUsage, err := system.GetMemoryUsage(); err == nil {
		memoryUsed = memoryUsage.Used
		memoryTotal = memoryUsage.Total
		if memoryUsage.Total > 0 {
			value := clampPercent((float64(memoryUsage.Used) / float64(memoryUsage.Total)) * 100)
			memoryPercent = &value
		}
	}

	sample := metricSample{
		Time:            now,
		CPUPercent:      cpuPercent,
		MemoryPercent:   memoryPercent,
		MemoryUsed:      memoryUsed,
		MemoryTotal:     memoryTotal,
		StorageRecorded: uint64(max(recordedSize, 0)),
		StorageUsed:     storageUsage.Used,
		StorageTotal:    storageUsage.Size,
		StorageAvail:    storageUsage.Avail,
	}
	h.samples = append(h.samples, sample)
	h.lastTime = now
	h.trimLocked(now)
	return sample
}

func (h *metricHistory) recent() []metricSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trimLocked(time.Now())
	out := make([]metricSample, len(h.samples))
	copy(out, h.samples)
	return out
}

func (h *metricHistory) trimLocked(now time.Time) {
	cutoff := now.Add(-metricHistoryWindow)
	keepFrom := 0
	for keepFrom < len(h.samples) && h.samples[keepFrom].Time.Before(cutoff) {
		keepFrom++
	}
	if keepFrom > 0 {
		copy(h.samples, h.samples[keepFrom:])
		h.samples = h.samples[:len(h.samples)-keepFrom]
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

type runningServer struct {
	server *http.Server
	label  string
}

type serverError struct {
	server runningServer
	err    error
}

func buildHTTPServers(paths Paths, cfg *config.Config) ([]runningServer, error) {
	return []runningServer{{
		server: &http.Server{
			Addr:              listenAddress(cfg.WUIHost, cfg.WUIPort),
			Handler:           newHandler(paths, cfg, cfg.WUIAuthenticationEnabled),
			ReadHeaderTimeout: 10 * time.Second,
		},
		label: "HTTP Server",
	}}, nil
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
		w.Header().Set("Server", "Strata PVR")
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
		s.configMu.Lock()
		accounts := append([]config.WebUser(nil), s.cfg.WUIAccounts...)
		s.configMu.Unlock()
		if len(accounts) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		authorization := r.Header.Get("Authorization")
		cacheKey := sha256.Sum256([]byte(authorization))
		s.authMu.Lock()
		expires, cached := s.authCache[cacheKey]
		valid := cached && time.Now().Before(expires)
		if cached && !valid {
			delete(s.authCache, cacheKey)
		}
		s.authMu.Unlock()
		username, password, ok := r.BasicAuth()
		if ok && !valid {
			select {
			case s.authWorkers <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			s.authMu.Lock()
			expires, cached = s.authCache[cacheKey]
			valid = cached && time.Now().Before(expires)
			s.authMu.Unlock()
			if !valid {
				for _, account := range accounts {
					if account.Username == username && passwordauth.VerifyPassword(account.PasswordHash, password) {
						valid = true
						s.authMu.Lock()
						if len(s.authCache) >= 256 {
							s.authCache = make(map[[sha256.Size]byte]time.Time)
						}
						s.authCache[cacheKey] = time.Now().Add(5 * time.Minute)
						s.authMu.Unlock()
						break
					}
				}
			}
			<-s.authWorkers
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", `Basic realm="Authentication."`)
			legacyHTTPError(w, r, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *server) withHostRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if s.paths.LogDir == "" {
			return
		}
		userAgent := r.Header.Get("User-Agent")
		if userAgent == "" {
			userAgent = "-"
		}
		_ = logging.AppendLine(
			filepath.Join(s.logDir(), "wui"),
			"%d %s:%s %s %q",
			recorder.status,
			r.Method,
			r.URL.RequestURI(),
			s.remoteAddress(r),
			userAgent,
		)
	})
}

func (s *server) remoteAddress(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if strings.HasPrefix(remote, "::ffff:") {
		remote = strings.TrimPrefix(remote, "::ffff:")
	}
	return remote
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if s.webRoot == "" {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	filePath, info, ok := s.staticFileInfo(r.URL.Path)
	if !ok {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	if r.Header.Get("Range") != "" && staticRangeExceedsSize(r.Header.Get("Range"), info.Size()) {
		legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
		return
	}
	switch strings.ToLower(filepath.Ext(r.URL.Path)) {
	case ".ico", ".png":
		w.Header().Set("Cache-Control", "private, max-age=86400")
	}
	if contentType := staticContentType(filePath); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.FileServer(http.Dir(s.webRoot)).ServeHTTP(w, r)
}

func (s *server) staticFileInfo(urlPath string) (string, os.FileInfo, bool) {
	clean := path.Clean("/" + urlPath)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || strings.HasSuffix(urlPath, "/") {
		rel = path.Join(rel, "index.html")
	}
	filePath := filepath.Join(s.webRoot, filepath.FromSlash(rel))
	info, err := os.Stat(filePath)
	if err != nil {
		return filePath, nil, false
	}
	if info.IsDir() {
		filePath = filepath.Join(filePath, "index.html")
		info, err = os.Stat(filePath)
		if err != nil {
			return filePath, nil, false
		}
	}
	return filePath, info, true
}

func staticRangeExceedsSize(header string, size int64) bool {
	if !strings.HasPrefix(header, "bytes=") {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(header, "bytes="), "-", 2)
	if len(parts) != 2 {
		return false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	end := size - 1
	if parts[1] != "" {
		if parsed, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			end = parsed
		}
	}
	return start > size || end > size
}

func staticContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".m2ts":
		return "video/MP2T"
	case ".xspf":
		return "application/xspf+xml"
	default:
		return mime.TypeByExtension(ext)
	}
}

func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	apiType := streamExtension(path)
	if apiType == "" && hasUnsupportedAPIExtension(path) {
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
		return
	}
	path = trimStreamExtension(path)
	parts := splitPath(path)
	if apiType == "" {
		apiType = nativeAPIType(parts)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if len(parts) > 0 && parts[0] == "index" && apiType == "html" {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	if methods, ok := apiAllowedMethods(parts); ok && !methodAllowed(r.Method, methods) {
		w.Header().Set("Allow", strings.Join(methods, ", "))
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}

	switch {
	case len(parts) == 1 && parts[0] == "status":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		writePrettyJSON(w, http.StatusOK, s.status())
	case len(parts) == 1 && parts[0] == "scheduler":
		if !requireAPIType(w, r, apiType, "json", "txt") {
			return
		}
		s.handleScheduler(w, r, apiType)
	case len(parts) == 2 && parts[0] == "scheduler" && parts[1] == "force":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleSchedulerForce(w, r)
	case len(parts) == 1 && parts[0] == "storage":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleStorage(w, r)
	case len(parts) == 1 && parts[0] == "metrics":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleMetrics(w, r)
	case len(parts) == 2 && parts[0] == "log":
		if !requireAPIType(w, r, apiType, "txt") {
			return
		}
		s.handleLog(w, r, parts[1], false)
	case len(parts) == 3 && parts[0] == "log" && parts[2] == "stream":
		if !requireAPIType(w, r, apiType, "txt") {
			return
		}
		s.handleLog(w, r, parts[1], true)
	case len(parts) == 1 && parts[0] == "config":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleConfig(w, r)
	case len(parts) == 1 && parts[0] == "rules":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRules(w, r)
	case len(parts) == 2 && parts[0] == "rules":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRule(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "rules":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRuleAction(w, r, parts[1], parts[2])
	case len(parts) == 1 && parts[0] == "schedule":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleSchedule(w, r)
	case len(parts) == 2 && parts[0] == "schedule" && parts[1] == "programs":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleSchedulePrograms(w, r)
	case len(parts) == 2 && parts[0] == "schedule" && parts[1] == "broadcasting":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleScheduleBroadcasting(w, r)
	case len(parts) == 2 && parts[0] == "schedule":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleScheduleChannel(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "schedule" && parts[2] == "programs":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleScheduleChannelPrograms(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "schedule" && parts[2] == "broadcasting":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleScheduleChannelBroadcasting(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "reserves":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleReservations(w, r)
	case len(parts) >= 2 && parts[0] == "reserves":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleReserveProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recording":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleProgramCollection(w, r, programstore.Recording)
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "preview":
		if !requireAPIType(w, r, apiType, "png", "jpg", "txt") {
			return
		}
		s.handleProgramPreview(w, r, programstore.Recording, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "watch":
		if !requireAPIType(w, r, apiType, "xspf", "m2ts", "mp4") {
			return
		}
		s.handleProgramWatch(w, r, programstore.Recording, parts[1], apiType, true)
	case len(parts) >= 2 && parts[0] == "recording":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRecordingProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recorded":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRecorded(w, r)
	case len(parts) == 2 && parts[0] == "recorded" && parts[1] == "cleanup":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRecordedCleanup(w, r)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		if !requireAPIType(w, r, apiType, "json", "m2ts") {
			return
		}
		s.handleRecordedFile(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "preview":
		if !requireAPIType(w, r, apiType, "png", "jpg", "txt") {
			return
		}
		s.handleProgramPreview(w, r, programstore.Recorded, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "watch":
		if !requireAPIType(w, r, apiType, "mp4", "xspf", "m2ts") {
			return
		}
		s.handleProgramWatch(w, r, programstore.Recorded, parts[1], apiType, false)
	case len(parts) == 4 && parts[0] == "recorded" && parts[2] == "hls":
		if !requireAPIType(w, r, apiType, "m3u8", "ts") {
			return
		}
		s.handleRecordedHLS(w, r, parts[1], parts[3], apiType)
	case len(parts) >= 2 && parts[0] == "recorded":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleRecordedProgram(w, r, parts[1:])
	case len(parts) == 2 && parts[0] == "program":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleProgram(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "channel" && parts[2] == "logo":
		if !requireAPIType(w, r, apiType, "png") {
			return
		}
		s.handleChannelLogo(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "channel" && parts[2] == "watch":
		if !requireAPIType(w, r, apiType, "xspf", "m2ts", "mp4") {
			return
		}
		s.handleChannelWatch(w, r, parts[1], apiType)
	default:
		if len(parts) > 0 && knownAPIResource(parts[0]) {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		legacyHTTPError(w, r, http.StatusNotFound)
	}
}

func (s *server) handleJSONFile(w http.ResponseWriter, r *http.Request, path, empty string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	var v any
	if err := storage.ReadJSON(path, &v, empty); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writeCompactJSON(w, http.StatusOK, v)
}

func (s *server) handleReservations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	reservations, err := reservationstore.Read(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writeCompactJSON(w, http.StatusOK, reservations)
}

func (s *server) handleProgramCollection(w http.ResponseWriter, r *http.Request, collection string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	programs, err := s.readPrograms(r.Context(), collection)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writeCompactJSON(w, http.StatusOK, programs)
}

func (s *server) readPrograms(ctx context.Context, collection string) ([]legacy.Program, error) {
	return programstore.Read(ctx, s.paths.Database, collection)
}

func (s *server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	info, err := os.Stat(s.paths.Database)
	if err != nil && !os.IsNotExist(err) {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	schedule, readErr := schedulestore.Read(r.Context(), s.paths.Database)
	if readErr != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if err == nil {
		lastModified := info.ModTime().UTC().Format(http.TimeFormat)
		w.Header().Set("Last-Modified", lastModified)
		if r.Header.Get("If-Modified-Since") == lastModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	body, err := json.Marshal(schedule)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if acceptsDeflate(r.Header.Get("Accept-Encoding")) {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(body); err != nil {
			_ = zw.Close()
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		if err := zw.Close(); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Encoding", "deflate")
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(compressed.Bytes())
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func acceptsDeflate(value string) bool {
	for _, part := range strings.Split(value, ",") {
		token := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(token, "deflate") {
			return true
		}
	}
	return false
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile(s.paths.Config)
	if err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if _, err := config.ParseDocument(data); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		s.updateStrataConfig(w, r, data)
		return
	}
	data, err = publicStrataConfig(data)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func publicStrataConfig(data []byte) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	web, _ := document["web"].(map[string]any)
	authentication, _ := web["authentication"].(map[string]any)
	users, _ := authentication["users"].([]any)
	for _, value := range users {
		user, _ := value.(map[string]any)
		if _, configured := user["passwordHash"]; configured {
			delete(user, "passwordHash")
			user["passwordConfigured"] = true
		}
	}
	return json.MarshalIndent(document, "", "  ")
}

type strataConfigUpdate struct {
	Schema       string                      `json:"schema"`
	Version      int                         `json:"version"`
	Mirakurun    config.MirakurunSettings    `json:"mirakurun"`
	Recording    config.RecordingSettings    `json:"recording"`
	PreviewCache config.PreviewCacheSettings `json:"previewCache"`
	Web          struct {
		ListenAddress  string `json:"listenAddress"`
		Port           int    `json:"port"`
		Authentication struct {
			Enabled bool `json:"enabled"`
			Users   []struct {
				Username           string `json:"username"`
				Password           string `json:"password"`
				PasswordConfigured bool   `json:"passwordConfigured"`
			} `json:"users"`
		} `json:"authentication"`
	} `json:"web"`
	Services config.ServiceSettings  `json:"services"`
	Advanced config.AdvancedSettings `json:"advanced"`
}

func (s *server) updateStrataConfig(w http.ResponseWriter, r *http.Request, currentData []byte) {
	var raw []byte
	if query := r.URL.Query().Get("json"); query != "" {
		raw = []byte(query)
	} else {
		var err error
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
	}
	var update strataConfigUpdate
	if len(raw) == 0 || json.Unmarshal(raw, &update) != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	current, err := config.ParseDocument(currentData)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if update.Schema != config.StrataSchema || update.Version != current.Version {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	existing := make(map[string]string, len(current.Web.Authentication.Users))
	for _, user := range current.Web.Authentication.Users {
		existing[user.Username] = user.PasswordHash
	}
	users := make([]config.WebUser, 0, len(update.Web.Authentication.Users))
	seen := make(map[string]bool, len(update.Web.Authentication.Users))
	for _, user := range update.Web.Authentication.Users {
		if user.Username == "" || seen[user.Username] {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		seen[user.Username] = true
		hash := existing[user.Username]
		if user.Password != "" {
			hash, err = passwordauth.HashPassword(user.Password)
			if err != nil {
				legacyHTTPError(w, r, http.StatusBadRequest)
				return
			}
		}
		if hash == "" {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		users = append(users, config.WebUser{Username: user.Username, PasswordHash: hash})
	}
	doc := config.Document{
		Schema: update.Schema, Version: update.Version, Mirakurun: update.Mirakurun,
		Recording: update.Recording, PreviewCache: update.PreviewCache, Services: update.Services, Advanced: update.Advanced,
		Web: config.WebSettings{
			ListenAddress: update.Web.ListenAddress, Port: update.Web.Port,
			Authentication: config.AuthenticationSettings{Enabled: update.Web.Authentication.Enabled, Users: users},
		},
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	if _, err := config.ParseDocument(encoded); err != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	loaded, err := config.Parse(encoded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if err := storage.WriteFileAtomic(s.paths.Config, encoded); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	*s.cfg = *loaded
	s.authMu.Lock()
	s.authCache = make(map[[sha256.Size]byte]time.Time)
	s.authMu.Unlock()
	public, err := publicStrataConfig(encoded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(public)
}

func (s *server) handleSchedulePrograms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	programs := []legacy.Program{}
	for _, channel := range schedules {
		programs = append(programs, channel.Programs...)
	}
	writePrettyJSON(w, http.StatusOK, programs)
}

func (s *server) handleScheduleBroadcasting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writePrettyJSON(w, http.StatusOK, broadcastingPrograms(schedules, time.Now()))
}

func (s *server) handleScheduleChannel(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if channel == nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	writePrettyJSON(w, http.StatusOK, channel)
}

func (s *server) handleScheduleChannelPrograms(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if channel == nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	writePrettyJSON(w, http.StatusOK, channel.Programs)
}

func (s *server) handleScheduleChannelBroadcasting(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	channel, err := s.findScheduleChannel(id)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if channel == nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	writePrettyJSON(w, http.StatusOK, broadcastingPrograms([]legacy.ChannelSchedule{*channel}, time.Now()))
}

func (s *server) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	recordedSize, usage, err := s.storageUsage()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writePrettyJSON(w, http.StatusOK, map[string]any{
		"recorded": recordedSize,
		"path":     s.recordedStoragePath(),
		"size":     usage.Size,
		"used":     usage.Used,
		"avail":    usage.Avail,
	})
}

func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	recordedSize, usage, err := s.storageUsage()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	sample := s.metrics.sample(recordedSize, usage)
	writePrettyJSON(w, http.StatusOK, map[string]any{
		"windowSeconds": int64(metricHistoryWindow.Seconds()),
		"sampleSeconds": int64(metricSampleInterval.Seconds()),
		"current":       sample,
		"samples":       s.metrics.recent(),
	})
}

func (s *server) storageUsage() (int64, system.DiskUsage, error) {
	recorded, err := s.readPrograms(context.Background(), programstore.Recorded)
	if err != nil {
		return 0, system.DiskUsage{}, err
	}
	var recordedSize int64
	for _, program := range recorded {
		if program.Recorded == "" {
			continue
		}
		info, err := os.Stat(filepath.FromSlash(program.Recorded))
		if err == nil && info.Mode().IsRegular() {
			recordedSize += allocatedFileSize(info)
		}
	}
	recordedDir := s.recordedStoragePath()
	usage, err := system.GetDiskUsage(recordedDir)
	if err != nil {
		return 0, system.DiskUsage{}, err
	}
	return recordedSize, usage, nil
}

func (s *server) recordedStoragePath() string {
	recordedDir := ""
	if s.cfg != nil {
		recordedDir = s.cfg.RecordedDir
	}
	if recordedDir == "" {
		recordedDir = "."
	}
	return recordedDir
}

func (s *server) handleScheduler(w http.ResponseWriter, r *http.Request, apiType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPut {
		if err := s.runScheduler(r.Context(), false); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
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
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	default:
		result, ok, err := s.schedulerResult(logPath)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			if r.Method != http.MethodHead {
				_ = json.NewEncoder(w).Encode(result)
			}
			return
		}
		writePrettyJSON(w, http.StatusOK, result)
	}
}

func (s *server) handleSchedulerForce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = s.runScheduler(ctx, false)
	}()
	writeCompactJSON(w, http.StatusAccepted, map[string]any{})
}

func (s *server) handleLog(w http.ResponseWriter, r *http.Request, name string, stream bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if name != "wui" && name != "operator" && name != "scheduler" {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	path := filepath.Join(s.logDir(), name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if stream {
		s.streamLog(w, r, path, data)
		return
	}
	_, _ = w.Write(data)
}

func (s *server) streamLog(w http.ResponseWriter, r *http.Request, path string, initial []byte) {
	_, _ = w.Write([]byte(strings.Repeat(" ", 1023)))
	_, _ = w.Write(tailLines(initial, 100))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	offset := int64(len(initial))
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				return
			}
			if info.Size() < offset {
				offset = 0
			}
			if info.Size() == offset {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				return
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				_ = f.Close()
				return
			}
			written, _ := io.Copy(w, f)
			_ = f.Close()
			offset += written
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func tailLines(data []byte, maxLines int) []byte {
	if maxLines <= 0 || len(data) == 0 {
		return nil
	}
	lines := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] != '\n' {
			continue
		}
		lines++
		if lines > maxLines {
			return data[i+1:]
		}
	}
	return data
}

func (s *server) runScheduler(ctx context.Context, simulation bool) error {
	if s.paths.Scheduler != nil {
		return s.paths.Scheduler(ctx, simulation)
	}
	_, err := scheduler.Run(ctx, scheduler.Paths{
		Config:   s.paths.Config,
		Database: s.paths.Database,
		PID:      s.pidPath("scheduler"),
		Log:      filepath.Join(s.logDir(), "scheduler"),
	}, simulation)
	return err
}

func (s *server) schedulerResult(path string) (map[string]any, bool, error) {
	result := map[string]any{
		"time":      int64(0),
		"conflicts": []legacy.Program{},
		"reserves":  []legacy.Program{},
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, false, nil
		}
		return result, false, err
	}
	reservations, err := reservationstore.Read(context.Background(), s.paths.Database)
	if err != nil {
		return result, false, err
	}
	conflicts := []legacy.Program{}
	reserves := []legacy.Program{}
	for _, program := range reservations {
		if program.IsConflict {
			conflicts = append(conflicts, program)
		} else if !program.IsSkip {
			reserves = append(reserves, program)
		}
	}
	result["time"] = info.ModTime().UnixMilli()
	result["conflicts"] = conflicts
	result["reserves"] = reserves
	return result, true, nil
}

func (s *server) handleRules(w http.ResponseWriter, r *http.Request) {
	rules, err := rulestore.ReadRaw(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writePrettyJSON(w, http.StatusOK, rules)
	case http.MethodPost:
		rule, err := decodeRuleRequest(r)
		if err != nil || len(rule) == 0 {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		normalizeRuleEnabled(rule)
		if err := rulestore.Append(r.Context(), s.paths.Database, rule); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusCreated, rule)
	default:
		w.Header().Set("Allow", "HEAD, GET, POST")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRule(w http.ResponseWriter, r *http.Request, num string) {
	index, ok := parseIndex(num)
	if !ok {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	rules, err := rulestore.ReadRaw(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if index < 0 || index >= len(rules) {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writePrettyJSON(w, http.StatusOK, rules[index])
	case http.MethodPut:
		rule, err := decodeRuleRequest(r)
		if err != nil || len(rule) == 0 {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		normalizeRuleEnabled(rule)
		if _, err := rulestore.Update(r.Context(), s.paths.Database, index, rule); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		if _, err := rulestore.Delete(r.Context(), s.paths.Database, index); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "HEAD, GET, PUT, DELETE")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRuleAction(w http.ResponseWriter, r *http.Request, num, action string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	index, ok := parseIndex(num)
	if !ok {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	rules, err := rulestore.ReadRaw(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if index < 0 || index >= len(rules) {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch action {
	case "enable":
		delete(rules[index], "isDisabled")
	case "disable":
		rules[index]["isDisabled"] = json.RawMessage("true")
	default:
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	if _, err := rulestore.Update(r.Context(), s.paths.Database, index, rules[index]); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writeCompactJSON(w, http.StatusOK, map[string]any{})
}

func (s *server) handleRecorded(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	recorded, err := s.readPrograms(r.Context(), programstore.Recorded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		result, err := s.runRecordedCleanup(recorded, true)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		recorded = result.Recorded
	}
	if r.Method == http.MethodPut {
		writePrettyJSON(w, http.StatusOK, recorded)
		return
	}
	writeCompactJSON(w, http.StatusOK, recorded)
}

type recordedCleanupItem struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Recorded string `json:"recorded"`
}

type recordedCleanupResult struct {
	Total      int                   `json:"total"`
	Removed    int                   `json:"removed"`
	Kept       int                   `json:"kept"`
	Items      []recordedCleanupItem `json:"items"`
	Recorded   []legacy.Program      `json:"-"`
	removedIDs []string
}

func (s *server) handleRecordedCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	recorded, err := s.readPrograms(r.Context(), programstore.Recorded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	result, err := s.runRecordedCleanup(recorded, r.Method == http.MethodPut)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writePrettyJSON(w, http.StatusOK, result)
}

func (s *server) runRecordedCleanup(recorded []legacy.Program, apply bool) (recordedCleanupResult, error) {
	result := recordedCleanupResult{
		Total: len(recorded),
		Items: make([]recordedCleanupItem, 0, len(recorded)),
	}
	kept := make([]legacy.Program, 0, len(recorded))
	for _, program := range recorded {
		item := recordedCleanupItem{
			Action:   "keep",
			ID:       program.ID,
			Recorded: program.Recorded,
		}
		if program.Recorded == "" {
			item.Action = "remove"
			result.Removed++
			result.removedIDs = append(result.removedIDs, program.ID)
		} else if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err == nil {
			kept = append(kept, program)
			result.Kept++
		} else {
			item.Action = "remove"
			result.Removed++
			result.removedIDs = append(result.removedIDs, program.ID)
		}
		result.Items = append(result.Items, item)
	}
	result.Recorded = kept
	if apply && result.Removed > 0 {
		for _, id := range result.removedIDs {
			if err := programstore.Remove(context.Background(), s.paths.Database, programstore.Recorded, id); err != nil {
				return result, err
			}
			s.removeProgramPreviewCache(context.Background(), id)
		}
	}
	return result, nil
}

func (s *server) handleReserveProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	reserves, err := reservationstore.Read(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(reserves, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writePrettyJSON(w, http.StatusOK, reserves[index])
	case http.MethodDelete:
		if !reserves[index].IsManualReserved {
			legacyHTTPError(w, r, http.StatusConflict)
			return
		}
		if _, err := reservationstore.Delete(r.Context(), s.paths.Database, id); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	case http.MethodPut:
		if action == "skip" {
			reserves[index].IsSkip = true
		} else if action == "unskip" {
			reserves[index].IsSkip = false
		} else {
			legacyHTTPError(w, r, http.StatusNotFound)
			return
		}
		if err := reservationstore.Upsert(r.Context(), s.paths.Database, reserves[index]); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordingProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	recording, err := s.readPrograms(r.Context(), programstore.Recording)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(recording, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writePrettyJSON(w, http.StatusOK, recording[index])
	case http.MethodDelete:
		if !recording[index].IsManualReserved {
			reserves, err := reservationstore.Read(r.Context(), s.paths.Database)
			if err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
			if reserveIndex := findProgram(reserves, id); reserveIndex != -1 {
				reserves[reserveIndex].IsSkip = true
				if err := reservationstore.Upsert(r.Context(), s.paths.Database, reserves[reserveIndex]); err != nil {
					legacyHTTPError(w, r, http.StatusInternalServerError)
					return
				}
			}
		}
		recording[index].Abort = true
		if err := programstore.Upsert(r.Context(), s.paths.Database, programstore.Recording, recording[index]); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordedProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	recorded, err := s.readPrograms(r.Context(), programstore.Recorded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(recorded, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writePrettyJSON(w, http.StatusOK, withRemovedFlag(recorded[index]))
	case http.MethodDelete:
		if recorded[index].Recorded != "" {
			_ = os.Remove(filepath.FromSlash(recorded[index].Recorded))
		}
		if err := programstore.Remove(r.Context(), s.paths.Database, programstore.Recorded, id); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		s.removeProgramPreviewCache(r.Context(), id)
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordedFile(w http.ResponseWriter, r *http.Request, id, apiType string) {
	recorded, err := s.readPrograms(r.Context(), programstore.Recorded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(recorded, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	path := filepath.FromSlash(recorded[index].Recorded)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch apiType {
		case "m2ts":
			file, err := os.Open(path)
			if err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
			defer file.Close()
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
			setAttachmentFileName(w, path, "m2ts")
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = io.Copy(w, file)
			}
		case "json":
			writePrettyJSON(w, http.StatusOK, fileStatJSON(info))
		default:
			legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
		}
	case http.MethodDelete:
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		if apiType == "m2ts" {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleProgramWatch(w http.ResponseWriter, r *http.Request, collection, id, apiType string, requirePID bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	programs, err := s.readPrograms(r.Context(), collection)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(programs, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	program := programs[index]
	if requirePID && !programHasPID(program) {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	if programIsScrambling(program) {
		legacyHTTPError(w, r, http.StatusConflict)
		return
	}
	if program.Recorded == "" {
		legacyHTTPError(w, r, http.StatusGone)
		return
	}
	filePath := filepath.FromSlash(program.Recorded)
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	switch apiType {
	case "xspf":
		ext := r.URL.Query().Get("ext")
		if ext == "" {
			ext = "m2ts"
		}
		target := xspfTarget(r, ext)
		w.Header().Set("Content-Type", "application/xspf+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xspf"`, id))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			writeXSPF(w, target, legacyRecordedXSPFTitle(program.Title))
		}
	case "m2ts":
		if !s.checkLegacyWatchStart(w, r, filePath) {
			return
		}
		if !requirePID {
			setWatchDownloadHeader(w, r, filePath, apiType)
		}
		if requirePID && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "video/MP2T")
			w.WriteHeader(http.StatusOK)
			streamGrowingFile(w, r, filePath, 61440)
			return
		}
		if watchNeedsTranscode(r) {
			plan, ok := s.prepareLegacyM2TSWatch(w, r, filePath)
			if !ok {
				return
			}
			file, err := os.Open(filePath)
			if err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
			defer file.Close()
			if r.Method == http.MethodHead {
				return
			}
			input := io.Reader(file)
			if plan.HasSourceEnd {
				input = io.NewSectionReader(file, plan.SourceStart, plan.SourceEnd-plan.SourceStart+1)
			} else if plan.SourceStart > 0 {
				if _, err := file.Seek(plan.SourceStart, io.SeekStart); err != nil {
					legacyHTTPError(w, r, http.StatusInternalServerError)
					return
				}
			}
			s.streamFFmpegWithStatus(w, r, input, "m2ts", false, plan.Status)
			return
		}
		if r.URL.Query().Has("ss") {
			if !s.streamLegacyM2TSOffset(w, r, filePath) {
				return
			}
			return
		}
		if r.Header.Get("Range") != "" && staticRangeExceedsSize(r.Header.Get("Range"), info.Size()) {
			legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
			return
		}
		file, err := os.Open(filePath)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "video/MP2T")
		http.ServeContent(w, r, filepath.Base(filePath), info.ModTime(), file)
	case "mp4":
		if !s.checkLegacyWatchStart(w, r, filePath) {
			return
		}
		if !requirePID {
			setWatchDownloadHeader(w, r, filePath, apiType)
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			return
		}
		if requirePID {
			s.streamFFmpeg(w, r, newGrowingFileReader(r.Context(), filePath, 0), "mp4", true)
			return
		}
		s.streamFFmpegFile(w, r, filePath, "mp4")
	default:
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
	}
}

func watchNeedsTranscode(r *http.Request) bool {
	q := r.URL.Query()
	for _, key := range []string{"t", "s", "f", "c:v", "c:a", "b:v", "b:a", "ar", "r"} {
		if q.Get(key) != "" {
			return true
		}
	}
	return false
}

func (s *server) checkLegacyWatchStart(w http.ResponseWriter, r *http.Request, filePath string) bool {
	if !r.URL.Query().Has("ss") {
		return true
	}
	duration, err := probeMediaDuration(r.Context(), filePath)
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "error %v", err)
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return false
	}
	start, _ := strconv.Atoi(legacyWatchStart(r.URL.Query().Get("ss")))
	if float64(start) > duration {
		legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
		return false
	}
	return true
}

func (s *server) checkLegacyRecordedWatchProbe(w http.ResponseWriter, r *http.Request, filePath string) bool {
	data, err := runFFprobeFormat(r.Context(), filePath)
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "error %v", err)
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return false
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return false
	}
	return true
}

func (s *server) streamLegacyM2TSOffset(w http.ResponseWriter, r *http.Request, filePath string) bool {
	plan, ok := s.prepareLegacyM2TSWatch(w, r, filePath)
	if !ok {
		return false
	}
	if r.Method == http.MethodHead {
		return true
	}
	length := int64(-1)
	if plan.HasSourceEnd {
		length = plan.SourceEnd - plan.SourceStart + 1
	}
	if length >= 0 {
		if err := copyFileRange(w, filePath, plan.SourceStart, length); err != nil {
			return false
		}
		return true
	}
	if err := copyFileFromOffset(w, filePath, plan.SourceStart); err != nil {
		return false
	}
	return true
}

type legacyM2TSWatchPlan struct {
	Status       int
	SourceStart  int64
	SourceEnd    int64
	HasSourceEnd bool
}

func (s *server) prepareLegacyM2TSWatch(w http.ResponseWriter, r *http.Request, filePath string) (legacyM2TSWatchPlan, bool) {
	format, err := probeMediaFormat(r.Context(), filePath)
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "error %v", err)
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return legacyM2TSWatchPlan{}, false
	}
	startSeconds, _ := strconv.Atoi(legacyWatchStart(r.URL.Query().Get("ss")))
	bitrate := format.BitRate
	if videoBitrate := legacyBitrateBits(r.URL.Query().Get("b:v")); videoBitrate != 0 {
		audioBitrate := legacyBitrateBits(r.URL.Query().Get("b:a"))
		if audioBitrate == 0 {
			audioBitrate = legacyBitrateBits("96k")
		}
		bitrate = videoBitrate + audioBitrate
	}
	if bitrate == 0 {
		bitrate = format.BitRate
	}
	totalSize := format.Size
	if bitrate != format.BitRate && format.Duration > 0 {
		totalSize = int64(float64(bitrate) / 8 * format.Duration)
	}
	if duration := r.URL.Query().Get("t"); duration != "" && format.Duration > 0 {
		if durationSeconds, err := strconv.Atoi(duration); err == nil {
			totalSize = int64(float64(totalSize) / format.Duration * float64(durationSeconds))
		}
	} else {
		totalSize -= int64(bitrate/8) * int64(startSeconds-2)
	}
	if totalSize < 0 {
		totalSize = 0
	}
	offset := int64(format.BitRate/8) * int64(startSeconds-2)
	offset -= offset % 188
	if offset < 0 {
		offset = 0
	}
	plan := legacyM2TSWatchPlan{Status: http.StatusOK, SourceStart: offset}
	w.Header().Set("Content-Type", "video/MP2T")
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		start, end, ok := parseLegacyWatchRange(rangeHeader, totalSize)
		if !ok {
			legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
			return legacyM2TSWatchPlan{}, false
		}
		sourceStart := int64(math.Round(float64(start) / float64(bitrate) * float64(format.BitRate)))
		sourceEnd := int64(math.Round(float64(end) / float64(bitrate) * float64(format.BitRate)))
		if sourceStart > format.Size || sourceEnd > format.Size {
			legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
			return legacyM2TSWatchPlan{}, false
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		plan.Status = http.StatusPartialContent
		plan.SourceStart = sourceStart
		plan.SourceEnd = sourceEnd
		plan.HasSourceEnd = true
		return plan, true
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
	w.WriteHeader(http.StatusOK)
	return plan, true
}

func parseLegacyWatchRange(header string, totalSize int64) (int64, int64, bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(header, "bytes="), "-", 2)
	if len(parts) != 2 || parts[0] == "" {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	end := totalSize - 2
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, false
		}
	}
	if end < start {
		return 0, 0, false
	}
	return start, end, true
}

func probeMediaDuration(ctx context.Context, filePath string) (float64, error) {
	data, err := runFFprobeFormat(ctx, filePath)
	if err != nil {
		return 0, err
	}
	var value struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(value.Format.Duration, 64)
}

type mediaFormat struct {
	Duration float64
	Size     int64
	BitRate  int64
}

func probeMediaFormat(ctx context.Context, filePath string) (mediaFormat, error) {
	data, err := runFFprobeFormat(ctx, filePath)
	if err != nil {
		return mediaFormat{}, err
	}
	var value struct {
		Format struct {
			Duration string `json:"duration"`
			Size     string `json:"size"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return mediaFormat{}, err
	}
	duration, err := strconv.ParseFloat(value.Format.Duration, 64)
	if err != nil {
		return mediaFormat{}, err
	}
	size, err := strconv.ParseInt(value.Format.Size, 10, 64)
	if err != nil {
		return mediaFormat{}, err
	}
	bitRate, err := strconv.ParseInt(value.Format.BitRate, 10, 64)
	if err != nil {
		return mediaFormat{}, err
	}
	return mediaFormat{Duration: duration, Size: size, BitRate: bitRate}, nil
}

func streamGrowingFile(w http.ResponseWriter, r *http.Request, filePath string, initialBytes int64) {
	offset := int64(0)
	if info, err := os.Stat(filePath); err == nil {
		offset = info.Size() - initialBytes
		if offset < 0 {
			offset = 0
		}
	}
	if err := copyFileFromOffset(w, filePath, offset); err != nil {
		return
	}
	if info, err := os.Stat(filePath); err == nil {
		offset = info.Size()
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			info, err := os.Stat(filePath)
			if err != nil {
				return
			}
			if info.Size() < offset {
				offset = 0
			}
			if info.Size() == offset {
				continue
			}
			before := offset
			if err := copyFileFromOffset(w, filePath, offset); err != nil {
				return
			}
			if info, err := os.Stat(filePath); err == nil {
				offset = info.Size()
			}
			if offset == before {
				continue
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

type growingFileReader struct {
	ctx      context.Context
	filePath string
	offset   int64
}

func newGrowingFileReader(ctx context.Context, filePath string, offset int64) io.Reader {
	if offset < 0 {
		offset = 0
	}
	return &growingFileReader{ctx: ctx, filePath: filePath, offset: offset}
}

func (r *growingFileReader) Read(p []byte) (int, error) {
	for {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
		file, err := os.Open(r.filePath)
		if err != nil {
			return 0, err
		}
		if _, err := file.Seek(r.offset, io.SeekStart); err != nil {
			_ = file.Close()
			return 0, err
		}
		n, readErr := file.Read(p)
		_ = file.Close()
		if n > 0 {
			r.offset += int64(n)
			return n, nil
		}
		if readErr != nil && readErr != io.EOF {
			return 0, readErr
		}
		if info, err := os.Stat(r.filePath); err == nil && info.Size() < r.offset {
			r.offset = 0
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return 0, r.ctx.Err()
		case <-timer.C:
		}
	}
}

func copyFileFromOffset(w io.Writer, filePath string, offset int64) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(w, file)
	return err
}

func copyFileRange(w io.Writer, filePath string, offset, length int64) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	_, err = io.CopyN(w, file, length)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func setWatchDownloadHeader(w http.ResponseWriter, r *http.Request, filePath, apiType string) {
	if r.URL.Query().Get("mode") != "download" {
		return
	}
	ext := r.URL.Query().Get("ext")
	if ext == "" {
		ext = apiType
	}
	setAttachmentFileName(w, filePath, ext)
}

func setAttachmentFileName(w http.ResponseWriter, filePath, ext string) {
	base := filepath.Base(filePath)
	if suffix := filepath.Ext(base); suffix != "" {
		base = strings.TrimSuffix(base, suffix)
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(base+"."+ext))
}

func (s *server) handleProgramPreview(w http.ResponseWriter, r *http.Request, collection, id, apiType string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if apiType != "png" && apiType != "jpg" && apiType != "txt" {
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
		return
	}
	programs, err := s.readPrograms(r.Context(), collection)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(programs, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	program := programs[index]
	if collection == programstore.Recording && !programHasPID(program) {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	if programIsScrambling(program) {
		legacyHTTPError(w, r, http.StatusConflict)
		return
	}
	if program.Recorded == "" {
		legacyHTTPError(w, r, http.StatusGone)
		return
	}
	filePath := filepath.FromSlash(program.Recorded)
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}

	width, height := previewSize(r)
	codec := "mjpeg"
	if r.URL.Query().Get("type") == "png" || apiType == "png" {
		codec = "png"
	}
	recording := collection == programstore.Recording
	cacheKey := previewCacheKey(id, width, height, codec, previewPosition(r))
	if !recording && s.paths.Database != "" {
		if output, ok := s.readPreviewCache(r.Context(), cacheKey, filePath, info); ok {
			s.writePreviewResponse(w, apiType, codec, output)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	output, err := s.runProgramPreview(ctx, recording, filePath, width, height, codec, r)
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "[previewer] %v", err)
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	if !recording && s.paths.Database != "" {
		if err := s.storePreviewCache(r.Context(), cacheKey, id, filePath, info, codec, output); err != nil {
			_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "[preview-cache] %v", err)
		}
	}
	s.writePreviewResponse(w, apiType, codec, output)
}

func (s *server) writePreviewResponse(w http.ResponseWriter, apiType, codec string, output []byte) {
	switch apiType {
	case "png":
		w.Header().Set("Content-Type", "image/png")
	case "jpg":
		w.Header().Set("Content-Type", "image/jpeg")
	case "txt":
		w.Header().Set("Content-Type", "text/plain")
	}
	w.WriteHeader(http.StatusOK)
	if apiType == "txt" {
		if codec == "png" {
			_, _ = w.Write([]byte("data:image/png;base64,"))
		} else {
			_, _ = w.Write([]byte("data:image/jpeg;base64,"))
		}
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(output)))
		return
	}
	_, _ = w.Write(output)
}

func previewCacheKey(programID, width, height, codec, position string) string {
	value := sha256.Sum256([]byte(strings.Join([]string{programID, width, height, codec, position}, "\x00")))
	return hex.EncodeToString(value[:])
}

func (s *server) previewCacheDir() string {
	return filepath.Join(filepath.Dir(s.paths.Database), ".cache", "previews")
}

func (s *server) cleanupPreviewCache(ctx context.Context) {
	if s.paths.Database == "" {
		return
	}
	entries, err := os.ReadDir(s.previewCacheDir())
	if err != nil && !os.IsNotExist(err) {
		return
	}
	ctx = database.WithHandle(ctx, s.db)
	db, release, err := database.Acquire(ctx, s.paths.Database)
	if err != nil {
		return
	}
	defer release()
	referenced, err := database.ListPreviewCacheFiles(ctx, db)
	if err != nil {
		return
	}
	existing := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			existing[entry.Name()] = struct{}{}
		}
	}
	_, _ = database.RemoveMissingPreviewCacheFiles(ctx, db, existing)
	cacheEntries, _ := database.ListPreviewCacheEntries(ctx, db)
	removed := s.previewCacheRetention(cacheEntries, entries, time.Now())
	cacheKeys := make([]string, 0, len(removed))
	for _, entry := range removed {
		cacheKeys = append(cacheKeys, entry.CacheKey)
	}
	_ = database.DeletePreviewCacheEntries(ctx, db, cacheKeys)
	for _, entry := range removed {
		if filepath.Base(entry.FileName) == entry.FileName {
			_ = os.Remove(filepath.Join(s.previewCacheDir(), entry.FileName))
		}
	}
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || (extension != ".jpg" && extension != ".png") {
			continue
		}
		if _, ok := referenced[entry.Name()]; !ok {
			_ = os.Remove(filepath.Join(s.previewCacheDir(), entry.Name()))
		}
	}
}

func (s *server) previewCacheRetention(cacheEntries []database.PreviewCacheEntry, files []os.DirEntry, now time.Time) []database.PreviewCacheEntry {
	if s.cfg.PreviewCacheMaxAgeDays == 0 && s.cfg.PreviewCacheMaxSizeMB == 0 {
		return nil
	}
	sizes := make(map[string]int64, len(files))
	var total int64
	for _, file := range files {
		info, err := file.Info()
		if err == nil && !file.IsDir() {
			sizes[file.Name()] = info.Size()
			total += info.Size()
		}
	}
	maxSize := int64(s.cfg.PreviewCacheMaxSizeMB) * 1024 * 1024
	cutoff := now.AddDate(0, 0, -s.cfg.PreviewCacheMaxAgeDays)
	removed := make([]database.PreviewCacheEntry, 0)
	for _, entry := range cacheEntries {
		accessed, err := time.Parse("2006-01-02T15:04:05.000Z", entry.AccessedAt)
		expired := s.cfg.PreviewCacheMaxAgeDays > 0 && err == nil && accessed.Before(cutoff)
		overSize := maxSize > 0 && total > maxSize
		if !expired && !overSize {
			continue
		}
		removed = append(removed, entry)
		total -= sizes[entry.FileName]
	}
	return removed
}

func (s *server) removeProgramPreviewCache(ctx context.Context, programID string) {
	if s.paths.Database == "" {
		return
	}
	db, release, err := database.Acquire(ctx, s.paths.Database)
	if err != nil {
		return
	}
	defer release()
	files, err := database.RemovePreviewCacheForProgram(ctx, db, programID)
	if err != nil {
		return
	}
	for _, fileName := range files {
		if filepath.Base(fileName) == fileName {
			_ = os.Remove(filepath.Join(s.previewCacheDir(), fileName))
		}
	}
}

func (s *server) readPreviewCache(ctx context.Context, cacheKey, sourcePath string, info os.FileInfo) ([]byte, bool) {
	db, release, err := database.Acquire(ctx, s.paths.Database)
	if err != nil {
		return nil, false
	}
	defer release()
	entry, found, err := database.FindPreviewCache(ctx, db, cacheKey)
	if err != nil || !found || entry.SourcePath != sourcePath || entry.SourceSize != info.Size() || entry.SourceMTime != info.ModTime().UnixNano() || filepath.Base(entry.FileName) != entry.FileName {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(s.previewCacheDir(), entry.FileName))
	if err != nil {
		_ = database.DeletePreviewCache(ctx, db, cacheKey)
	}
	return data, err == nil
}

func (s *server) storePreviewCache(ctx context.Context, cacheKey, programID, sourcePath string, info os.FileInfo, codec string, output []byte) error {
	if err := os.MkdirAll(s.previewCacheDir(), 0o755); err != nil {
		return err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	extension := ".jpg"
	if codec == "png" {
		extension = ".png"
	}
	fileName := hex.EncodeToString(random) + extension
	cachePath := filepath.Join(s.previewCacheDir(), fileName)
	if err := storage.WriteFileAtomic(cachePath, output); err != nil {
		return err
	}
	db, release, err := database.Acquire(ctx, s.paths.Database)
	if err != nil {
		_ = os.Remove(cachePath)
		return err
	}
	defer release()
	previous, err := database.StorePreviewCache(ctx, db, database.PreviewCacheEntry{
		CacheKey: cacheKey, ProgramID: programID, SourcePath: sourcePath,
		SourceSize: info.Size(), SourceMTime: info.ModTime().UnixNano(), FileName: fileName,
	})
	if err != nil {
		_ = os.Remove(cachePath)
		return err
	}
	if previous != "" && previous != fileName && filepath.Base(previous) == previous {
		_ = os.Remove(filepath.Join(s.previewCacheDir(), previous))
	}
	return nil
}

func (s *server) runProgramPreview(ctx context.Context, recording bool, filePath, width, height, codec string, r *http.Request) ([]byte, error) {
	if recording {
		input, err := legacyRecordingPreviewInput(filePath)
		if err != nil {
			return nil, err
		}
		return runFFmpegPreview(ctx, input,
			"-f", "mpegts",
			"-r", "10",
			"-i", "pipe:0",
			"-ss", "1.5",
			"-r", "10",
			"-frames:v", "1",
			"-f", "image2",
			"-codec:v", codec,
			"-an",
			"-s", width+"x"+height,
			"-map", "0:0",
			"-y", "pipe:1",
		)
	}
	pos := previewPosition(r)
	return runFFmpegPreview(ctx, nil,
		"-f", "mpegts",
		"-ss", pos,
		"-r", "10",
		"-i", filePath,
		"-ss", "1.5",
		"-r", "10",
		"-frames:v", "1",
		"-c:v", codec,
		"-an",
		"-f", "image2",
		"-s", width+"x"+height,
		"-map", "0:0",
		"-y", "pipe:1",
	)
}

func legacyRecordingPreviewInput(filePath string) (io.Reader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - legacyRecordingPreviewTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

func previewSize(r *http.Request) (string, string) {
	width := r.URL.Query().Get("width")
	height := r.URL.Query().Get("height")
	size := r.URL.Query().Get("size")
	if parts := strings.Split(size, "x"); len(parts) == 2 && validPreviewDimension(parts[0]) && validPreviewDimension(parts[1]) {
		width = parts[0]
		height = parts[1]
	}
	if !validPreviewDimension(width) {
		width = "320"
	}
	if !validPreviewDimension(height) {
		height = "180"
	}
	return width, height
}

func validPreviewDimension(value string) bool {
	if value == "" || len(value) > 4 {
		return false
	}
	n, err := strconv.Atoi(value)
	return err == nil && n > 0
}

func previewPosition(r *http.Request) string {
	pos := r.URL.Query().Get("pos")
	n, err := strconv.Atoi(pos)
	if err != nil {
		n = 5
	}
	return strconv.FormatFloat(float64(n)-1.5, 'f', -1, 64)
}

func (s *server) handleProgram(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut {
		w.Header().Set("Allow", "HEAD, GET, PUT")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if program := legacy.GetProgramByID(id, schedules, nil); program != nil {
		if r.Method == http.MethodPut {
			s.reserveProgram(w, r, *program)
			return
		}
		writePrettyJSON(w, http.StatusOK, program)
		return
	}
	if r.Method == http.MethodPut {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	legacyHTTPError(w, r, http.StatusNotFound)
}

func (s *server) reserveProgram(w http.ResponseWriter, r *http.Request, program legacy.Program) {
	reserves, err := reservationstore.Read(r.Context(), s.paths.Database)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if findProgram(reserves, program.ID) != -1 {
		legacyHTTPError(w, r, http.StatusConflict)
		return
	}
	program.IsManualReserved = true
	program.OneSeg = r.URL.Query().Get("mode") == "1seg"
	if err := reservationstore.Upsert(r.Context(), s.paths.Database, program); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writeCompactJSON(w, http.StatusOK, map[string]any{})
}

func (s *server) handleChannelLogo(w http.ResponseWriter, r *http.Request, id, apiType string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	if apiType != "png" {
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
		return
	}
	channel, ok := s.findChannel(id)
	if !ok {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	serviceID, err := strconv.ParseInt(channel.ID, 36, 64)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	client.UserAgent = mirakurun.StrataUserAgent("wui")
	body, err := client.LogoImage(r.Context(), serviceID)
	if err != nil {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
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
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	channel, ok := s.findChannel(id)
	if !ok {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	switch apiType {
	case "xspf":
		ext := r.URL.Query().Get("ext")
		if ext == "" {
			ext = "m2ts"
		}
		target := xspfTarget(r, ext)
		w.Header().Set("Content-Type", "application/xspf+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.xspf"`, channel.ID))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			writeXSPF(w, target, channel.Name)
		}
	case "m2ts":
		serviceID, err := strconv.ParseInt(channel.ID, 36, 64)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client.UserAgent = mirakurun.StrataUserAgent("wui")
		body, err := client.ServiceStream(r.Context(), serviceID, true)
		if err != nil {
			legacyHTTPError(w, r, http.StatusServiceUnavailable)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Type", "video/MP2T")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, body)
		}
	case "mp4":
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "video/mp4")
			w.WriteHeader(http.StatusOK)
			return
		}
		serviceID, err := strconv.ParseInt(channel.ID, 36, 64)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client.UserAgent = mirakurun.StrataUserAgent("wui")
		body, err := client.ServiceStream(r.Context(), serviceID, true)
		if err != nil {
			legacyHTTPError(w, r, http.StatusServiceUnavailable)
			return
		}
		defer body.Close()
		s.streamFFmpeg(w, r, body, "mp4", true)
	default:
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
	}
}

func (s *server) streamFFmpeg(w http.ResponseWriter, r *http.Request, input io.Reader, format string, live bool) {
	s.streamFFmpegWithStatus(w, r, input, format, live, http.StatusOK)
}

func (s *server) streamFFmpegWithStatus(w http.ResponseWriter, r *http.Request, input io.Reader, format string, live bool, status int) {
	if format == "mp4" && r.URL.Query().Get("c:v") == "" {
		if _, err := detectedH264Encoder(); err != nil {
			_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "H.264 encoder detection failed: %v", err)
			legacyHTTPError(w, r, http.StatusServiceUnavailable)
			return
		}
	}
	args := watchFFmpegArgs(r, format, live)
	output, wait, err := runFFmpegStream(r.Context(), input, args...)
	s.streamFFmpegOutput(w, r, output, wait, err, args, format, status)
}

func (s *server) streamFFmpegFile(w http.ResponseWriter, r *http.Request, filePath string, format string) {
	if format == "mp4" && r.URL.Query().Get("c:v") == "" {
		if _, err := detectedH264Encoder(); err != nil {
			_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "H.264 encoder detection failed: %v", err)
			legacyHTTPError(w, r, http.StatusServiceUnavailable)
			return
		}
	}
	args := watchFFmpegFileArgs(r, format, filePath)
	output, wait, err := runFFmpegFileStream(r.Context(), args...)
	s.streamFFmpegOutput(w, r, output, wait, err, args, format, http.StatusOK)
}

func (s *server) streamFFmpegOutput(w http.ResponseWriter, r *http.Request, output io.ReadCloser, wait func() error, err error, args []string, format string, status int) {
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "SPAWN: ffmpeg %s: %v", strings.Join(args, " "), err)
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	defer output.Close()
	_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "SPAWN: ffmpeg %s", strings.Join(args, " "))
	switch format {
	case "mp4":
		w.Header().Set("Content-Type", "video/mp4")
	default:
		w.Header().Set("Content-Type", "video/MP2T")
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, output)
	if err := wait(); err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "#ffmpeg: %v", err)
	}
}

func watchFFmpegArgs(r *http.Request, format string, live bool) []string {
	return watchFFmpegArgsForInput(r, format, live, "pipe:0", false)
}

func watchFFmpegFileArgs(r *http.Request, format string, filePath string) []string {
	return watchFFmpegArgsForInput(r, format, false, filePath, true)
}

func watchFFmpegArgsForInput(r *http.Request, format string, live bool, input string, seekBeforeInput bool) []string {
	q := r.URL.Query()
	videoCodec := q.Get("c:v")
	audioCodec := q.Get("c:a")
	container := q.Get("f")
	videoBitrate := q.Get("b:v")
	audioBitrate := q.Get("b:a")
	if format == "mp4" {
		container = "mp4"
		if videoCodec == "" {
			videoCodec, _ = detectedH264Encoder()
		}
		if audioCodec == "" {
			audioCodec = "aac"
		}
	} else {
		container = "mpegts"
		if videoCodec == "" {
			videoCodec = "copy"
		}
		if audioCodec == "" {
			audioCodec = "copy"
		}
	}
	if !live && videoBitrate != "" && (audioCodec == "copy" || audioBitrate == "") {
		audioCodec = ""
		audioBitrate = "96k"
	}
	args := []string{}
	if !q.Has("debug") {
		args = append(args, "-v", "error")
	}
	if live {
		args = append(args, "-re")
	}
	args = append(args, "-fflags", "+genpts+discardcorrupt", "-err_detect", "ignore_err", "-analyzeduration", "10000000", "-probesize", "10000000")
	if !live && seekBeforeInput {
		args = append(args, "-ss", legacyWatchStart(q.Get("ss")))
	}
	args = append(args, "-f", "mpegts", "-i", input, "-threads", "0")
	if !live {
		if !seekBeforeInput {
			args = append(args, "-ss", legacyWatchStart(q.Get("ss")))
		}
	}
	if duration := q.Get("t"); duration != "" {
		args = append(args, "-t", duration)
	}
	if format == "mp4" {
		args = append(args, "-map", "0:v:0", "-map", watchAudioMap(q.Get("audio")), "-sn", "-dn")
	}
	args = append(args, "-filter:v", "yadif")
	if videoCodec != "" {
		args = append(args, "-c:v", videoCodec)
	}
	if audioCodec != "" {
		args = append(args, "-c:a", audioCodec)
		if format == "mp4" && audioCodec != "copy" {
			args = append(args, "-ac", "2")
		}
	}
	if size := q.Get("s"); size != "" {
		args = append(args, "-s", size)
	}
	for _, key := range []string{"r", "ar"} {
		if value := q.Get(key); value != "" {
			args = append(args, "-"+key, value)
		}
	}
	if videoBitrate != "" {
		args = append(args, "-b:v", videoBitrate, "-minrate:v", videoBitrate, "-maxrate:v", videoBitrate)
		if !live {
			args = append(args, "-bufsize:v", strconv.FormatInt(legacyBitrateBits(videoBitrate)*8, 10))
		}
	}
	if audioBitrate != "" {
		args = append(args, "-b:a", audioBitrate, "-minrate:a", audioBitrate, "-maxrate:a", audioBitrate)
		if !live {
			args = append(args, "-bufsize:a", strconv.FormatInt(legacyBitrateBits(audioBitrate)*8, 10))
		}
	}
	if videoCodec == "libx264" {
		args = appendH264CompatibilityArgs(args, videoCodec)
	} else if videoCodec == "libopenh264" {
		args = appendH264CompatibilityArgs(args, videoCodec)
	}
	if container == "mp4" {
		args = append(args, "-movflags", "frag_keyframe+empty_moov+faststart+default_base_moof")
	}
	return append(args, "-y", "-f", container, "pipe:1")
}

func watchAudioMap(value string) string {
	if value == "secondary" {
		return "0:a:1?"
	}
	return "0:a:0?"
}

func legacyWatchStart(value string) string {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 2 {
		return "2"
	}
	return strconv.Itoa(seconds)
}

func legacyBitrateBits(value string) int64 {
	if len(value) < 2 {
		return 0
	}
	unit := value[len(value)-1]
	if unit != 'k' && unit != 'K' && unit != 'm' && unit != 'M' {
		return 0
	}
	n, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
	if err != nil {
		return 0
	}
	if unit == 'm' || unit == 'M' {
		return n * 1024 * 1024
	}
	return n * 1024
}

func (s *server) findChannel(id string) (legacy.ChannelSchedule, bool) {
	schedules, err := s.readSchedule()
	if err != nil {
		return legacy.ChannelSchedule{}, false
	}
	for _, channel := range schedules {
		if channel.ID == id {
			return channel, true
		}
	}
	return legacy.ChannelSchedule{}, false
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

func (s *server) readSchedule() ([]legacy.ChannelSchedule, error) {
	return schedulestore.Read(context.Background(), s.paths.Database)
}

func (s *server) findScheduleChannel(id string) (*legacy.ChannelSchedule, error) {
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

func broadcastingPrograms(schedules []legacy.ChannelSchedule, now time.Time) []legacy.Program {
	nowMS := now.UnixMilli()
	programs := []legacy.Program{}
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
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	now := time.Now()
	return map[string]any{
		"connectedCount": 0,
		"feature": map[string]any{
			"previewer":         true,
			"streamer":          true,
			"filer":             true,
			"configurator":      true,
			"normalizationForm": s.cfg.NormalizationForm,
		},
		"system": map[string]any{
			"core":          runtime.NumCPU(),
			"os":            runtime.GOOS,
			"arch":          runtime.GOARCH,
			"goVersion":     runtime.Version(),
			"goroutines":    runtime.NumGoroutine(),
			"pid":           os.Getpid(),
			"startedAt":     serverStartedAt.Format(time.RFC3339),
			"uptimeSeconds": int64(now.Sub(serverStartedAt).Seconds()),
			"memory": map[string]any{
				"alloc":      mem.Alloc,
				"totalAlloc": mem.TotalAlloc,
				"sys":        mem.Sys,
				"heapAlloc":  mem.HeapAlloc,
				"heapSys":    mem.HeapSys,
				"heapIdle":   mem.HeapIdle,
				"heapInuse":  mem.HeapInuse,
				"stackInuse": mem.StackInuse,
				"numGC":      mem.NumGC,
				"lastGC":     mem.LastGC,
			},
		},
		"operator": map[string]any{
			"alive": pidAlive(operatorPID),
			"pid":   operatorPID,
		},
		"wui": map[string]any{
			"alive": false,
			"pid":   nil,
		},
	}
}

func pidAlive(pid *int) bool {
	if pid == nil {
		return false
	}
	return system.ProcessAlive(*pid)
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

func writePrettyJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeCompactJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func legacyHTTPError(w http.ResponseWriter, r *http.Request, status int) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		return
	}
	http.Error(w, http.StatusText(status), status)
}

func requireAPIType(w http.ResponseWriter, r *http.Request, apiType string, allowed ...string) bool {
	for _, value := range allowed {
		if apiType == value {
			return true
		}
	}
	legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
	return false
}

func decodeJSONObject(body io.Reader) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	err := json.NewDecoder(body).Decode(&value)
	return value, err
}

func decodeRuleRequest(r *http.Request) (map[string]json.RawMessage, error) {
	if r.Body != nil && r.ContentLength != 0 {
		return decodeJSONObject(r.Body)
	}
	values := r.URL.Query()
	if len(values) == 0 {
		return nil, io.EOF
	}
	rule := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		if len(value) == 0 {
			continue
		}
		raw := queryRuleValue(value)
		rule[key] = raw
	}
	return rule, nil
}

func queryRuleValue(values []string) json.RawMessage {
	if len(values) > 1 {
		b, _ := json.Marshal(values)
		return b
	}
	value := values[0]
	if value == "" {
		return json.RawMessage(`""`)
	}
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	b, _ := json.Marshal(value)
	return b
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

func withRemovedFlag(program legacy.Program) map[string]any {
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
	value := map[string]any{
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
	enrichFileStatJSON(value, info)
	return value
}

func programHasPID(program legacy.Program) bool {
	return program.PID != 0
}

func programIsScrambling(program legacy.Program) bool {
	raw, ok := program.Raw["tuner"]
	if !ok || len(raw) == 0 {
		return false
	}
	var tuner struct {
		IsScrambling bool `json:"isScrambling"`
	}
	if err := json.Unmarshal(raw, &tuner); err != nil {
		return false
	}
	return tuner.IsScrambling
}

func writeXSPF(w io.Writer, target, title string) {
	fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(w, "<playlist version=\"1\" xmlns=\"http://xspf.org/ns/0/\">\n")
	fmt.Fprintf(w, "<trackList>\n")
	fmt.Fprintf(w, "<track>\n<location>%s</location>\n<title>%s</title>\n</track>\n", legacyXSPFLocation(target), title)
	fmt.Fprintf(w, "</trackList>\n")
	fmt.Fprintf(w, "</playlist>\n")
}

func xspfTarget(r *http.Request, ext string) string {
	prefix := r.URL.Query().Get("prefix")
	path := ""
	if prefix != "" {
		path = prefix + "watch." + ext
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
			return path
		}
	} else {
		path = strings.TrimSuffix(r.URL.Path, ".xspf") + "." + ext
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + path
}

func legacyXSPFLocation(value string) string {
	return strings.ReplaceAll(value, "&", "&amp;")
}

func legacyRecordedXSPFTitle(value string) string {
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return value
}

func reversePrograms(programs []legacy.Program) {
	for i, j := 0, len(programs)-1; i < j; i, j = i+1, j-1 {
		programs[i], programs[j] = programs[j], programs[i]
	}
}

func findWebRoot(configured string) string {
	candidates := []string{configured, "web"}
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

func apiAllowedMethods(parts []string) ([]string, bool) {
	if len(parts) == 0 {
		return nil, false
	}
	switch {
	case len(parts) == 1 && parts[0] == "status":
		return []string{"GET"}, true
	case len(parts) == 1 && parts[0] == "scheduler":
		return []string{"GET", "PUT"}, true
	case len(parts) == 2 && parts[0] == "scheduler" && parts[1] == "force":
		return []string{"PUT"}, true
	case len(parts) == 1 && parts[0] == "storage":
		return []string{"GET"}, true
	case len(parts) == 1 && parts[0] == "metrics":
		return []string{"GET"}, true
	case len(parts) == 2 && parts[0] == "log":
		return []string{"GET"}, true
	case len(parts) == 3 && parts[0] == "log" && parts[2] == "stream":
		return []string{"GET"}, true
	case len(parts) == 1 && parts[0] == "config":
		return []string{"GET", "PUT"}, true
	case len(parts) == 1 && parts[0] == "rules":
		return []string{"GET", "POST"}, true
	case len(parts) == 2 && parts[0] == "rules":
		return []string{"GET", "PUT", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "rules":
		return []string{"PUT"}, true
	case len(parts) == 1 && parts[0] == "schedule":
		return []string{"HEAD", "GET"}, true
	case len(parts) == 2 && parts[0] == "schedule" && (parts[1] == "programs" || parts[1] == "broadcasting"):
		return []string{"GET"}, true
	case len(parts) == 2 && parts[0] == "schedule":
		return []string{"GET"}, true
	case len(parts) == 3 && parts[0] == "schedule" && (parts[2] == "programs" || parts[2] == "broadcasting"):
		return []string{"GET"}, true
	case len(parts) == 1 && parts[0] == "reserves":
		return []string{"GET"}, true
	case len(parts) == 2 && parts[0] == "reserves":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "reserves":
		return []string{"PUT"}, true
	case len(parts) == 1 && parts[0] == "recording":
		return []string{"GET"}, true
	case len(parts) == 2 && parts[0] == "recording":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "recording" && (parts[2] == "preview" || parts[2] == "watch"):
		return []string{"GET"}, true
	case len(parts) == 1 && parts[0] == "recorded":
		return []string{"GET", "PUT"}, true
	case len(parts) == 2 && parts[0] == "recorded" && parts[1] == "cleanup":
		return []string{"GET", "PUT"}, true
	case len(parts) == 2 && parts[0] == "recorded":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "recorded" && (parts[2] == "preview" || parts[2] == "watch"):
		return []string{"GET"}, true
	case len(parts) == 4 && parts[0] == "recorded" && parts[2] == "hls":
		return []string{"GET", "HEAD", "DELETE"}, true
	case len(parts) == 2 && parts[0] == "program":
		return []string{"GET", "PUT"}, true
	case len(parts) == 3 && parts[0] == "channel" && (parts[2] == "logo" || parts[2] == "watch"):
		return []string{"GET"}, true
	default:
		return nil, false
	}
}

func methodAllowed(method string, allowed []string) bool {
	for _, value := range allowed {
		if method == value {
			return true
		}
	}
	return false
}

func knownAPIResource(name string) bool {
	switch name {
	case "status", "scheduler", "storage", "metrics", "log", "config", "rules", "schedule", "reserves", "recording", "recorded", "program", "channel":
		return true
	default:
		return false
	}
}

func nativeAPIType(parts []string) string {
	if len(parts) >= 2 && parts[0] == "log" {
		return "txt"
	}
	if len(parts) == 3 && parts[2] == "preview" {
		return "png"
	}
	if len(parts) == 3 && parts[0] == "channel" && parts[2] == "logo" {
		return "png"
	}
	return "json"
}

func streamExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash {
		ext := path[dot+1:]
		if isStreamExtension(ext) {
			return ext
		}
	}
	return ""
}

func trimStreamExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash && isStreamExtension(path[dot+1:]) {
		return path[:dot]
	}
	return path
}

func isStreamExtension(ext string) bool {
	return ext == "xspf" || ext == "m2ts" || ext == "mp4" || ext == "m3u8" || ext == "ts"
}

func hasUnsupportedAPIExtension(path string) bool {
	last := path[strings.LastIndex(path, "/")+1:]
	return strings.Contains(last, ".")
}

func findProgram(programs []legacy.Program, id string) int {
	for i := range programs {
		if programs[i].ID == id {
			return i
		}
	}
	return -1
}

func removeProgram(programs []legacy.Program, id string) []legacy.Program {
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
