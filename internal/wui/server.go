package wui

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
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

var runFFmpegPreview = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "ffmpeg", args...).Output()
}

var runFFmpegStream = func(ctx context.Context, input io.Reader, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdin = input
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	return stdout, cmd.Wait, nil
}

var runFFprobeFormat = func(ctx context.Context, filePath string) ([]byte, error) {
	return exec.CommandContext(ctx, "ffprobe", "-v", "0", "-show_format", "-of", "json", filePath).Output()
}

func Run(ctx context.Context, paths Paths) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	if err := system.DropPrivileges(cfg.UID, cfg.GID); err != nil {
		return err
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
			if s.tls {
				err = s.server.ListenAndServeTLS("", "")
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
	handler = server.withMethodOverride(handler)
	handler = server.withHostRequired(handler)
	handler = server.withAccessLog(handler)
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

func buildHTTPServers(paths Paths, cfg *config.Config) ([]runningServer, error) {
	servers := []runningServer{}
	if cfg.WUIPort != nil {
		tls := cfg.WUITlsKeyPath != "" && cfg.WUITlsCertPath != ""
		label := "HTTP Server"
		if tls {
			label = "HTTPS Server"
		}
		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		servers = append(servers, runningServer{
			server: &http.Server{
				Addr:              listenAddress(cfg.WUIHost, *cfg.WUIPort),
				Handler:           newHandler(paths, cfg, true),
				TLSConfig:         tlsConfig,
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
		host := cfg.WUIOpenHost
		if host == "" {
			host = detectPrivateIPv4()
		}
		servers = append(servers, runningServer{
			server: &http.Server{
				Addr:              listenAddress(host, port),
				Handler:           newHandler(paths, cfg, false),
				ReadHeaderTimeout: 10 * time.Second,
			},
			label: "HTTP Open Server",
		})
	}
	return servers, nil
}

func detectPrivateIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if ip := privateIPv4FromAddrs(addrs); ip != "" {
			return ip
		}
	}
	return ""
}

func privateIPv4FromAddrs(addrs []net.Addr) string {
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		ip = ip.To4()
		if ip != nil && ip.IsPrivate() {
			return ip.String()
		}
	}
	return ""
}

func buildTLSConfig(cfg *config.Config) (*tls.Config, error) {
	if cfg.WUITlsKeyPath == "" || cfg.WUITlsCertPath == "" {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS10}
	cert, err := loadTLSCertificate(cfg.WUITlsCertPath, cfg.WUITlsKeyPath, cfg.WUITlsPassphrase)
	if err != nil {
		return nil, err
	}
	tlsConfig.Certificates = []tls.Certificate{cert}
	if cfg.WUITlsRequestCert {
		tlsConfig.ClientAuth = tls.RequestClientCert
		if cfg.WUITlsRejectUnauthorized {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}
	if cfg.WUITlsCaPath != "" {
		caBytes, err := os.ReadFile(cfg.WUITlsCaPath)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("failed to parse WUI TLS CA file: %s", cfg.WUITlsCaPath)
		}
		tlsConfig.ClientCAs = pool
	}
	return tlsConfig, nil
}

func loadTLSCertificate(certPath, keyPath, passphrase string) (tls.Certificate, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	if passphrase != "" {
		decrypted, err := decryptPEMKey(keyPEM, []byte(passphrase))
		if err != nil {
			return tls.Certificate{}, err
		}
		keyPEM = decrypted
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

func decryptPEMKey(keyPEM, passphrase []byte) ([]byte, error) {
	var out []byte
	rest := keyPEM
	decryptedAny := false
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			out = append(out, rest...)
			break
		}
		if x509.IsEncryptedPEMBlock(block) {
			der, err := x509.DecryptPEMBlock(block, passphrase)
			if err != nil {
				return nil, err
			}
			block = &pem.Block{Type: block.Type, Bytes: der}
			decryptedAny = true
		}
		out = append(out, pem.EncodeToMemory(block)...)
		rest = next
	}
	if !decryptedAny {
		return keyPEM, nil
	}
	return out, nil
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
		w.Header().Set("Server", "Chinachu (Node)")
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
			w.Header().Set("WWW-Authenticate", `Basic realm="Authentication."`)
			legacyHTTPError(w, r, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) withMethodOverride(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		method := query.Get("method")
		if override := query.Get("_method"); override != "" {
			method = override
		}
		if method != "" {
			r = r.Clone(r.Context())
			r.Method = strings.ToUpper(method)
			query.Del("method")
			query.Del("_method")
			r.URL.RawQuery = query.Encode()
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
	if s.cfg.WUIXFF {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			remote = strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
	}
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
	if contentType := legacyStaticContentType(filePath); contentType != "" {
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

func legacyStaticContentType(path string) string {
	switch strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".") {
	case "html":
		return "text/html"
	case "js":
		return "text/javascript"
	case "css":
		return "text/css"
	case "ico", "cur":
		return "image/vnd.microsoft.icon"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "jpg":
		return "image/jpeg"
	case "f4v", "m4v", "mp4":
		return "video/mp4"
	case "flv":
		return "video/x-flv"
	case "webm":
		return "video/webm"
	case "m2ts":
		return "video/MP2T"
	case "asf":
		return "video/x-ms-asf"
	case "json":
		return "application/json; charset=utf-8"
	case "xspf":
		return "application/xspf+xml"
	default:
		return ""
	}
}

func (s *server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	apiType := apiExtension(path)
	path = trimLastExtension(path)
	parts := splitPath(path)
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
		s.handleJSONFile(w, r, s.paths.Reserves, "[]")
	case len(parts) >= 2 && parts[0] == "reserves":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleReserveProgram(w, r, parts[1:])
	case len(parts) == 1 && parts[0] == "recording":
		if !requireAPIType(w, r, apiType, "json") {
			return
		}
		s.handleJSONFile(w, r, s.paths.Recording, "[]")
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "preview":
		s.handleProgramPreview(w, r, s.paths.Recording, parts[1])
	case len(parts) == 3 && parts[0] == "recording" && parts[2] == "watch":
		s.handleProgramWatch(w, r, s.paths.Recording, parts[1], apiType, true)
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
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		s.handleRecordedFile(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "preview":
		s.handleProgramPreview(w, r, s.paths.Recorded, parts[1])
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "watch":
		s.handleProgramWatch(w, r, s.paths.Recorded, parts[1], apiType, false)
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
		s.handleChannelLogo(w, r, parts[1], apiType)
	case len(parts) == 3 && parts[0] == "channel" && parts[2] == "watch":
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

func (s *server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	info, err := os.Stat(s.paths.Schedule)
	if err != nil && !os.IsNotExist(err) {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	var schedule []chinachu.ChannelSchedule
	if err := storage.ReadJSON(s.paths.Schedule, &schedule, "[]"); err != nil {
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
	if _, err := os.Stat(s.paths.Config); err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
			return
		}
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		raw := r.URL.Query().Get("json")
		if raw == "" {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		var obj any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		if err := storage.WriteFileAtomic(s.paths.Config, []byte(raw)); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
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
		legacyHTTPError(w, r, http.StatusInternalServerError)
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
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	schedules, err := s.readSchedule()
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	programs := []chinachu.Program{}
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
	writePrettyJSON(w, http.StatusOK, broadcastingPrograms([]chinachu.ChannelSchedule{*channel}, time.Now()))
}

func (s *server) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
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
	recordedDir := s.cfg.RecordedDir
	if recordedDir == "" {
		recordedDir = "."
	}
	usage, err := system.GetDiskUsage(recordedDir)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	writePrettyJSON(w, http.StatusOK, map[string]any{
		"recorded": recordedSize,
		"size":     usage.Size,
		"used":     usage.Used,
		"avail":    usage.Avail,
	})
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
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	default:
		result, ok, err := s.schedulerResultFromLog(logPath)
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
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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
		rules = append(rules, rule)
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
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
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(s.paths.Rules, &rules, "[]"); err != nil {
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
		rules[index] = rule
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		rules = append(rules[:index], rules[index+1:]...)
		if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
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
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(s.paths.Rules, &rules, "[]"); err != nil {
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
	if err := storage.WriteJSONAtomic(s.paths.Rules, rules, true); err != nil {
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
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPut {
		kept := recorded[:0]
		removed := false
		for _, program := range recorded {
			if program.Recorded == "" {
				removed = true
				continue
			}
			if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err == nil {
				kept = append(kept, program)
			} else {
				removed = true
			}
		}
		recorded = kept
		if removed {
			if _, err := storage.BackupFile(s.paths.Recorded); err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
		}
		if err := storage.WriteJSONAtomic(s.paths.Recorded, recorded, false); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
	}
	if r.Method == http.MethodPut {
		writePrettyJSON(w, http.StatusOK, recorded)
		return
	}
	writeCompactJSON(w, http.StatusOK, recorded)
}

func (s *server) handleReserveProgram(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
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
		reserves = removeProgram(reserves, id)
		if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
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
		if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
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
	var recording []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recording, &recording, "[]"); err != nil {
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
			var reserves []chinachu.Program
			if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
			if reserveIndex := findProgram(reserves, id); reserveIndex != -1 {
				reserves[reserveIndex].IsSkip = true
				if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
					legacyHTTPError(w, r, http.StatusInternalServerError)
					return
				}
			}
		}
		recording[index].Abort = true
		if err := storage.WriteJSONAtomic(s.paths.Recording, recording, false); err != nil {
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
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
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
		if _, err := storage.BackupFile(s.paths.Recorded); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		recorded = removeProgram(recorded, id)
		if err := storage.WriteJSONAtomic(s.paths.Recorded, recorded, false); err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		writeCompactJSON(w, http.StatusOK, map[string]any{})
	default:
		w.Header().Set("Allow", "GET, HEAD, DELETE")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRecordedFile(w http.ResponseWriter, r *http.Request, id, apiType string) {
	var recorded []chinachu.Program
	if err := storage.ReadJSON(s.paths.Recorded, &recorded, "[]"); err != nil {
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
			if r.Header.Get("Range") != "" && staticRangeExceedsSize(r.Header.Get("Range"), info.Size()) {
				legacyHTTPError(w, r, http.StatusRequestedRangeNotSatisfiable)
				return
			}
			file, err := os.Open(path)
			if err != nil {
				legacyHTTPError(w, r, http.StatusInternalServerError)
				return
			}
			defer file.Close()
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.m2ts"`, id))
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
		case "json", "":
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

func (s *server) handleProgramWatch(w http.ResponseWriter, r *http.Request, path, id, apiType string, requirePID bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "HEAD, GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	var programs []chinachu.Program
	if err := storage.ReadJSON(path, &programs, "[]"); err != nil {
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
		file, err := os.Open(filePath)
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		defer file.Close()
		s.streamFFmpeg(w, r, file, "mp4", false)
	default:
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
	}
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

func setWatchDownloadHeader(w http.ResponseWriter, r *http.Request, filePath, apiType string) {
	if r.URL.Query().Get("mode") != "download" {
		return
	}
	ext := r.URL.Query().Get("ext")
	if ext == "" {
		ext = apiType
	}
	base := filepath.Base(filePath)
	if suffix := filepath.Ext(base); suffix != "" {
		base = strings.TrimSuffix(base, suffix)
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(base+"."+ext))
}

func (s *server) handleProgramPreview(w http.ResponseWriter, r *http.Request, path, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		legacyHTTPError(w, r, http.StatusMethodNotAllowed)
		return
	}
	apiType := apiExtension(r.URL.Path)
	if apiType != "png" && apiType != "jpg" && apiType != "txt" {
		legacyHTTPError(w, r, http.StatusUnsupportedMediaType)
		return
	}
	var programs []chinachu.Program
	if err := storage.ReadJSON(path, &programs, "[]"); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(programs, id)
	if index == -1 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	program := programs[index]
	if path == s.paths.Recording && !programHasPID(program) {
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
	if _, err := os.Stat(filePath); err != nil {
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
	pos := previewPosition(r)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	output, err := runFFmpegPreview(ctx,
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
	if err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "[previewer] %v", err)
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
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
	if program := chinachu.GetProgramByID(id, schedules, nil); program != nil {
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

func (s *server) reserveProgram(w http.ResponseWriter, r *http.Request, program chinachu.Program) {
	var reserves []chinachu.Program
	if err := storage.ReadJSON(s.paths.Reserves, &reserves, "[]"); err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	if findProgram(reserves, program.ID) != -1 {
		legacyHTTPError(w, r, http.StatusConflict)
		return
	}
	program.IsManualReserved = true
	program.OneSeg = r.URL.Query().Get("mode") == "1seg"
	reserves = append(reserves, program)
	if err := storage.WriteJSONAtomic(s.paths.Reserves, reserves, false); err != nil {
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
	client.UserAgent = mirakurun.LegacyUserAgent("wui")
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
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
		if err != nil {
			legacyHTTPError(w, r, http.StatusInternalServerError)
			return
		}
		client.UserAgent = mirakurun.LegacyUserAgent("wui")
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
		client.UserAgent = mirakurun.LegacyUserAgent("wui")
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
	args := watchFFmpegArgs(r, s.cfg, format, live)
	output, wait, err := runFFmpegStream(r.Context(), input, args...)
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
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, output)
	if err := wait(); err != nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "#ffmpeg: %v", err)
	}
}

func watchFFmpegArgs(r *http.Request, cfg *config.Config, format string, live bool) []string {
	q := r.URL.Query()
	videoCodec := q.Get("c:v")
	audioCodec := q.Get("c:a")
	container := q.Get("f")
	videoBitrate := q.Get("b:v")
	audioBitrate := q.Get("b:a")
	if format == "mp4" {
		container = "mp4"
		if videoCodec == "" {
			videoCodec = "h264"
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
		args = append(args, "-v", "0")
	}
	if cfg.VAAPIEnabled {
		device := cfg.VAAPIDevice
		if device == "" {
			device = "/dev/dri/renderD128"
		}
		args = append(args, "-vaapi_device", device, "-hwaccel", "vaapi", "-hwaccel_output_format", "yuv420p")
	}
	if live {
		args = append(args, "-re")
	}
	args = append(args, "-i", "pipe:0", "-threads", "0")
	if !live {
		args = append(args, "-ss", legacyWatchStart(q.Get("ss")))
	}
	if duration := q.Get("t"); duration != "" {
		args = append(args, "-t", duration)
	}
	if cfg.VAAPIEnabled {
		filter := "format=nv12|vaapi,hwupload,deinterlace_vaapi"
		if size := q.Get("s"); size != "" {
			if parts := strings.Split(size, "x"); len(parts) == 2 {
				filter += ",scale_vaapi=w=" + parts[0] + ":h=" + parts[1]
			}
		}
		args = append(args, "-vf", filter, "-aspect", "16:9")
	} else {
		args = append(args, "-filter:v", "yadif")
	}
	if cfg.VAAPIEnabled {
		if videoCodec == "mpeg2video" {
			videoCodec = "mpeg2_vaapi"
		}
		if videoCodec == "h264" {
			videoCodec = "h264_vaapi"
		}
	}
	if videoCodec != "" {
		args = append(args, "-c:v", videoCodec)
	}
	if audioCodec != "" {
		args = append(args, "-c:a", audioCodec)
	}
	if size := q.Get("s"); size != "" && !cfg.VAAPIEnabled {
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
	if videoCodec == "h264" {
		args = append(args, "-profile:v", "baseline", "-preset", "ultrafast", "-tune", "fastdecode,zerolatency")
	}
	if videoCodec == "h264_vaapi" {
		args = append(args, "-profile", "77", "-level", "41")
	}
	if container == "mp4" {
		args = append(args, "-movflags", "frag_keyframe+empty_moov+faststart+default_base_moof")
	}
	return append(args, "-y", "-f", container, "pipe:1")
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
			"previewer":         true,
			"streamer":          true,
			"filer":             true,
			"configurator":      true,
			"normalizationForm": s.cfg.NormalizationForm,
		},
		"system": map[string]any{"core": runtime.NumCPU()},
		"operator": map[string]any{
			"alive": pidAlive(operatorPID),
			"pid":   operatorPID,
		},
		"scheduler": map[string]any{
			"alive": pidAlive(schedulerPID),
			"pid":   schedulerPID,
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
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(w, "%d %s\n", status, legacyStatusText(status))
}

func legacyStatusText(status int) string {
	if status == http.StatusRequestURITooLong {
		return "Request-URI Too Long"
	}
	return http.StatusText(status)
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

func programHasPID(program chinachu.Program) bool {
	return program.PID > 0
}

func programIsScrambling(program chinachu.Program) bool {
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
	case len(parts) == 2 && parts[0] == "recorded":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "recorded" && parts[2] == "file":
		return []string{"GET", "DELETE"}, true
	case len(parts) == 3 && parts[0] == "recorded" && (parts[2] == "preview" || parts[2] == "watch"):
		return []string{"GET"}, true
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
	case "status", "scheduler", "storage", "log", "config", "rules", "schedule", "reserves", "recording", "recorded", "program", "channel":
		return true
	default:
		return false
	}
}

func apiExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash {
		ext := path[dot+1:]
		if isLegacyAPIExtension(ext) {
			return ext
		}
	}
	return ""
}

func trimLastExtension(path string) string {
	slash := strings.LastIndex(path, "/")
	dot := strings.LastIndex(path, ".")
	if dot > slash && isLegacyAPIExtension(path[dot+1:]) {
		return path[:dot]
	}
	return path
}

func isLegacyAPIExtension(ext string) bool {
	if ext == "" {
		return false
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
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
