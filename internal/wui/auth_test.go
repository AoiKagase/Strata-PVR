package wui

import (
	"net/http/httptest"
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
	if got, want := server.requestOrigin(request), "https://public.example"; got != want {
		t.Fatalf("forwarded origin = %q, want %q", got, want)
	}
}
