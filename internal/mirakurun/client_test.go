package mirakurun

import (
	"context"
	"fmt"
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
