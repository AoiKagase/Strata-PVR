package wui

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
)

const sessionCookieName = "strata_session"
const sessionDuration = 8 * time.Hour
// Playback tickets remain valid for the viewing session because media players
// issue fresh Range requests whenever the user seeks.
const playbackTicketDuration = sessionDuration

type authSession struct {
	username string
	expires  time.Time
}

type playbackTicket struct {
	path    string
	expires time.Time
}

type authIdentity struct {
	username string
	bearer   bool
}

func randomAuthValue(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *server) createSession(username string) (string, error) {
	id, err := randomAuthValue(32)
	if err != nil {
		return "", err
	}
	s.authMu.Lock()
	s.cleanupSessionsLocked(time.Now())
	s.sessions[id] = authSession{username: username, expires: time.Now().Add(sessionDuration)}
	s.authMu.Unlock()
	return id, nil
}

func (s *server) clearSession(id string) {
	if id == "" {
		return
	}
	s.authMu.Lock()
	delete(s.sessions, id)
	s.authMu.Unlock()
}

func (s *server) clearSessions() {
	s.authMu.Lock()
	s.sessions = make(map[string]authSession)
	s.authMu.Unlock()
}

func (s *server) createPlaybackTicket(path string) (string, error) {
	token, err := randomAuthValue(32)
	if err != nil {
		return "", err
	}
	s.authMu.Lock()
	s.cleanupPlaybackTicketsLocked(time.Now())
	s.playbackTickets[token] = playbackTicket{path: path, expires: time.Now().Add(playbackTicketDuration)}
	s.authMu.Unlock()
	return token, nil
}

func (s *server) playbackTicketIdentity(r *http.Request) (authIdentity, bool) {
	if !isPlaybackRequest(r.URL.Path) {
		return authIdentity{}, false
	}
	token := r.URL.Query().Get("playback")
	if token == "" {
		return authIdentity{}, false
	}
	s.authMu.Lock()
	s.cleanupPlaybackTicketsLocked(time.Now())
	ticket, ok := s.playbackTickets[token]
	s.authMu.Unlock()
	if !ok || ticket.path != r.URL.Path {
		return authIdentity{}, false
	}
	return authIdentity{bearer: true}, true
}

func (s *server) cleanupPlaybackTicketsLocked(now time.Time) {
	for token, ticket := range s.playbackTickets {
		if !now.Before(ticket.expires) {
			delete(s.playbackTickets, token)
		}
	}
}

func isPlaybackRequest(requestPath string) bool {
	parts := strings.Split(strings.Trim(requestPath, "/"), "/")
	return len(parts) == 4 && parts[0] == "api" && (parts[1] == "recorded" || parts[1] == "recording" || parts[1] == "channel") && parts[3] == "watch.m2ts"
}

func (s *server) cleanupSessionsLocked(now time.Time) {
	for id, session := range s.sessions {
		if !now.Before(session.expires) {
			delete(s.sessions, id)
		}
	}
}

func (s *server) sessionIdentity(r *http.Request) (authIdentity, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return authIdentity{}, false
	}
	s.authMu.Lock()
	session, ok := s.sessions[cookie.Value]
	if ok && !time.Now().Before(session.expires) {
		delete(s.sessions, cookie.Value)
		ok = false
	}
	s.authMu.Unlock()
	if !ok {
		return authIdentity{}, false
	}
	return authIdentity{username: session.username}, true
}

func (s *server) bearerIdentity(r *http.Request) (authIdentity, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return authIdentity{}, false
	}
	secret := parts[1]
	if secret == "" {
		return authIdentity{}, false
	}
	hash := sha256.Sum256([]byte(secret))
	want := hex.EncodeToString(hash[:])
	s.configMu.Lock()
	tokens := append([]config.APIToken(nil), s.cfg.WUIAPITokens...)
	s.configMu.Unlock()
	for _, token := range tokens {
		if subtle.ConstantTimeCompare([]byte(token.TokenHash), []byte(want)) == 1 {
			return authIdentity{username: token.Name, bearer: true}, true
		}
	}
	return authIdentity{}, false
}

func (s *server) authenticateRequest(r *http.Request) (authIdentity, bool) {
	if identity, ok := s.bearerIdentity(r); ok {
		return identity, true
	}
	return s.sessionIdentity(r)
}

func (s *server) verifyLogin(username, password string, r *http.Request) bool {
	if username == "" || password == "" {
		return false
	}
	s.configMu.Lock()
	accounts := append([]config.WebUser(nil), s.cfg.WUIAccounts...)
	s.configMu.Unlock()
	select {
	case s.authWorkers <- struct{}{}:
	case <-r.Context().Done():
		return false
	}
	defer func() { <-s.authWorkers }()
	for _, account := range accounts {
		if account.Username == username && passwordauth.VerifyPassword(account.PasswordHash, password) {
			return true
		}
	}
	return false
}

func requestUsesHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if requestUsesHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

func validSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Scheme+"://"+parsed.Host, requestOrigin(r))
}
