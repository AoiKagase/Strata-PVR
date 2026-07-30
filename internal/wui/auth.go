package wui

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
)

const sessionCookieName = "strata_session"
const sessionDuration = 8 * time.Hour
const loginAttemptWindow = time.Minute
const maxLoginAttempts = 5

type loginAttempt struct {
	count int
	since time.Time
}

// Playback tickets remain valid for the viewing session because media players
// issue fresh Range requests whenever the user seeks.
const playbackTicketDuration = sessionDuration
const maxPlaybackTickets = 128

type authSession struct {
	username string
	expires  time.Time
}

type playbackTicket struct {
	path    string
	query   string
	expires time.Time
}

type authIdentity struct {
	username  string
	principal string
	bearer    bool
	scope     string
}

type authSubjectContextKey struct{}

func (identity authIdentity) subject() string {
	if identity.principal != "" {
		return identity.principal
	}
	if identity.username != "" {
		return "user:" + identity.username
	}
	return "authenticated"
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

func (s *server) clearPlaybackTickets() {
	s.authMu.Lock()
	s.playbackTickets = make(map[string]playbackTicket)
	s.authMu.Unlock()
}

func (s *server) createPlaybackTicket(path string, queries ...url.Values) (string, error) {
	token, err := randomAuthValue(32)
	if err != nil {
		return "", err
	}
	s.authMu.Lock()
	s.cleanupPlaybackTicketsLocked(time.Now())
	if len(s.playbackTickets) >= maxPlaybackTickets {
		s.authMu.Unlock()
		return "", errors.New("playback ticket capacity reached")
	}
	query := ""
	if len(queries) > 0 {
		query = playbackTicketQuery(queries[0])
	}
	s.playbackTickets[token] = playbackTicket{path: path, query: query, expires: time.Now().Add(playbackTicketDuration)}
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
	if !ok || ticket.path != r.URL.Path || ticket.query != playbackTicketQuery(r.URL.Query()) {
		return authIdentity{}, false
	}
	return authIdentity{principal: "ticket:" + token, bearer: true, scope: "playback"}, true
}

func playbackTicketQuery(values url.Values) string {
	query := make(url.Values, len(values))
	for key, value := range values {
		if key != "playback" && key != "prefix" && key != "ext" {
			query[key] = append([]string(nil), value...)
		}
	}
	return query.Encode()
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
			if token.ExpiresAt != "" {
				expires, err := time.Parse(time.RFC3339, token.ExpiresAt)
				if err != nil || !time.Now().Before(expires) {
					continue
				}
			}
			scope := token.Scope
			if scope == "" {
				scope = "admin"
			}
			return authIdentity{username: token.Name, principal: "token:" + token.ID, bearer: true, scope: scope}, true
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

func (s *server) allowLoginAttempt(r *http.Request, username string) bool {
	key := s.remoteAddress(r) + "\x00" + username
	now := time.Now()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.loginAttempts == nil {
		s.loginAttempts = make(map[string]loginAttempt)
	}
	attempt := s.loginAttempts[key]
	if now.Sub(attempt.since) >= loginAttemptWindow {
		attempt = loginAttempt{since: now}
	}
	if attempt.count >= maxLoginAttempts {
		return false
	}
	attempt.count++
	s.loginAttempts[key] = attempt
	return true
}

func (s *server) clearLoginAttempts(r *http.Request, username string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	delete(s.loginAttempts, s.remoteAddress(r)+"\x00"+username)
}

func (s *server) requestUsesHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.cfg == nil || !s.cfg.WUITrustForwardedHeaders || !s.requestFromTrustedProxy(r) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (s *server) requestOrigin(r *http.Request) string {
	scheme := "http"
	if s.requestUsesHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" && s.cfg != nil && s.cfg.WUITrustForwardedHeaders && s.requestFromTrustedProxy(r) {
		host = forwarded
	}
	return scheme + "://" + host
}

func (s *server) requestFromTrustedProxy(r *http.Request) bool {
	if s.cfg == nil {
		return false
	}
	ip := net.ParseIP(s.remoteAddress(r))
	if ip == nil {
		return false
	}
	for _, value := range s.cfg.WUITrustedProxies {
		if candidate := net.ParseIP(value); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(value); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *server) validSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Scheme+"://"+parsed.Host, s.requestOrigin(r))
}
