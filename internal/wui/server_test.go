package wui

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

func TestStaticImageCacheHeadersMatchLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "web")
	if err := os.MkdirAll(webRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "favicon.ico"), []byte("ico"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "logo.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := testPaths(dir)
	paths.WebRoot = webRoot
	handler := NewHandler(paths, &config.Config{})

	for _, path := range []string{"/favicon.ico", "/logo.png"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != "private, max-age=86400" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("index status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index Cache-Control = %q", got)
	}
}

func TestStaticContentTypesMatchLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "web")
	if err := os.MkdirAll(webRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"app.js":        "text/javascript",
		"style.css":     "text/css",
		"cursor.cur":    "image/vnd.microsoft.icon",
		"stream.m2ts":   "video/MP2T",
		"playlist.xspf": "application/xspf+xml",
		"data.json":     "application/json; charset=utf-8",
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(webRoot, name), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	paths := testPaths(dir)
	paths.WebRoot = webRoot
	handler := NewHandler(paths, &config.Config{})

	for name, want := range files {
		req := httptest.NewRequest(http.MethodGet, "/"+name, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", name, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Content-Type"); got != want {
			t.Fatalf("%s Content-Type = %q, want %q", name, got, want)
		}
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

func TestAPIMethodQueryOverrideMatchesLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{{ID: "abc", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/reserves/abc/skip.json?method=put", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("method override status = %d body=%s", res.Code, res.Body.String())
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsSkip {
		t.Fatalf("reserve was not skipped: %#v", reserves)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/reserves/abc/unskip.json?_method=put", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("_method override status = %d body=%s", res.Code, res.Body.String())
	}
	reserves = nil
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].IsSkip {
		t.Fatalf("reserve was not unskipped: %#v", reserves)
	}
}

func TestHostHeaderRequiredMatchesLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = ""
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
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

func TestAPIRulesMutationFromQueryMatchesLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodPost, `/api/rules.json?types=["GR"]&reserve_titles=["Title"]&isEnabled=false`, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("query post status = %d body=%s", res.Code, res.Body.String())
	}
	var rules []map[string]json.RawMessage
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules length = %d", len(rules))
	}
	var types []string
	if err := json.Unmarshal(rules[0]["types"], &types); err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err := json.Unmarshal(rules[0]["reserve_titles"], &titles); err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0] != "GR" || len(titles) != 1 || titles[0] != "Title" {
		t.Fatalf("query arrays were not preserved: %#v", rules[0])
	}
	if string(rules[0]["isDisabled"]) != "true" {
		t.Fatalf("isDisabled = %s", rules[0]["isDisabled"])
	}

	req = httptest.NewRequest(http.MethodPut, `/api/rules/0.json?categories=["anime"]&sid=101`, nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("query put status = %d body=%s", res.Code, res.Body.String())
	}
	rules = nil
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	var categories []string
	if err := json.Unmarshal(rules[0]["categories"], &categories); err != nil {
		t.Fatal(err)
	}
	var sid int
	if err := json.Unmarshal(rules[0]["sid"], &sid); err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0] != "anime" || sid != 101 {
		t.Fatalf("query put values were not preserved: %#v", rules[0])
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

func TestAPIScheduleChannelRoutes(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	now := time.Now()
	schedule := []chinachu.ChannelSchedule{
		{
			Channel: chinachu.Channel{ID: "gr101", Name: "GR 101"},
			Programs: []chinachu.Program{
				{ID: "onair", Title: "On Air", Start: now.Add(-time.Minute).UnixMilli(), End: now.Add(time.Minute).UnixMilli()},
				{ID: "future", Title: "Future", Start: now.Add(time.Hour).UnixMilli(), End: now.Add(2 * time.Hour).UnixMilli()},
			},
		},
		{
			Channel:  chinachu.Channel{ID: "gr102", Name: "GR 102"},
			Programs: []chinachu.Program{{ID: "other", Title: "Other", Start: now.Add(-time.Minute).UnixMilli(), End: now.Add(time.Minute).UnixMilli()}},
		},
	}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/schedule/gr101.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("channel status = %d body=%s", res.Code, res.Body.String())
	}
	var channel chinachu.ChannelSchedule
	if err := json.Unmarshal(res.Body.Bytes(), &channel); err != nil {
		t.Fatal(err)
	}
	if channel.ID != "gr101" || len(channel.Programs) != 2 {
		t.Fatalf("unexpected channel: %#v", channel)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedule/gr101/programs.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("channel programs status = %d body=%s", res.Code, res.Body.String())
	}
	var programs []chinachu.Program
	if err := json.Unmarshal(res.Body.Bytes(), &programs); err != nil {
		t.Fatal(err)
	}
	if len(programs) != 2 || programs[0].ID != "onair" || programs[1].ID != "future" {
		t.Fatalf("unexpected channel programs: %#v", programs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedule/broadcasting.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("broadcasting status = %d body=%s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &programs); err != nil {
		t.Fatal(err)
	}
	if len(programs) != 2 || programs[0].ID != "onair" || programs[1].ID != "other" {
		t.Fatalf("unexpected broadcasting programs: %#v", programs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedule/gr101/broadcasting.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("channel broadcasting status = %d body=%s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &programs); err != nil {
		t.Fatal(err)
	}
	if len(programs) != 1 || programs[0].ID != "onair" {
		t.Fatalf("unexpected channel broadcasting programs: %#v", programs)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/schedule/missing.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing channel status = %d", res.Code)
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

func TestAPIRecordedCleanupBacksUpBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	existingPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(existingPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorded := []chinachu.Program{
		{ID: "exists", Recorded: filepath.ToSlash(existingPath)},
		{ID: "missing", Recorded: filepath.ToSlash(filepath.Join(dir, "missing.m2ts"))},
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodPut, "/api/recorded.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("cleanup status = %d body=%s", res.Code, res.Body.String())
	}
	backups, err := filepath.Glob(paths.Recorded + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d backups=%#v", len(backups), backups)
	}
	var backup []chinachu.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(paths.Recorded, &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "exists" {
		t.Fatalf("cleanup should keep only existing recording: %#v", got)
	}
}

func TestAPIRecordedDeleteBacksUpBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recorded := []chinachu.Program{
		{ID: "abc", Recorded: filepath.ToSlash(filepath.Join(dir, "abc.m2ts"))},
		{ID: "def", Recorded: filepath.ToSlash(filepath.Join(dir, "def.m2ts"))},
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodDelete, "/api/recorded/abc.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	backups, err := filepath.Glob(paths.Recorded + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d backups=%#v", len(backups), backups)
	}
	var backup []chinachu.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(paths.Recorded, &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "def" {
		t.Fatalf("delete should remove target only: %#v", got)
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

func TestAPIRecordedWatchXSPFAndM2TS(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("watchdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []chinachu.Program{{ID: "abc", Title: "Title & One", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.xspf?prefix=/api/recorded/abc/&ext=m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("xspf status=%d body=%q", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Title &amp; One") || !strings.Contains(res.Body.String(), "watch.m2ts?prefix=/api/recorded/abc/") {
		t.Fatalf("unexpected xspf: %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "watchdata" {
		t.Fatalf("m2ts status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIProgramPreviewDisabled(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Recorded, []chinachu.Program{{ID: "recorded"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []chinachu.Program{{ID: "recording"}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	for _, target := range []string{
		"/api/recorded/recorded/preview.png",
		"/api/recorded/recorded/preview.jpg",
		"/api/recorded/recorded/preview.txt",
		"/api/recording/recording/preview.png",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%q", target, res.Code, res.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/missing/preview.png", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing preview status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIRecordingWatchRequiresPID(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recording.m2ts")
	if err := os.WriteFile(recordedPath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []chinachu.Program{{ID: "abc", Title: "Live", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recording/abc/watch.m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing pid status=%d body=%q", res.Code, res.Body.String())
	}

	if err := storage.WriteJSONAtomic(paths.Recording, []chinachu.Program{{ID: "abc", Title: "Live", Recorded: filepath.ToSlash(recordedPath), PID: 123}}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req = httptest.NewRequest(http.MethodGet, "/api/recording/abc/watch.m2ts", nil).WithContext(ctx)
	res = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(res, req)
		close(done)
	}()
	if err := os.WriteFile(recordedPath, []byte("livefollow"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done
	if res.Code != http.StatusOK || res.Body.String() != "livefollow" {
		t.Fatalf("recording watch status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIChannelLogoAndWatchProxyMirakurun(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []chinachu.ChannelSchedule{{
		Channel: chinachu.Channel{ID: chid, Name: "Service & One"},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	requests := []string{}
	mirakurunServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/services/123/logo":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("pngdata"))
		case "/api/services/123/stream":
			_, _ = w.Write([]byte("tsdata"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mirakurunServer.Close()
	handler := NewHandler(paths, &config.Config{MirakurunPath: mirakurunServer.URL + "/"})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/"+chid+"/logo.png", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "pngdata" {
		t.Fatalf("logo status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/channel/"+chid+"/watch.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "tsdata" {
		t.Fatalf("watch status=%d body=%q", res.Code, res.Body.String())
	}
	want := []string{"/api/services/123/logo", "/api/services/123/stream?decode=1"}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("request[%d] = %q, want %q", i, requests[i], want[i])
		}
	}
}

func TestAPIChannelWatchXSPF(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []chinachu.ChannelSchedule{{
		Channel: chinachu.Channel{ID: chid, Name: "Service & One"},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/"+chid+"/watch.xspf?prefix=/api/channel/"+chid+"/&ext=m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("xspf status=%d body=%q", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Service &amp; One") {
		t.Fatalf("xspf title was not escaped: %q", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "watch.m2ts?prefix=/api/channel/") {
		t.Fatalf("xspf target missing: %q", res.Body.String())
	}
}

func TestAPIStorage(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []chinachu.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{RecordedDir: dir})
	req := httptest.NewRequest(http.MethodGet, "/api/storage.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("storage status=%d body=%q", res.Code, res.Body.String())
	}
	var usage map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage["recorded"].(float64) != 5 {
		t.Fatalf("recorded = %#v", usage["recorded"])
	}
	if usage["size"].(float64) <= 0 || usage["avail"].(float64) <= 0 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestAPILogAndStream(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	if err := os.MkdirAll(paths.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LogDir, "wui"), []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/log/wui.txt", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "line\n" {
		t.Fatalf("log status=%d body=%q", res.Code, res.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	req = httptest.NewRequest(http.MethodGet, "/api/log/wui/stream.txt", nil).WithContext(ctx)
	res = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(res, req)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after request cancellation")
	}
	if res.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%q", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "line\n") || len(res.Body.String()) <= len("line\n") {
		t.Fatalf("stream body missing padding or log: %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/log/operator.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("missing log status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPILogStreamFollowsAppends(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	if err := os.MkdirAll(paths.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(paths.LogDir, "wui")
	if err := os.WriteFile(logPath, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/log/wui/stream.txt", nil).WithContext(ctx)
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(res, req)
		close(done)
	}()
	if err := os.WriteFile(logPath, []byte("initial\nfollowed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done
	if !strings.Contains(res.Body.String(), "followed\n") {
		t.Fatalf("stream did not follow appended log: %q", res.Body.String())
	}
}

func TestRunWritesWUILog(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Config, []byte(`{"wuiHost":"127.0.0.1","wuiPort":`+strconv.Itoa(port)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, paths) }()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		data, err := os.ReadFile(filepath.Join(paths.LogDir, "wui"))
		if err == nil && strings.Contains(string(data), "HTTP Server Listening on") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	data, err := os.ReadFile(filepath.Join(paths.LogDir, "wui"))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	if !strings.Contains(logText, "HTTP Server Listening on") || !strings.Contains(logText, "HTTP Server Closed") {
		t.Fatalf("wui log missing expected lines: %s", logText)
	}
}

func TestAPISchedulerJSONTXTAndPut(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	if err := os.MkdirAll(paths.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := []chinachu.ChannelSchedule{{
		Programs: []chinachu.Program{
			{ID: "aaa", Title: "Reserve"},
			{ID: "bbb", Title: "Conflict"},
		},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	logData := "old\nRUNNING SCHEDULER.\nRESERVE: aaa\nCONFLICT: bbb\n"
	if err := os.WriteFile(filepath.Join(paths.LogDir, "scheduler"), []byte(logData), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	paths.Scheduler = func(_ context.Context, simulation bool) error {
		calls++
		if simulation {
			t.Fatal("scheduler should not be called in simulation mode")
		}
		return nil
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/scheduler.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("scheduler json status=%d body=%q", res.Code, res.Body.String())
	}
	var result struct {
		Time      int64              `json:"time"`
		Conflicts []chinachu.Program `json:"conflicts"`
		Reserves  []chinachu.Program `json:"reserves"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Time == 0 || len(result.Reserves) != 1 || result.Reserves[0].ID != "aaa" || len(result.Conflicts) != 1 || result.Conflicts[0].ID != "bbb" {
		t.Fatalf("unexpected scheduler result: %#v", result)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/scheduler.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != logData {
		t.Fatalf("scheduler txt status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/scheduler.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || calls != 1 {
		t.Fatalf("scheduler put status=%d calls=%d body=%q", res.Code, calls, res.Body.String())
	}
}

func TestAPISchedulerNoLogAndForce(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	done := make(chan struct{}, 1)
	paths.Scheduler = func(_ context.Context, _ bool) error {
		done <- struct{}{}
		return nil
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/scheduler.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("scheduler missing log status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/scheduler/force.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("scheduler force status=%d body=%q", res.Code, res.Body.String())
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler force did not run")
	}
}

func TestAPIStatusReadsPIDFiles(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.OperatorPID = filepath.Join(dir, "operator.pid")
	paths.SchedulerPID = filepath.Join(dir, "scheduler.pid")
	currentPID := os.Getpid()
	if err := os.WriteFile(paths.OperatorPID, []byte(strconv.Itoa(currentPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SchedulerPID, []byte("-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
	var status struct {
		Operator  map[string]any `json:"operator"`
		Scheduler map[string]any `json:"scheduler"`
		Feature   map[string]any `json:"feature"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Operator["pid"].(float64) != float64(currentPID) || status.Operator["alive"] != true {
		t.Fatalf("unexpected operator status: %#v", status.Operator)
	}
	if status.Scheduler["pid"] != nil || status.Scheduler["alive"] != false {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.Feature["streamer"] != true || status.Feature["previewer"] != false || status.Feature["filer"] != true || status.Feature["configurator"] != true {
		t.Fatalf("unexpected feature flags: %#v", status.Feature)
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
	if got := res.Header().Get("WWW-Authenticate"); got != `Basic realm="Authentication."` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status with auth = %d body=%s", res.Code, res.Body.String())
	}
}

func TestAPIConfigGetAndPut(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := os.MkdirAll(filepath.Dir(paths.Config), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{"wuiOpenServer":true}`
	if err := os.WriteFile(paths.Config, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/config.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != initial {
		t.Fatalf("config get status=%d body=%q", res.Code, res.Body.String())
	}

	next := `{"wuiOpenServer":false,"wuiOpenPort":20772}`
	req = httptest.NewRequest(http.MethodPut, "/api/config.json?json="+url.QueryEscape(next), nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != next {
		t.Fatalf("config put status=%d body=%q", res.Code, res.Body.String())
	}
	data, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != next {
		t.Fatalf("config file = %q", data)
	}
}

func TestAPIConfigPutRequiresValidJSON(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := os.WriteFile(paths.Config, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	for _, target := range []string{"/api/config.json", "/api/config.json?json=%7B"} {
		req := httptest.NewRequest(http.MethodPut, target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", target, res.Code)
		}
	}
}

func TestOpenServerHandlerSkipsAuth(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := newHandler(paths, &config.Config{WUIUsers: []string{"user:pass"}}, false)
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("open status without auth = %d body=%s", res.Code, res.Body.String())
	}
}

func TestBuildTLSConfigClientAuth(t *testing.T) {
	cfg := &config.Config{
		WUITlsKeyPath:            "key.pem",
		WUITlsCertPath:           "cert.pem",
		WUITlsRequestCert:        true,
		WUITlsRejectUnauthorized: true,
	}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig == nil {
		t.Fatal("tls config was nil")
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("client auth = %s", tlsConfig.ClientAuth)
	}
}

func TestBuildTLSConfigCAError(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		WUITlsKeyPath:  "key.pem",
		WUITlsCertPath: "cert.pem",
		WUITlsCaPath:   caPath,
	}
	if _, err := buildTLSConfig(cfg); err == nil {
		t.Fatal("expected CA parse error")
	}
}

func TestPrivateIPv4FromAddrs(t *testing.T) {
	_, publicNet, err := net.ParseCIDR("203.0.113.10/24")
	if err != nil {
		t.Fatal(err)
	}
	publicNet.IP = net.ParseIP("203.0.113.10")
	_, privateNet, err := net.ParseCIDR("192.168.10.20/24")
	if err != nil {
		t.Fatal(err)
	}
	privateNet.IP = net.ParseIP("192.168.10.20")
	got := privateIPv4FromAddrs([]net.Addr{
		&net.IPAddr{IP: net.ParseIP("2001:db8::1")},
		publicNet,
		privateNet,
	})
	if got != "192.168.10.20" {
		t.Fatalf("private IPv4 = %q", got)
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

func TestAccessLogTrustsXForwardedForWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	handler := NewHandler(paths, &config.Config{WUIXFF: true})
	req := httptest.NewRequest(http.MethodGet, "/api/status.json?foo=bar", nil)
	req.RemoteAddr = "[::ffff:10.0.0.1]:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 198.51.100.2")
	req.Header.Set("User-Agent", "test-agent")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	logBytes, err := os.ReadFile(filepath.Join(paths.LogDir, "wui"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, `200 GET:/api/status.json?foo=bar 203.0.113.7 "test-agent"`) {
		t.Fatalf("access log did not use X-Forwarded-For: %q", log)
	}
}

func TestAccessLogKeepsRemoteAddressWhenXForwardedForDisabled(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.RemoteAddr = "[::ffff:10.0.0.1]:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	logBytes, err := os.ReadFile(filepath.Join(paths.LogDir, "wui"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, `200 GET:/api/status.json 10.0.0.1`) {
		t.Fatalf("access log did not use RemoteAddr: %q", log)
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
