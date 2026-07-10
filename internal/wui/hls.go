package wui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"strata-pvr/internal/logging"
	"strata-pvr/internal/programstore"
)

type hlsPreset struct {
	width, height int
	video, audio  string
}

var hlsPresets = map[string]hlsPreset{
	"1080p": {1920, 1080, "2600k", "96k"},
	"720p":  {1280, 720, "1400k", "96k"},
	"540p":  {960, 540, "900k", "64k"},
	"360p":  {640, 360, "550k", "64k"},
}

type hlsSession struct {
	id, dir    string
	lastAccess time.Time
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
	timer      *time.Timer
}

type hlsSessionManager struct {
	mu       sync.Mutex
	root     string
	paths    Paths
	sessions map[string]*hlsSession
}

const hlsSessionIdleTimeout = 15 * time.Second

func newHLSSessionManager(paths Paths) *hlsSessionManager {
	return &hlsSessionManager{paths: paths, sessions: make(map[string]*hlsSession)}
}

func (s *server) handleRecordedHLS(w http.ResponseWriter, r *http.Request, id, resource, apiType string) {
	if s.hls == nil {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	programs, err := s.readPrograms(r.Context(), programstore.Recorded)
	if err != nil {
		legacyHTTPError(w, r, http.StatusInternalServerError)
		return
	}
	index := findProgram(programs, id)
	if index < 0 {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	filePath := filepath.FromSlash(programs[index].Recorded)
	if apiType == "m3u8" && resource == "index" && r.Method == http.MethodDelete {
		quality, _, start, duration, audio, ok := hlsRequestOptions(r)
		if !ok {
			legacyHTTPError(w, r, http.StatusBadRequest)
			return
		}
		s.hls.stop(filePath, quality, start, duration, audio)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			legacyHTTPError(w, r, http.StatusGone)
		} else {
			legacyHTTPError(w, r, http.StatusInternalServerError)
		}
		return
	}
	if apiType == "m3u8" && resource == "index" {
		s.serveHLSPlaylist(w, r, filePath)
		return
	}
	if apiType == "ts" && validHLSSegmentName(resource+".ts") {
		s.serveHLSSegment(w, r, resource+".ts")
		return
	}
	legacyHTTPError(w, r, http.StatusNotFound)
}

func (s *server) serveHLSPlaylist(w http.ResponseWriter, r *http.Request, filePath string) {
	quality, preset, start, duration, audio, ok := hlsRequestOptions(r)
	if !ok {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	session, err := s.hls.getOrStart(filePath, quality, preset, start, duration, audio)
	if err != nil {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	playlist := filepath.Join(session.dir, "index.m3u8")
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		data, readErr := os.ReadFile(playlist)
		if readErr == nil {
			text := addHLSSessionQuery(string(data), session.id)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(text))
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-session.done:
			if session.err != nil {
				legacyHTTPError(w, r, http.StatusServiceUnavailable)
				return
			}
		case <-deadline.C:
			legacyHTTPError(w, r, http.StatusGatewayTimeout)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *server) serveHLSSegment(w http.ResponseWriter, r *http.Request, name string) {
	session := s.hls.lookup(r.URL.Query().Get("session"))
	if session == nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	filePath := filepath.Join(session.dir, name)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if info, err := os.Stat(filePath); err == nil {
			w.Header().Set("Content-Type", "video/MP2T")
			w.Header().Set("Cache-Control", "private, max-age=300")
			http.ServeFile(w, r, filePath)
			_ = info
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			legacyHTTPError(w, r, http.StatusNotFound)
			return
		case <-time.After(75 * time.Millisecond):
		}
	}
}

func (m *hlsSessionManager) getOrStart(filePath, quality string, preset hlsPreset, start, duration int, audio string) (*hlsSession, error) {
	encoder, err := detectedH264Encoder()
	if err != nil {
		return nil, err
	}
	id := hlsSessionID(filePath, quality, start, duration, audio)
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[id]; existing != nil {
		existing.lastAccess = time.Now()
		existing.timer.Reset(hlsSessionIdleTimeout)
		return existing, nil
	}
	if m.root == "" {
		root, err := os.MkdirTemp("", "strata-pvr-hls-")
		if err != nil {
			return nil, err
		}
		m.root = root
	}
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &hlsSession{id: id, dir: dir, lastAccess: time.Now(), cancel: cancel, done: make(chan struct{})}
	args := hlsFFmpegArgs(filePath, dir, preset, start, duration, audio, encoder)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	logFile, err := os.Create(filepath.Join(dir, "ffmpeg.log"))
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		cancel()
		return nil, err
	}
	m.sessions[id] = session
	session.timer = time.AfterFunc(hlsSessionIdleTimeout, func() { m.expire(id) })
	_ = logging.AppendLine(filepath.Join(logDir(m.paths), "wui"), "SPAWN HLS: ffmpeg %s", strings.Join(args, " "))
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		m.mu.Lock()
		session.err = err
		close(session.done)
		m.mu.Unlock()
	}()
	return session, nil
}

func (m *hlsSessionManager) lookup(id string) *hlsSession {
	if len(id) != 24 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s != nil {
		s.lastAccess = time.Now()
		s.timer.Reset(hlsSessionIdleTimeout)
	}
	return s
}

func (m *hlsSessionManager) expire(id string) {
	m.mu.Lock()
	session := m.sessions[id]
	if session == nil {
		m.mu.Unlock()
		return
	}
	if idle := time.Since(session.lastAccess); idle < hlsSessionIdleTimeout {
		session.timer.Reset(hlsSessionIdleTimeout - idle)
		m.mu.Unlock()
		return
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	session.cancel()
	_ = os.RemoveAll(session.dir)
}

func (m *hlsSessionManager) stop(filePath, quality string, start, duration int, audio string) {
	m.expireNow(hlsSessionID(filePath, quality, start, duration, audio))
}

func (m *hlsSessionManager) expireNow(id string) {
	m.mu.Lock()
	session := m.sessions[id]
	if session != nil {
		delete(m.sessions, id)
		if session.timer != nil {
			session.timer.Stop()
		}
	}
	m.mu.Unlock()
	if session == nil {
		return
	}
	session.cancel()
	_ = os.RemoveAll(session.dir)
}

func hlsSessionID(filePath, quality string, start, duration int, audio string) string {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", filePath, quality, start, duration, audio)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:12])
}

func hlsRequestOptions(r *http.Request) (string, hlsPreset, int, int, string, bool) {
	quality := r.URL.Query().Get("quality")
	if quality == "" {
		quality = "540p"
	}
	preset, ok := hlsPresets[quality]
	audio := r.URL.Query().Get("audio")
	if !ok || (audio != "" && audio != "secondary") {
		return "", hlsPreset{}, 0, 0, "", false
	}
	return quality, preset, nonNegativeInt(r.URL.Query().Get("ss")), nonNegativeInt(r.URL.Query().Get("t")), audio, true
}

func hlsFFmpegArgs(input, dir string, p hlsPreset, start, duration int, audio, encoder string) []string {
	args := []string{"-v", "error", "-re", "-fflags", "+genpts+discardcorrupt", "-err_detect", "ignore_err"}
	if start > 0 {
		args = append(args, "-ss", strconv.Itoa(start))
	}
	args = append(args, "-f", "mpegts", "-i", input)
	if duration > 0 {
		args = append(args, "-t", strconv.Itoa(duration))
	}
	audioMap := "0:a:0?"
	if audio == "secondary" {
		audioMap = "0:a:1?"
	}
	filter := fmt.Sprintf("yadif,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", p.width, p.height, p.width, p.height)
	args = append(args, "-map", "0:v:0", "-map", audioMap, "-sn", "-dn", "-filter:v", filter, "-c:v", encoder)
	args = appendH264CompatibilityArgs(args, encoder)
	args = append(args, "-r", "24", "-g", "48", "-keyint_min", "48",
		"-b:v", p.video, "-maxrate:v", p.video, "-bufsize:v", bitrateTimes(p.video, 2),
		"-c:a", "aac", "-ac", "2", "-ar", "48000", "-b:a", p.audio,
		"-hls_time", "4", "-hls_playlist_type", "event", "-hls_flags", "independent_segments+temp_file",
		"-hls_segment_filename", filepath.Join(dir, "segment%05d.ts"), "-y", "-f", "hls", filepath.Join(dir, "index.m3u8"))
	return args
}

func bitrateTimes(value string, factor int) string {
	if len(value) < 2 {
		return value
	}
	n, err := strconv.Atoi(value[:len(value)-1])
	if err != nil {
		return value
	}
	return strconv.Itoa(n*factor) + value[len(value)-1:]
}

func nonNegativeInt(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func validHLSSegmentName(name string) bool {
	if !strings.HasPrefix(name, "segment") || !strings.HasSuffix(name, ".ts") {
		return false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, "segment"), ".ts")
	if digits == "" {
		return false
	}
	for _, ch := range digits {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func addHLSSessionQuery(playlist, id string) string {
	lines := strings.Split(playlist, "\n")
	for i, line := range lines {
		if validHLSSegmentName(line) {
			lines[i] = line + "?session=" + id
		}
	}
	return strings.Join(lines, "\n")
}
