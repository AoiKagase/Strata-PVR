package wui

import (
	"context"
	"regexp"
	"sync"
)

var playbackSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

type playbackRequestSessions struct {
	mu       sync.Mutex
	nextID   uint64
	sessions map[string]map[uint64]context.CancelFunc
}

func newPlaybackRequestSessions() *playbackRequestSessions {
	return &playbackRequestSessions{sessions: make(map[string]map[uint64]context.CancelFunc)}
}

func (m *playbackRequestSessions) register(parent context.Context, token string) (context.Context, func()) {
	if m == nil || !playbackSessionPattern.MatchString(token) {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	requests := m.sessions[token]
	if requests == nil {
		requests = make(map[uint64]context.CancelFunc)
		m.sessions[token] = requests
	}
	requests[id] = cancel
	m.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			if requests := m.sessions[token]; requests != nil {
				delete(requests, id)
				if len(requests) == 0 {
					delete(m.sessions, token)
				}
			}
			m.mu.Unlock()
			cancel()
		})
	}
	return ctx, release
}

func (m *playbackRequestSessions) stop(token string) int {
	if m == nil || !playbackSessionPattern.MatchString(token) {
		return 0
	}
	m.mu.Lock()
	requests := m.sessions[token]
	delete(m.sessions, token)
	m.mu.Unlock()
	for _, cancel := range requests {
		cancel()
	}
	return len(requests)
}
