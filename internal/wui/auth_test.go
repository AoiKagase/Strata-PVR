package wui

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"strata-pvr/internal/config"
)

func TestRequestOriginTrustsForwardedHeadersOnlyWhenEnabled(t *testing.T) {
	request := httptest.NewRequest("GET", "http://local.example/", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "public.example")

	server := &server{cfg: &config.Config{}}
	if got, want := server.requestOrigin(request), "http://local.example"; got != want {
		t.Fatalf("default origin = %q, want %q", got, want)
	}

	server.cfg.WUITrustForwardedHeaders = true
	server.cfg.WUITrustedProxies = []string{"127.0.0.1"}
	request.RemoteAddr = "127.0.0.1:12345"
	if got, want := server.requestOrigin(request), "https://public.example"; got != want {
		t.Fatalf("forwarded origin = %q, want %q", got, want)
	}
}

func TestPlaybackTicketBindsMediaQueryButAllowsRangeRequests(t *testing.T) {
	s := &server{playbackTickets: make(map[string]playbackTicket)}
	ticket, err := s.createPlaybackTicket("/api/recorded/abc/watch.m2ts", url.Values{"t": {"30"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/recorded/abc/watch.m2ts?t=30&playback="+ticket, nil)
	request.Header.Set("Range", "bytes=5-8")
	if _, ok := s.playbackTicketIdentity(request); !ok {
		t.Fatal("ticket did not authorize its issued query with a Range request")
	}
	changed := httptest.NewRequest("GET", "/api/recorded/abc/watch.m2ts?t=60&playback="+ticket, nil)
	if _, ok := s.playbackTicketIdentity(changed); ok {
		t.Fatal("ticket authorized a changed media query")
	}
}
