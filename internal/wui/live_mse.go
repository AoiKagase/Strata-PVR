package wui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"strata-pvr/internal/logging"
	"strata-pvr/internal/mirakurun"
)

const (
	liveMSEProbeLimit        = 2 * 1024 * 1024
	liveMSESubtitleWait      = 5 * time.Second
	liveMSESubtitleAccept    = 10 * time.Second
	liveMSESubtitleCueLimit  = 128
	liveMSESubtitleQueueSize = 32
)

var (
	errLiveMSESessionExists   = errors.New("live MSE session already exists")
	errLiveMSESessionNotFound = errors.New("live MSE session not found")
)

type liveMSESessionManager struct {
	mu       sync.Mutex
	sessions map[string]*liveMSESession
	starting map[string]*liveMSEStart
}

type liveMSEStart struct {
	cancel  context.CancelFunc
	done    chan struct{}
	stopped bool
}

type liveMSESession struct {
	token        string
	key          string
	ctx          context.Context
	cancel       context.CancelFunc
	media        io.ReadCloser
	source       io.Closer
	subtitles    *liveVTTHub
	subtitleErr  error
	listener     net.Listener
	connMu       sync.Mutex
	subtitleConn net.Conn
	done         chan struct{}
	finishOnce   sync.Once
	stopOnce     sync.Once
}

func newLiveMSESessionManager() *liveMSESessionManager {
	return &liveMSESessionManager{
		sessions: make(map[string]*liveMSESession),
		starting: make(map[string]*liveMSEStart),
	}
}

func (m *liveMSESessionManager) start(
	parent context.Context,
	token string,
	key string,
	start func(context.Context, context.CancelFunc) (*liveMSESession, error),
) (*liveMSESession, error) {
	if m == nil || !playbackSessionPattern.MatchString(token) {
		return nil, errLiveMSESessionNotFound
	}

	sessionCtx, cancel := context.WithCancel(parent)
	flight := &liveMSEStart{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	if m.sessions[token] != nil || m.starting[token] != nil {
		m.mu.Unlock()
		cancel()
		return nil, errLiveMSESessionExists
	}
	m.starting[token] = flight
	m.mu.Unlock()

	session, err := start(sessionCtx, cancel)
	m.mu.Lock()
	current := m.starting[token]
	canceled := current != flight || flight.stopped || sessionCtx.Err() != nil
	if current == flight {
		delete(m.starting, token)
	}
	if err == nil && !canceled {
		session.token = token
		session.key = key
		m.sessions[token] = session
	}
	close(flight.done)
	m.mu.Unlock()
	if canceled {
		cancel()
		if session != nil {
			session.stop()
		}
		if err == nil {
			err = context.Canceled
		}
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return session, nil
}

func (m *liveMSESessionManager) wait(ctx context.Context, token string) (*liveMSESession, error) {
	if m == nil || !playbackSessionPattern.MatchString(token) {
		return nil, errLiveMSESessionNotFound
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		session := m.sessions[token]
		flight := m.starting[token]
		m.mu.Unlock()
		if session != nil {
			return session, nil
		}
		if flight != nil {
			select {
			case <-flight.done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (m *liveMSESessionManager) stop(token string) {
	if m == nil || !playbackSessionPattern.MatchString(token) {
		return
	}
	m.mu.Lock()
	session := m.sessions[token]
	delete(m.sessions, token)
	flight := m.starting[token]
	if flight != nil {
		flight.stopped = true
	}
	m.mu.Unlock()
	if flight != nil {
		flight.cancel()
	}
	if session != nil {
		session.stop()
	}
}

func (m *liveMSESessionManager) stopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*liveMSESession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	flights := make([]*liveMSEStart, 0, len(m.starting))
	for _, flight := range m.starting {
		flights = append(flights, flight)
	}
	m.sessions = make(map[string]*liveMSESession)
	m.starting = make(map[string]*liveMSEStart)
	m.mu.Unlock()
	for _, flight := range flights {
		flight.cancel()
	}
	for _, session := range sessions {
		session.stop()
	}
}

func (s *liveMSESession) setSubtitleConn(conn net.Conn) {
	s.connMu.Lock()
	s.subtitleConn = conn
	s.connMu.Unlock()
}

func (s *liveMSESession) closeSubtitleConn() {
	s.connMu.Lock()
	conn := s.subtitleConn
	s.subtitleConn = nil
	s.connMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (s *liveMSESession) finish() {
	s.finishOnce.Do(func() {
		s.cancel()
		if s.source != nil {
			_ = s.source.Close()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.closeSubtitleConn()
		if s.subtitles != nil {
			s.subtitles.finish()
		}
		close(s.done)
	})
}

func (s *liveMSESession) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		if s.source != nil {
			_ = s.source.Close()
		}
		if s.media != nil {
			_ = s.media.Close()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.closeSubtitleConn()
	})
	select {
	case <-s.done:
	case <-time.After(playbackRequestStopTimeout):
	}
}

func liveMSESessionKey(channelID string, r *http.Request) string {
	q := r.URL.Query()
	parts := []string{channelID}
	for _, key := range []string{"s", "b:v", "b:a", "audio", "c:v", "c:a", "r", "ar"} {
		parts = append(parts, key+"="+q.Get(key))
	}
	return strings.Join(parts, "\x00")
}

func (s *server) handleLiveMSEVideo(w http.ResponseWriter, r *http.Request, channelID string, serviceID int64) {
	token := r.URL.Query().Get("session")
	if !playbackSessionPattern.MatchString(token) {
		legacyHTTPError(w, r, http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "video/MP2T")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}
	manager := s.liveMSE
	if manager == nil {
		manager = newLiveMSESessionManager()
		s.liveMSE = manager
	}
	key := liveMSESessionKey(channelID, r)
	session, err := manager.start(r.Context(), token, key, func(
		sessionCtx context.Context,
		cancel context.CancelFunc,
	) (*liveMSESession, error) {
		return s.startLiveMSESession(sessionCtx, cancel, r, serviceID)
	})
	if err != nil {
		if errors.Is(err, errLiveMSESessionExists) {
			legacyHTTPError(w, r, http.StatusConflict)
		} else {
			legacyHTTPError(w, r, http.StatusServiceUnavailable)
		}
		return
	}
	defer manager.stop(token)

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := copyFFmpegOutput(w, session.media); err != nil && r.Context().Err() == nil {
		_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "live MSE output: %v", err)
	}
}

func (s *server) handleLiveMSESubtitles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}
	manager := s.liveMSE
	if manager == nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	waitCtx, cancel := context.WithTimeout(r.Context(), liveMSESubtitleWait)
	defer cancel()
	session, err := manager.wait(waitCtx, r.URL.Query().Get("session"))
	if err != nil {
		legacyHTTPError(w, r, http.StatusNotFound)
		return
	}
	if session.subtitleErr != nil {
		legacyHTTPError(w, r, http.StatusServiceUnavailable)
		return
	}
	if session.subtitles == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	reader := session.subtitles.subscribe(r.Context())
	defer reader.Close()
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = copySharedLiveWebVTT(w, reader)
}

func (s *server) startLiveMSESession(
	sessionCtx context.Context,
	cancel context.CancelFunc,
	r *http.Request,
	serviceID int64,
) (*liveMSESession, error) {
	client, err := mirakurun.New(s.cfg.EffectiveMirakurunPath())
	if err != nil {
		return nil, err
	}
	client.UserAgent = mirakurun.StrataUserAgent("wui")
	body, err := client.ServiceStream(sessionCtx, serviceID, true)
	if err != nil {
		return nil, err
	}
	input, hasCaption, err := probeLiveMSEInput(body)
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	encoder, err := s.mp4VideoEncoder()
	if err != nil {
		_ = body.Close()
		return nil, err
	}

	decoder := ""
	var subtitleErr error
	if hasCaption {
		decoder, subtitleErr = s.aribCaptionDecoder()
	}
	var listener net.Listener
	var subtitles *liveVTTHub
	subtitleURL := ""
	if hasCaption && subtitleErr == nil {
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			_ = body.Close()
			return nil, err
		}
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			_ = tcpListener.SetDeadline(time.Now().Add(liveMSESubtitleAccept))
		}
		subtitles = newLiveVTTHub()
		subtitleURL = "tcp://" + listener.Addr().String()
	}

	args := liveMSEFFmpegArgs(r, encoder, decoder, subtitleURL)
	output, wait, err := runFFmpegStream(sessionCtx, input, args...)
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		_ = body.Close()
		return nil, err
	}
	_ = logging.AppendLine(filepath.Join(logDir(s.paths), "wui"), "SPAWN MSE: ffmpeg %s", strings.Join(args, " "))
	session := &liveMSESession{
		ctx:         sessionCtx,
		cancel:      cancel,
		media:       output,
		source:      body,
		subtitles:   subtitles,
		subtitleErr: subtitleErr,
		listener:    listener,
		done:        make(chan struct{}),
	}
	if listener != nil {
		go session.acceptSubtitles()
	}
	go func() {
		waitErr := wait()
		s.logFFmpegWaitError(sessionCtx, waitErr)
		session.finish()
	}()
	return session, nil
}

func (s *liveMSESession) acceptSubtitles() {
	conn, err := s.listener.Accept()
	_ = s.listener.Close()
	if err != nil {
		if s.subtitles != nil {
			s.subtitles.finish()
		}
		return
	}
	s.setSubtitleConn(conn)
	s.subtitles.consume(conn)
	s.closeSubtitleConn()
}

func liveMSEFFmpegArgs(r *http.Request, encoder, decoder, subtitleURL string) []string {
	args := watchFFmpegArgs(r, "m2ts", true, encoder)
	if decoder == "" || subtitleURL == "" {
		return args
	}
	// The low-latency video path intentionally uses a short probe.  That is
	// sufficient for video and audio, but ARIB data streams can be identified
	// only after their PMT and first caption PES have both arrived.  Use the
	// established subtitle probe window when this shared process emits WebVTT.
	args = liveMSESubtitleProbeArgs(args)
	insertAt := 0
	for index, arg := range args {
		if arg == "-fflags" {
			insertAt = index
			break
		}
	}
	withDecoder := make([]string, 0, len(args)+len(subtitleFFmpegDecoderArgs(decoder))+11)
	withDecoder = append(withDecoder, args[:insertAt]...)
	withDecoder = append(withDecoder, subtitleFFmpegDecoderArgs(decoder)...)
	withDecoder = append(withDecoder, args[insertAt:]...)
	return append(withDecoder,
		"-map", "0:s:0", "-vn", "-an", "-c:s", "webvtt",
		"-flush_packets", "1", "-f", "webvtt", subtitleURL,
	)
}

func liveMSESubtitleProbeArgs(args []string) []string {
	adjusted := append([]string(nil), args...)
	for index := 0; index+1 < len(adjusted); index += 2 {
		switch adjusted[index] {
		case "-analyzeduration", "-probesize":
			adjusted[index+1] = "10000000"
		}
	}
	return adjusted
}

type prefixedReadCloser struct {
	io.Reader
	io.Closer
}

func probeLiveMSEInput(source io.ReadCloser) (io.ReadCloser, bool, error) {
	var prefix bytes.Buffer
	probe := newMPEGTSSubtitleProbe()
	buffer := make([]byte, 32*1024)
	for prefix.Len() < liveMSEProbeLimit && !probe.complete {
		limit := len(buffer)
		if remaining := liveMSEProbeLimit - prefix.Len(); remaining < limit {
			limit = remaining
		}
		n, err := source.Read(buffer[:limit])
		if n > 0 {
			_, _ = prefix.Write(buffer[:n])
			probe.feed(buffer[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, false, err
			}
			break
		}
	}
	reader := io.MultiReader(bytes.NewReader(prefix.Bytes()), source)
	return &prefixedReadCloser{Reader: reader, Closer: source}, probe.hasCaption, nil
}

type mpegTSSubtitleProbe struct {
	pending      []byte
	pat          psiAssembler
	pmts         map[uint16]*psiAssembler
	seenPMTs     map[uint16]bool
	hasCaption   bool
	complete     bool
	synchronized bool
}

func newMPEGTSSubtitleProbe() *mpegTSSubtitleProbe {
	return &mpegTSSubtitleProbe{
		pmts:     make(map[uint16]*psiAssembler),
		seenPMTs: make(map[uint16]bool),
	}
}

func (p *mpegTSSubtitleProbe) feed(data []byte) {
	p.pending = append(p.pending, data...)
	for {
		if !p.synchronized {
			offset := mpegTSSyncOffset(p.pending)
			if offset < 0 {
				if len(p.pending) > 376 {
					p.pending = append([]byte(nil), p.pending[len(p.pending)-376:]...)
				}
				return
			}
			p.pending = p.pending[offset:]
			p.synchronized = true
		}
		if len(p.pending) < 188 {
			return
		}
		if p.pending[0] != 0x47 {
			p.synchronized = false
			p.pending = p.pending[1:]
			continue
		}
		packet := p.pending[:188]
		p.pending = p.pending[188:]
		p.feedPacket(packet)
		if p.complete {
			return
		}
	}
}

func mpegTSSyncOffset(data []byte) int {
	for offset := 0; offset < len(data); offset++ {
		if data[offset] != 0x47 {
			continue
		}
		if offset+188 >= len(data) || data[offset+188] == 0x47 {
			return offset
		}
	}
	return -1
}

func (p *mpegTSSubtitleProbe) feedPacket(packet []byte) {
	if len(packet) != 188 || packet[0] != 0x47 || packet[1]&0x80 != 0 {
		return
	}
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	payloadStart := packet[1]&0x40 != 0
	adaptationControl := (packet[3] >> 4) & 0x03
	if adaptationControl == 0 || adaptationControl == 2 {
		return
	}
	offset := 4
	if adaptationControl == 3 {
		if offset >= len(packet) {
			return
		}
		offset += 1 + int(packet[offset])
	}
	if offset >= len(packet) {
		return
	}
	payload := packet[offset:]
	if pid == 0 {
		for _, section := range p.pat.feed(payload, payloadStart) {
			for _, pmtPID := range parsePATPMTPIDs(section) {
				if p.pmts[pmtPID] == nil {
					p.pmts[pmtPID] = &psiAssembler{}
				}
			}
		}
		return
	}
	assembler := p.pmts[pid]
	if assembler == nil {
		return
	}
	for _, section := range assembler.feed(payload, payloadStart) {
		if hasARIBCaptionStream(section) {
			p.hasCaption = true
		}
		p.seenPMTs[pid] = true
		p.complete = p.hasCaption || (len(p.pmts) > 0 && len(p.seenPMTs) == len(p.pmts))
	}
}

type psiAssembler struct {
	buffer []byte
}

func (a *psiAssembler) feed(payload []byte, payloadStart bool) [][]byte {
	var sections [][]byte
	if payloadStart {
		if len(payload) == 0 {
			return nil
		}
		pointer := int(payload[0])
		payload = payload[1:]
		if pointer > len(payload) {
			a.buffer = nil
			return nil
		}
		if len(a.buffer) > 0 && pointer > 0 {
			a.buffer = append(a.buffer, payload[:pointer]...)
			sections, a.buffer = extractPSISections(a.buffer, sections)
		}
		a.buffer = nil
		payload = payload[pointer:]
	}
	a.buffer = append(a.buffer, payload...)
	sections, a.buffer = extractPSISections(a.buffer, sections)
	return sections
}

func extractPSISections(buffer []byte, sections [][]byte) ([][]byte, []byte) {
	for {
		for len(buffer) > 0 && buffer[0] == 0xff {
			buffer = buffer[1:]
		}
		if len(buffer) < 3 {
			return sections, buffer
		}
		length := 3 + (int(buffer[1]&0x0f) << 8) + int(buffer[2])
		if length < 3 || length > 4096 {
			return sections, nil
		}
		if len(buffer) < length {
			return sections, buffer
		}
		sections = append(sections, append([]byte(nil), buffer[:length]...))
		buffer = buffer[length:]
	}
}

func parsePATPMTPIDs(section []byte) []uint16 {
	if len(section) < 12 || section[0] != 0x00 {
		return nil
	}
	end := len(section) - 4
	var result []uint16
	for offset := 8; offset+4 <= end; offset += 4 {
		program := uint16(section[offset])<<8 | uint16(section[offset+1])
		if program == 0 {
			continue
		}
		pid := uint16(section[offset+2]&0x1f)<<8 | uint16(section[offset+3])
		result = append(result, pid)
	}
	return result
}

func hasARIBCaptionStream(section []byte) bool {
	if len(section) < 16 || section[0] != 0x02 {
		return false
	}
	end := len(section) - 4
	programInfoLength := (int(section[10]&0x0f) << 8) | int(section[11])
	for offset := 12 + programInfoLength; offset+5 <= end; {
		streamType := section[offset]
		infoLength := (int(section[offset+3]&0x0f) << 8) | int(section[offset+4])
		infoEnd := offset + 5 + infoLength
		if infoEnd > end {
			return false
		}
		if streamType == 0x06 && hasARIBCaptionDescriptor(section[offset+5:infoEnd]) {
			return true
		}
		offset = infoEnd
	}
	return false
}

func hasARIBCaptionDescriptor(descriptors []byte) bool {
	for offset := 0; offset+2 <= len(descriptors); {
		tag := descriptors[offset]
		length := int(descriptors[offset+1])
		end := offset + 2 + length
		if end > len(descriptors) {
			return false
		}
		payload := descriptors[offset+2 : end]
		if tag == 0x52 && len(payload) >= 1 {
			componentTag := payload[0]
			if componentTag >= 0x30 && componentTag <= 0x37 || componentTag == 0x87 {
				return true
			}
		}
		if tag == 0xfd && len(payload) >= 2 {
			dataComponentID := uint16(payload[0])<<8 | uint16(payload[1])
			if dataComponentID == 0x0008 {
				return true
			}
		}
		offset = end
	}
	return false
}

type liveVTTHub struct {
	mu          sync.Mutex
	cues        [][]byte
	subscribers map[*liveVTTReader]struct{}
	done        bool
}

type liveVTTReader struct {
	ctx     context.Context
	hub     *liveVTTHub
	chunks  chan []byte
	current []byte
	once    sync.Once
}

func newLiveVTTHub() *liveVTTHub {
	return &liveVTTHub{subscribers: make(map[*liveVTTReader]struct{})}
}

func (h *liveVTTHub) consume(source io.Reader) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var lines []string
	emit := func() {
		if len(lines) == 0 {
			return
		}
		block := strings.Join(lines, "\n")
		lines = nil
		if !strings.Contains(block, "-->") {
			return
		}
		h.broadcast([]byte(block + "\n\n"))
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			emit()
			continue
		}
		lines = append(lines, line)
	}
	emit()
	h.finish()
}

func (h *liveVTTHub) subscribe(ctx context.Context) *liveVTTReader {
	h.mu.Lock()
	defer h.mu.Unlock()
	reader := &liveVTTReader{
		ctx: ctx, hub: h, chunks: make(chan []byte, liveMSESubtitleQueueSize),
	}
	replay := h.cues
	if len(replay) > liveMSESubtitleQueueSize {
		replay = replay[len(replay)-liveMSESubtitleQueueSize:]
	}
	for _, cue := range replay {
		reader.chunks <- append([]byte(nil), cue...)
	}
	if h.done {
		close(reader.chunks)
		return reader
	}
	h.subscribers[reader] = struct{}{}
	return reader
}

func (h *liveVTTHub) broadcast(cue []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cues = append(h.cues, append([]byte(nil), cue...))
	if len(h.cues) > liveMSESubtitleCueLimit {
		copy(h.cues, h.cues[len(h.cues)-liveMSESubtitleCueLimit:])
		h.cues = h.cues[:liveMSESubtitleCueLimit]
	}
	for subscriber := range h.subscribers {
		chunk := append([]byte(nil), cue...)
		select {
		case subscriber.chunks <- chunk:
		default:
			select {
			case <-subscriber.chunks:
			default:
			}
			select {
			case subscriber.chunks <- chunk:
			default:
			}
		}
	}
}

func (h *liveVTTHub) finish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	h.done = true
	for subscriber := range h.subscribers {
		close(subscriber.chunks)
	}
	h.subscribers = nil
}

func (h *liveVTTHub) remove(reader *liveVTTReader) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		return
	}
	delete(h.subscribers, reader)
}

func (r *liveVTTReader) Read(p []byte) (int, error) {
	for len(r.current) == 0 {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case chunk, ok := <-r.chunks:
			if !ok {
				return 0, io.EOF
			}
			r.current = chunk
		}
	}
	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

func (r *liveVTTReader) Close() error {
	r.once.Do(func() {
		if r.hub != nil {
			r.hub.remove(r)
		}
	})
	return nil
}
