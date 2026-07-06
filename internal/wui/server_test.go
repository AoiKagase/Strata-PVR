package wui

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{{ID: "abc", IsManualReserved: true}}, false); err != nil {
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

func TestAPIRulesMutation(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodPost, "/api/rules.json", strings.NewReader(`{"isEnabled":false,"categories":["anime"]}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("post status = %d body=%s", res.Code, res.Body.String())
	}
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules length = %d", len(rules))
	}
	if _, ok := rules[0]["isEnabled"]; ok {
		t.Fatalf("isEnabled was not removed: %#v", rules[0])
	}
	if string(rules[0]["isDisabled"]) != "true" {
		t.Fatalf("isDisabled = %s", rules[0]["isDisabled"])
	}

	req = httptest.NewRequest(http.MethodPut, "/api/rules/0/enable.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enable status = %d body=%s", res.Code, res.Body.String())
	}
	rules = nil
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rules[0]["isDisabled"]; ok {
		t.Fatalf("isDisabled was not removed: %#v", rules[0])
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/rules/0.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	rules = nil
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules were not deleted: %#v", rules)
	}
}

func TestAPIProgramPutCreatesManualReserve(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := chinachu.Program{ID: "abc", Title: "番組", Start: time.Now().UnixMilli()}
	schedule := []chinachu.ChannelSchedule{{Programs: []chinachu.Program{program}}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/program/abc.json?mode=1seg", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", res.Code, res.Body.String())
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsManualReserved || !reserves[0].OneSeg {
		t.Fatalf("reserve was not created correctly: %#v", reserves)
	}
}

func TestAPIReserveDeleteRejectsAutomaticReserve(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodDelete, "/api/reserves/abc.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestAPIRecordingDeleteSkipsReserveAndAborts(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Recording, []chinachu.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodDelete, "/api/recording/abc.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || !recording[0].Abort {
		t.Fatalf("recording was not aborted: %#v", recording)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsSkip {
		t.Fatalf("reserve was not skipped: %#v", reserves)
	}
}

func TestAPIRecordedFileJSONM2TSAndDelete(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []chinachu.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("json status = %d body=%s", res.Code, res.Body.String())
	}
	var stat map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &stat); err != nil {
		t.Fatal(err)
	}
	if stat["size"].(float64) != 6 {
		t.Fatalf("size = %#v", stat["size"])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("m2ts status = %d body=%s", res.Code, res.Body.String())
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "tsdata" {
		t.Fatalf("m2ts body = %q", body)
	}
	if got := res.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="abc.m2ts"`) {
		t.Fatalf("content-disposition = %q", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/recorded/abc/file.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(recordedPath); !os.IsNotExist(err) {
		t.Fatalf("recorded file still exists or unexpected stat error: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusGone {
		t.Fatalf("gone status = %d body=%s", res.Code, res.Body.String())
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
