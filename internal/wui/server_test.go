package wui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/storage"
)

func TestAPIReadsLegacyJSONState(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := chinachu.Program{ID: "abc", Title: "番組", Channel: chinachu.Channel{Name: "svc"}}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/reserves.json", nil)
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var got []chinachu.Program
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "abc" {
		t.Fatalf("unexpected reserves: %#v", got)
	}
}

func TestAPIReserveSkipAndDelete(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/reserves/abc/skip.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("skip status = %d body=%s", res.Code, res.Body.String())
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsSkip {
		t.Fatalf("reserve was not skipped: %#v", reserves)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/reserves/abc.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("reserve was not deleted: %#v", reserves)
	}
}

func TestAPIAuth(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{WUIUsers: []string{"user:pass"}})
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status with auth = %d body=%s", res.Code, res.Body.String())
	}
}

func TestStaticServingUsesWebRoot(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "web")
	if err := os.MkdirAll(webRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := testPaths(dir)
	paths.WebRoot = webRoot
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "ok" {
		t.Fatalf("static response status=%d body=%q", res.Code, res.Body.String())
	}
}

func testPaths(dir string) Paths {
	return Paths{
		Config:    filepath.Join(dir, "config.json"),
		Rules:     filepath.Join(dir, "rules.json"),
		Schedule:  filepath.Join(dir, "data", "schedule.json"),
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	}
}
