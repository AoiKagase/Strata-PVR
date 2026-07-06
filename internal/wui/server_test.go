package wui

import (
	"context"
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
	req = httptest.NewRequest(http.MethodGet, "/api/recording/abc/watch.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "live" {
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

	req = httptest.NewRequest(http.MethodGet, "/api/log/wui/stream.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%q", res.Code, res.Body.String())
	}
	if !strings.HasSuffix(res.Body.String(), "line\n") || len(res.Body.String()) <= len("line\n") {
		t.Fatalf("stream body missing padding or log: %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/log/operator.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("missing log status=%d body=%q", res.Code, res.Body.String())
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
	if err := os.WriteFile(paths.OperatorPID, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SchedulerPID, []byte("456\n"), 0o644); err != nil {
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
	}
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Operator["pid"].(float64) != 123 || status.Scheduler["pid"].(float64) != 456 {
		t.Fatalf("unexpected status: %#v", status)
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
