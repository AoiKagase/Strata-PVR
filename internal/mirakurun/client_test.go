package mirakurun

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != LegacyUserAgent("go") {
			t.Fatalf("user-agent = %q", got)
		}
		fmt.Fprint(w, `[{"id":1,"serviceId":101,"networkId":1,"name":"svc","channel":{"type":"GR","channel":"27"}}]`)
	}))
	defer srv.Close()
	client, err := New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	var services []Service
	if err := client.GetJSON(context.Background(), "/api/services", &services); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Name != "svc" {
		t.Fatalf("unexpected services: %#v", services)
	}
}

func TestStreamEndpoints(t *testing.T) {
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		fmt.Fprint(w, "stream")
	}))
	defer srv.Close()
	client, err := New(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func(context.Context) (io.ReadCloser, error){
		func(ctx context.Context) (io.ReadCloser, error) { return client.ProgramStream(ctx, 1, true) },
		func(ctx context.Context) (io.ReadCloser, error) { return client.ServiceStream(ctx, 2, true) },
		func(ctx context.Context) (io.ReadCloser, error) { return client.LogoImage(ctx, 3) },
	} {
		rc, err := call(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		rc.Close()
	}
	want := []string{"/api/programs/1/stream?decode=1", "/api/services/2/stream?decode=1", "/api/services/3/logo"}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestLegacyUserAgentAndPriorityHeaders(t *testing.T) {
	client, err := New("http://127.0.0.1/")
	if err != nil {
		t.Fatal(err)
	}
	client.UserAgent = LegacyUserAgent("operator")
	client.SetPriority(2)
	req, err := client.newRequest(context.Background(), http.MethodGet, "/api/programs/1/stream?decode=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != "Chinachu/0.10.7-gamma.1 (operator)" {
		t.Fatalf("user-agent = %q", got)
	}
	if got := req.Header.Get("X-Mirakurun-Priority"); got != "2" {
		t.Fatalf("priority = %q", got)
	}
}

func TestUnixSocketURLParsing(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		basePath string
	}{
		{
			name:     "standard",
			raw:      "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/base",
			basePath: "/base",
		},
		{
			name:     "legacy",
			raw:      "http://unix:/var/run/mirakurun.sock:/base",
			basePath: "/base",
		},
		{
			name:     "legacy encoded socket",
			raw:      "http://unix:%2Fvar%2Frun%2Fmirakurun.sock:/base",
			basePath: "/base",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if client.baseURL.Scheme != "http" || client.baseURL.Host != "unix" || client.baseURL.Path != tt.basePath {
				t.Fatalf("baseURL = %s", client.baseURL.String())
			}
			req, err := client.newRequest(context.Background(), http.MethodGet, "/api/services", nil)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(req.URL.String())
			if err != nil {
				t.Fatal(err)
			}
			if u.Path != "/base/api/services" {
				t.Fatalf("request path = %s", u.Path)
			}
		})
	}
}
