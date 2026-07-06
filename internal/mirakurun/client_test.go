package mirakurun

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services" {
			t.Fatalf("path = %s", r.URL.Path)
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
