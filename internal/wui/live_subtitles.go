package wui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	liveSubtitleIdleTimeout    = 3 * time.Second
	liveSubtitleQueueSize      = 32
	maxLiveSubtitleSubscribers = 16
)

type liveSubtitleSource struct {
	output io.ReadCloser
	wait   func() error
	close  func() error
}

type liveSubtitleManager struct {
	mu       sync.Mutex
	sessions map[string]*liveSubtitleSession
}

type liveSubtitleSession struct {
	manager     *liveSubtitleManager
	key         string
	ctx         context.Context
	cancel      context.CancelFunc
	source      liveSubtitleSource
	subscribers map[*liveSubtitleReader]struct{}
	idleTimer   *time.Timer
	done        chan struct{}
	err         error
}

type liveSubtitleReader struct {
	ctx     context.Context
	session *liveSubtitleSession
	chunks  chan []byte
	current []byte
	once    sync.Once
}

func newLiveSubtitleManager() *liveSubtitleManager {
	return &liveSubtitleManager{sessions: make(map[string]*liveSubtitleSession)}
}

func (m *liveSubtitleManager) subscribe(
	ctx context.Context,
	key string,
	start func(context.Context) (liveSubtitleSource, error),
) (io.ReadCloser, error) {
	m.mu.Lock()
	if session := m.sessions[key]; session != nil {
		if len(session.subscribers) >= maxLiveSubtitleSubscribers {
			m.mu.Unlock()
			return nil, errors.New("live subtitle subscriber capacity reached")
		}
		reader := session.addSubscriberLocked(ctx)
		m.mu.Unlock()
		return reader, nil
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	source, err := start(sessionCtx)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return nil, err
	}
	session := &liveSubtitleSession{
		manager: m, key: key, ctx: sessionCtx, cancel: cancel, source: source,
		subscribers: make(map[*liveSubtitleReader]struct{}), done: make(chan struct{}),
	}
	m.sessions[key] = session
	reader := session.addSubscriberLocked(ctx)
	m.mu.Unlock()
	go session.run()
	return reader, nil
}

func (s *liveSubtitleSession) addSubscriberLocked(ctx context.Context) *liveSubtitleReader {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	reader := &liveSubtitleReader{
		ctx: ctx, session: s, chunks: make(chan []byte, liveSubtitleQueueSize),
	}
	s.subscribers[reader] = struct{}{}
	return reader
}

func (s *liveSubtitleSession) run() {
	defer s.finish()
	reader := bufio.NewReader(s.source.output)
	first, err := reader.ReadString('\n')
	if err != nil {
		s.err = err
		return
	}
	if strings.TrimSpace(first) == "WEBVTT" {
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				s.err = readErr
				return
			}
			if strings.TrimSpace(line) == "" {
				break
			}
		}
	} else {
		s.broadcast([]byte(first))
	}

	buffer := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			s.broadcast(buffer[:n])
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && s.ctx.Err() == nil {
				s.err = readErr
			}
			return
		}
	}
}

func (s *liveSubtitleSession) broadcast(data []byte) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	for subscriber := range s.subscribers {
		chunk := append([]byte(nil), data...)
		select {
		case subscriber.chunks <- chunk:
		default:
			// A slow subtitle client must not stall Mirakurun or FFmpeg.
			// Dropping a late cue is preferable to backing up the live stream.
		}
	}
}

func (s *liveSubtitleSession) finish() {
	_ = s.source.output.Close()
	if s.source.close != nil {
		_ = s.source.close()
	}
	if s.source.wait != nil {
		if err := s.source.wait(); err != nil && s.ctx.Err() == nil && s.err == nil {
			s.err = err
		}
	}
	s.cancel()

	s.manager.mu.Lock()
	if s.manager.sessions[s.key] == s {
		delete(s.manager.sessions, s.key)
	}
	for subscriber := range s.subscribers {
		close(subscriber.chunks)
	}
	s.subscribers = nil
	close(s.done)
	s.manager.mu.Unlock()
}

func (s *liveSubtitleSession) removeSubscriber(reader *liveSubtitleReader) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if _, ok := s.subscribers[reader]; !ok {
		return
	}
	delete(s.subscribers, reader)
	if len(s.subscribers) == 0 && s.idleTimer == nil {
		s.idleTimer = time.AfterFunc(liveSubtitleIdleTimeout, func() {
			s.manager.mu.Lock()
			if len(s.subscribers) == 0 && s.manager.sessions[s.key] == s {
				s.cancel()
				if s.source.close != nil {
					_ = s.source.close()
				}
			}
			s.manager.mu.Unlock()
		})
	}
}

func (r *liveSubtitleReader) Read(p []byte) (int, error) {
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

func (r *liveSubtitleReader) Close() error {
	r.once.Do(func() {
		if r.session != nil {
			r.session.removeSubscriber(r)
		}
	})
	return nil
}
