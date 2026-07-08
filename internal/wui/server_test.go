package wui

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

	"strata-pvr/internal/config"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func TestAPIReadsLegacyJSONState(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := legacy.Program{ID: "abc", Title: "番組", Channel: legacy.Channel{Name: "svc"}}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/reserves.json", nil)
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var got []legacy.Program
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "abc" {
		t.Fatalf("unexpected reserves: %#v", got)
	}
	if strings.Contains(res.Body.String(), "\n") {
		t.Fatalf("legacy reserves list should be compact JSON, got %q", res.Body.String())
	}
}

func TestAPIProgramReadsUseLegacyPrettyJSON(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	now := time.Now()
	program := legacy.Program{
		ID:      "abc",
		Title:   "番組",
		Start:   now.Add(-time.Minute).UnixMilli(),
		End:     now.Add(time.Minute).UnixMilli(),
		Channel: legacy.Channel{ID: "gr101", Name: "GR 101"},
	}
	schedule := []legacy.ChannelSchedule{{
		Channel:  legacy.Channel{ID: "gr101", Name: "GR 101"},
		Programs: []legacy.Program{program},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	for _, path := range []string{
		"/api/program/abc.json",
		"/api/reserves/abc.json",
		"/api/recording/abc.json",
		"/api/recorded/abc.json",
		"/api/schedule/programs.json",
		"/api/schedule/broadcasting.json",
		"/api/schedule/gr101.json",
		"/api/schedule/gr101/programs.json",
		"/api/schedule/gr101/broadcasting.json",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, res.Code, res.Body.String())
		}
		body := res.Body.String()
		if !strings.Contains(body, "\n  ") {
			t.Fatalf("%s should use legacy pretty JSON, got %q", path, body)
		}
		var decoded any
		if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
	}
}

func TestAPIListReadsUseLegacyCompactJSON(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := legacy.Program{ID: "abc", Title: "番組", Start: 1, End: 2}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	for _, path := range []string{"/api/reserves.json", "/api/recording.json", "/api/recorded.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", path, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "\n") {
			t.Fatalf("%s should use compact JSON, got %q", path, res.Body.String())
		}
		var decoded []legacy.Program
		if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s invalid JSON: %v", path, err)
		}
	}
}

func TestAPIRejectsUnsupportedResourceTypes(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	if err := os.MkdirAll(paths.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.LogDir, "wui"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/status.txt", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status.txt status = %d body=%s", res.Code, res.Body.String())
	}
	if res.Body.String() != "415 Unsupported Media Type\n" {
		t.Fatalf("status.txt body=%q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status without extension status = %d body=%s", res.Code, res.Body.String())
	}
	if res.Body.String() != "415 Unsupported Media Type\n" {
		t.Fatalf("status without extension body=%q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/api/schedule.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType || res.Body.Len() != 0 {
		t.Fatalf("schedule.txt HEAD status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/status.JSON", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || res.Body.String() != "404 Not Found\n" {
		t.Fatalf("uppercase extension status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/log/wui.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("log json status = %d body=%s", res.Code, res.Body.String())
	}

	for _, target := range []string{
		"/api/recording/missing/preview",
		"/api/recording/missing/watch",
		"/api/recorded/missing/preview",
		"/api/recorded/missing/watch",
		"/api/channel/missing/logo",
		"/api/channel/missing/watch",
		"/api/channel/missing/logo.jpg",
	} {
		req = httptest.NewRequest(http.MethodGet, target, nil)
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnsupportedMediaType || res.Body.String() != "415 Unsupported Media Type\n" {
			t.Fatalf("%s status=%d body=%q", target, res.Code, res.Body.String())
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/log/wui.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("log txt status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("log txt content-type = %q", got)
	}
}

func TestAPIHeadMethodsMatchLegacyResources(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Schedule, []legacy.ChannelSchedule{}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodHead, "/api/schedule.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.Len() != 0 {
		t.Fatalf("schedule HEAD status=%d body=%q", res.Code, res.Body.String())
	}

	for _, tc := range []struct {
		path  string
		allow string
	}{
		{"/api/status.json", "GET"},
		{"/api/config.json", "GET, PUT"},
		{"/api/recorded/abc/watch.m2ts", "GET"},
	} {
		req = httptest.NewRequest(http.MethodHead, tc.path, nil)
		res = httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s HEAD status=%d body=%q", tc.path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Allow"); got != tc.allow {
			t.Fatalf("%s Allow=%q, want %q", tc.path, got, tc.allow)
		}
	}
}

func TestAPIInternalErrorsUseLegacyBody(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := os.MkdirAll(filepath.Dir(paths.Reserves), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/reserves.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
	if res.Body.String() != "500 Internal Server Error\n" {
		t.Fatalf("body=%q", res.Body.String())
	}
}

func TestAPIBadKnownResourcePathMatchesLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/schedule/foo/bar/baz.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("known resource bad path status=%d body=%q", res.Code, res.Body.String())
	}
	if res.Body.String() != "400 Bad Request\n" {
		t.Fatalf("known resource bad path body=%q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/index.html", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || res.Body.String() != "400 Bad Request\n" {
		t.Fatalf("api index status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/status.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed status=%d body=%q", res.Code, res.Body.String())
	}
	if res.Body.String() != "405 Method Not Allowed\n" {
		t.Fatalf("method not allowed body=%q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/no-such-resource/foo.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown resource status=%d body=%q", res.Code, res.Body.String())
	}
	if res.Body.String() != "404 Not Found\n" {
		t.Fatalf("unknown resource body=%q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/no-such-resource", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown resource without extension status=%d body=%q", res.Code, res.Body.String())
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
	if got := res.Header().Get("Server"); got != "Strata PVR" {
		t.Fatalf("Server = %q", got)
	}
	for key, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"X-UA-Compatible":        "IE=Edge,chrome=1",
		"X-XSS-Protection":       "1; mode=block",
	} {
		if got := res.Header().Get(key); got != want {
			t.Fatalf("%s = %q", key, got)
		}
	}
	lastModified := res.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("index Last-Modified missing")
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-Modified-Since", lastModified)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotModified || res.Body.Len() != 0 {
		t.Fatalf("conditional static status=%d body=%q", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=0-3")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusPartialContent || res.Body.String() != "<!do" {
		t.Fatalf("range static status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Range"); got != "bytes 0-3/15" {
		t.Fatalf("Content-Range = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/missing.html", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || res.Body.String() != "404 Not Found\n" {
		t.Fatalf("missing static status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=16-20")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestedRangeNotSatisfiable || res.Body.String() != "416 Requested Range Not Satisfiable\n" {
		t.Fatalf("invalid range status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestNativeDashboardAssetsServe(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Clean(filepath.Join("..", "..", "web"))
	for _, name := range []string{"index.html", "app.js", "styles.css"} {
		if _, err := os.Stat(filepath.Join(webRoot, name)); err != nil {
			t.Fatalf("native web asset %s missing: %v", name, err)
		}
	}
	paths := testPaths(dir)
	paths.WebRoot = webRoot
	handler := NewHandler(paths, &config.Config{})

	for _, tc := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/", "text/html", "scheduleChannelTools"},
		{"/app.js", "text/javascript", "renderScheduleChannelTools"},
		{"/styles.css", "text/css", ".channel-tools"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%q", tc.path, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Content-Type"); got != tc.contentType {
			t.Fatalf("%s Content-Type=%q", tc.path, got)
		}
		if !strings.Contains(res.Body.String(), tc.contains) {
			t.Fatalf("%s body missing %q", tc.path, tc.contains)
		}
	}
}

func TestNativeDashboardListFilters(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "index.html"): {
			`id="channelProgramsQuery"`,
			`id="channelProgramsFilterSummary"`,
			`id="reserveListQuery"`,
			`id="reserveListCategory"`,
			`id="recordedListQuery"`,
			`id="recordedListCategory"`,
			`id="ruleListQuery"`,
			`id="ruleListState"`,
			`id="ruleListFilterSummary"`,
			`list-filter-summary`,
		},
		filepath.Join("..", "..", "web", "app.js"): {
			`listFilters`,
			`listFiltersStorageKey`,
			`function loadListFilters()`,
			`function saveListFilters()`,
			`filteredPrograms`,
			`filteredRules`,
			`programSearchText`,
			`updateListCategoryOptions`,
			`state.listFilters.channelPrograms.query`,
			`state.listFilters.channelPrograms.category`,
			`state.listFilters.rules.state`,
			`saveListFilters();`,
			`bindListFilter("reserves"`,
			`bindListFilter("recorded"`,
			`条件に一致する番組はありません`,
			`条件に一致する予約はありません`,
			`条件に一致する録画済み番組はありません`,
			`条件に一致するルールはありません`,
		},
		filepath.Join("..", "..", "web", "styles.css"): {
			`.list-filter-controls`,
			`.list-filter-summary`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestNativeDashboardLiveWatchActionsPreferMP4Playback(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	if strings.Contains(source, `"watch-recording"`) {
		t.Fatal("recording list should not expose live/growing M2TS watch action")
	}
	for _, legacyLabel := range []string{`actionButton("MP4"`, "録画中のM2TS", "チャンネルのM2TS"} {
		if strings.Contains(source, legacyLabel) {
			t.Fatalf("native dashboard still exposes confusing live watch label/action %q", legacyLabel)
		}
	}
	for _, recordedOnly := range []string{`name === "watch-m2ts"`, `name === "watch-m2ts-offset"`} {
		if !strings.Contains(source, recordedOnly) {
			t.Fatalf("recorded M2TS action %q should remain available", recordedOnly)
		}
	}
}

func TestNativeDashboardConfirmDialog(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "index.html"): {
			`id="confirmDialog"`,
			`id="confirmDialogMessage"`,
			`id="confirmDialogCancel"`,
			`id="confirmDialogOK"`,
			`aria-describedby="confirmDialogMessage"`,
		},
		filepath.Join("..", "..", "web", "app.js"): {
			`function confirmAction(message, options)`,
			`pendingConfirmResolve`,
			`confirmDialogReturnFocus`,
			`restoreFocus(confirmDialogReturnFocus)`,
			`actionConfirmOptions("DELETE"`,
			`録画停止の確認`,
			`録画済み削除の確認`,
			`設定保存の確認`,
			`ルール追加の確認`,
			`ルール保存の確認`,
			`JSONエディタの内容でルールを追加しますか？`,
			`フォームの内容でルールを追加しますか？`,
		},
		filepath.Join("..", "..", "web", "styles.css"): {
			`.confirm-dialog .program-dialog-actions`,
			`.danger-button`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestNativeDashboardKeyboardMouseShortcuts(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "app.js"): {
			`function initKeyboardShortcuts()`,
			`isEditableTarget(event.target)`,
			`"1": "dashboard"`,
			`"7": "settings"`,
			`event.key === "/"`,
			`focusCurrentSearch`,
			`event.key === "r" || event.key === "R"`,
			`event.key === "Escape" && closeTopDialog()`,
			`event.key === "j" || event.key === "ArrowDown"`,
			`event.key === "k" || event.key === "ArrowUp"`,
			`focusAdjacentRow`,
			`addEventListener("dblclick"`,
			`event.key === "Enter" || event.key === " "`,
			`initKeyboardShortcuts();`,
		},
		filepath.Join("..", "..", "web", "styles.css"): {
			`.program-row:focus-visible`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestNativeDashboardRealtimeNotifications(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(app)
	for _, want := range []string{
		`function publishMutation(path, method)`,
		`function publishRealtime(eventName)`,
		`function subscribeRealtimeRefresh()`,
		`window.StrataPVRNotify`,
		`BroadcastChannel("strata-pvr")`,
		`realtimeChannel`,
		`strata-pvr:notify`,
		`notify-reserves`,
		`notify-recording`,
		`notify-recorded`,
		`notify-rules`,
		`notify-schedule`,
		`notify-config`,
		`subscribeRealtimeRefresh();`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("web/app.js missing %q", want)
		}
	}
}

func TestNativeDashboardVisualStateRetention(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "app.js"): {
			`activeProgramID`,
			`viewScrollPositions`,
			`scheduleGuideScroll`,
			`state.viewScrollPositions[state.currentView]`,
			`window.scrollTo(0, state.viewScrollPositions[state.currentView] || 0)`,
			`scroll.scrollLeft = state.scheduleGuideScroll.left || 0`,
			`scroll.scrollTop = state.scheduleGuideScroll.top || 0`,
			`function isActiveProgram(program)`,
			`card.classList.toggle("selected", isActiveProgram(program))`,
			`state.activeProgramID = program && program.id ? program.id : ""`,
			`hasLoaded`,
			`function renderInitialLoadingState()`,
			`function renderInitialLoadError(error)`,
			`setListPlaceholder(id, "読み込み中")`,
			`setListPlaceholder(id, "読み込みに失敗しました", "list empty error")`,
			`setRefreshLoading(false)`,
		},
		filepath.Join("..", "..", "web", "styles.css"): {
			`.program-row.selected`,
			`.schedule-card.selected`,
			`.list.empty.error`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestNativeDashboardMergesProgramRuntimeState(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "app.js"): {
			`programStateIndex`,
			`function decorateProgramState(program)`,
			`program.isRecording`,
			`program.isReserved`,
			`renderProgramStateBadges(item, program)`,
			`録画中`,
			`手動予約`,
			`watch-recording-mp4`,
			`card.classList.toggle("recording"`,
		},
		filepath.Join("..", "..", "web", "styles.css"): {
			`.program-state-badge`,
			`.schedule-card.recording`,
			`.schedule-card.reserved`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestNativeDashboardConfigControlsCoverRuntimeFields(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "web", "index.html"): {
			`id="configUid"`,
			`id="configGid"`,
			`id="configSchedulerMirakurunPath"`,
			`id="configWuiXFF"`,
			`id="configWuiMdnsAdvertisement"`,
			`id="configVaapiDevice"`,
			`id="configRecordingPriority"`,
			`id="configConflictedPriority"`,
			`id="configWuiTlsCaPath"`,
			`id="configWuiTlsRequestCert"`,
			`id="configWuiTlsRejectUnauthorized"`,
			`id="configStorageLowSpaceCommand"`,
			`id="configSchedulerStartCommand"`,
			`id="configEpgEndCommand"`,
			`id="configRecordedCommand"`,
		},
		filepath.Join("..", "..", "web", "app.js"): {
			`cfg.schedulerMirakurunPath`,
			`cfg.wuiXFF`,
			`cfg.wuiMdnsAdvertisement`,
			`cfg.vaapiDevice`,
			`cfg.recordingPriority`,
			`cfg.conflictedPriority`,
			`cfg.wuiTlsCaPath`,
			`cfg.wuiTlsRequestCert`,
			`cfg.wuiTlsRejectUnauthorized`,
			`cfg.storageLowSpaceCommand`,
			`cfg.schedulerStartCommand`,
			`cfg.epgEndCommand`,
			`cfg.recordedCommand`,
			`setOptionalBooleanSelect(config, "wuiXFF"`,
			`setOptionalBooleanSelect(config, "wuiMdnsAdvertisement"`,
			`setOptionalString(config, "schedulerStartCommand"`,
			`setOptionalString(config, "recordedCommand"`,
		},
	}
	for path, wants := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range wants {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q", path, want)
			}
		}
	}
}

func TestSocketIOCompatScript(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/socket.io/socket.io.js", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("socket.io script status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "application/javascript" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := res.Body.String()
	for _, want := range []string{
		"io.connect",
		"notify-",
		"notify-schedule",
		"notify-recording",
		"status.json",
		"5000",
		"pollers[name]",
		"setConnected(false)",
		"'reconnect' : 'disconnect'",
		"emit: function(name)",
		"clearInterval",
		"BroadcastChannel",
		"StrataPVRNotify",
		"strata-pvr:notify",
		"createRealtimeBus",
		"once:",
		"off:",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("socket.io compat script missing %q: %s", want, body)
		}
	}

	req = httptest.NewRequest(http.MethodHead, "/socket.io/socket.io.js", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.Len() != 0 {
		t.Fatalf("socket.io HEAD status=%d body=%q", res.Code, res.Body.String())
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
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{{ID: "abc", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/reserves/abc/skip.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("skip status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Body.String(); got != `{}` {
		t.Fatalf("skip body = %q", got)
	}
	var reserves []legacy.Program
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
	if got := res.Body.String(); got != `{}` {
		t.Fatalf("delete body = %q", got)
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
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{{ID: "abc", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/reserves/abc/skip.json?method=put", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("method override status = %d body=%s", res.Code, res.Body.String())
	}
	var reserves []legacy.Program
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
	if got := res.Body.String(); got != `{"categories":["anime"],"isDisabled":true}` {
		t.Fatalf("post body = %q", got)
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
	if got := res.Body.String(); got != `{}` {
		t.Fatalf("enable body = %q", got)
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
	if got := res.Body.String(); got != `{}` {
		t.Fatalf("delete body = %q", got)
	}
	rules = nil
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules were not deleted: %#v", rules)
	}
}

func TestAPIRulesGetUsesLegacyPrettyJSON(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	if err := storage.WriteJSONAtomic(paths.Rules, []map[string]any{{"categories": []string{"anime"}}}, true); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/rules.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("rules get status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Body.String(); got != "[\n  {\n    \"categories\": [\n      \"anime\"\n    ]\n  }\n]" {
		t.Fatalf("rules get body = %q", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/rules/0.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("rule get status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Body.String(); got != "{\n  \"categories\": [\n    \"anime\"\n  ]\n}" {
		t.Fatalf("rule get body = %q", got)
	}
}

func TestAPIRulesMutationFromQueryMatchesLegacyWUI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, `/api/rules.json?method=post&types=["GR"]&reserve_titles=["Title"]&isEnabled=false`, nil)
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
	if _, ok := rules[0]["method"]; ok {
		t.Fatalf("method override query leaked into rule: %#v", rules[0])
	}

	req = httptest.NewRequest(http.MethodGet, `/api/rules/0.json?_method=put&categories=["anime"]&sid=101`, nil)
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
	if _, ok := rules[0]["_method"]; ok {
		t.Fatalf("_method override query leaked into rule: %#v", rules[0])
	}
}

func TestAPIProgramPutCreatesManualReserve(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := legacy.Program{ID: "abc", Title: "番組", Start: time.Now().UnixMilli()}
	schedule := []legacy.ChannelSchedule{{Programs: []legacy.Program{program}}}
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
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsManualReserved || !reserves[0].OneSeg {
		t.Fatalf("reserve was not created correctly: %#v", reserves)
	}
}

func TestAPIProgramPutSortsManualReserveLikeCLI(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	existing := legacy.Program{ID: "later", Title: "既存", Start: 2000}
	program := legacy.Program{ID: "earlier", Title: "手動", Start: 1000}
	schedule := []legacy.ChannelSchedule{{Programs: []legacy.Program{program}}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{existing}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodPut, "/api/program/earlier.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", res.Code, res.Body.String())
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 2 || reserves[0].ID != "earlier" || reserves[1].ID != "later" {
		t.Fatalf("manual reserve was not sorted by start: %#v", reserves)
	}
	if !reserves[0].IsManualReserved {
		t.Fatalf("manual reserve flag was not set: %#v", reserves[0])
	}
}

func TestAPIProgramGetOnlySearchesSchedule(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := legacy.Program{ID: "abc", Title: "予約だけの番組", Start: 1, End: 2}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/program/abc.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("program GET should not search reserve/recording/recorded lists: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAPIScheduleDeflateAndLastModified(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	schedule := []legacy.ChannelSchedule{{
		Channel:  legacy.Channel{ID: "ch", Type: "GR", Channel: "27"},
		Programs: []legacy.Program{{ID: "p1", Title: "番組", Start: 1, End: 2, Channel: legacy.Channel{ID: "ch"}}},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/schedule.json", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("schedule status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Encoding"); got != "deflate" {
		t.Fatalf("content-encoding = %q", got)
	}
	zr, err := zlib.NewReader(bytes.NewReader(res.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"id":"p1"`) {
		t.Fatalf("unexpected deflated body: %s", body)
	}
	lastModified := res.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("missing Last-Modified")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/schedule.json", nil)
	req.Header.Set("If-Modified-Since", lastModified)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotModified || res.Body.Len() != 0 {
		t.Fatalf("conditional status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIScheduleChannelRoutes(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	now := time.Now()
	schedule := []legacy.ChannelSchedule{
		{
			Channel: legacy.Channel{ID: "gr101", Name: "GR 101"},
			Programs: []legacy.Program{
				{ID: "onair", Title: "On Air", Start: now.Add(-time.Minute).UnixMilli(), End: now.Add(time.Minute).UnixMilli()},
				{ID: "future", Title: "Future", Start: now.Add(time.Hour).UnixMilli(), End: now.Add(2 * time.Hour).UnixMilli()},
			},
		},
		{
			Channel:  legacy.Channel{ID: "gr102", Name: "GR 102"},
			Programs: []legacy.Program{{ID: "other", Title: "Other", Start: now.Add(-time.Minute).UnixMilli(), End: now.Add(time.Minute).UnixMilli()}},
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
	var channel legacy.ChannelSchedule
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
	var programs []legacy.Program
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
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{{ID: "abc"}}, false); err != nil {
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
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{{ID: "abc"}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodDelete, "/api/recording/abc.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || !recording[0].Abort {
		t.Fatalf("recording was not aborted: %#v", recording)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsSkip {
		t.Fatalf("reserve was not skipped: %#v", reserves)
	}
}

func TestAPIRecordingDeleteKeepsManualReserveUnskipped(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	program := legacy.Program{ID: "manual", IsManualReserved: true}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodDelete, "/api/recording/manual.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", res.Code, res.Body.String())
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || !recording[0].Abort {
		t.Fatalf("manual recording was not aborted: %#v", recording)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].IsSkip {
		t.Fatalf("manual reserve should not be skipped: %#v", reserves)
	}
}

func TestAPIRecordedCleanupBacksUpBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	existingPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(existingPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorded := []legacy.Program{
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
	var backup []legacy.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []legacy.Program
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
	recorded := []legacy.Program{
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
	var backup []legacy.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []legacy.Program
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
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file.json", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("json status = %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "\n  ") {
		t.Fatalf("file.json should use legacy pretty JSON, got %q", res.Body.String())
	}
	var stat map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &stat); err != nil {
		t.Fatal(err)
	}
	if stat["size"].(float64) != 6 {
		t.Fatalf("size = %#v", stat["size"])
	}
	for _, key := range []string{"dev", "ino", "mode", "ulink", "uid", "gid", "rdev", "blksize", "blocks", "atime", "mtime", "ctime"} {
		if _, ok := stat[key]; !ok {
			t.Fatalf("stat key %q missing: %#v", key, stat)
		}
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

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType || res.Body.String() != "415 Unsupported Media Type\n" {
		t.Fatalf("file without extension status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/file.m2ts", nil)
	req.Header.Set("Range", "bytes=7-10")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "tsdata" {
		t.Fatalf("file range should be ignored like legacy status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Length"); got != "6" {
		t.Fatalf("file content-length=%q", got)
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
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Title: `Title <&"'> One`, Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0","size":"9","bit_rate":"2400"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.xspf?prefix=/api/recorded/abc/&ext=m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("xspf status=%d body=%q", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Title &amp;lt;&amp;&quot;'&amp;gt; One") || !strings.Contains(res.Body.String(), "watch.m2ts?prefix=/api/recorded/abc/") {
		t.Fatalf("unexpected xspf: %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?mode=download&ext=m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "watchdata" {
		t.Fatalf("m2ts status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Disposition"); got != "attachment; filename*=UTF-8''recorded.m2ts" {
		t.Fatalf("download content-disposition = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts", nil)
	req.Header.Set("Range", "bytes=10-20")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestedRangeNotSatisfiable || res.Body.String() != "416 Requested Range Not Satisfiable\n" {
		t.Fatalf("watch invalid range status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIRecordedWatchMP4UsesFFmpeg(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Title: "Title", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mp4data", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4?s=640x360&b:v=1m&t=30&mode=download&ext=mp4", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "mp4data" {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Disposition"); got != "attachment; filename*=UTF-8''recorded.mp4" {
		t.Fatalf("download content-disposition = %q", got)
	}
	if gotInput != "tsdata" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-f mp4", "-map 0:v:0 -map 0:a:0?", "-sn -dn", "-c:v h264", "-movflags frag_keyframe+empty_moov+faststart+default_base_moof", "-s 640x360", "-b:v 1m", "-bufsize:v 8388608", "-b:a 96k", "-bufsize:a 786432", "-ss 2", "-t 30"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "-c:a aac") {
		t.Fatalf("legacy b:v path should omit explicit audio codec: %s", joined)
	}
	if strings.Contains(joined, "-ac 2") {
		t.Fatalf("legacy b:v path should not force audio channels without an explicit audio codec: %s", joined)
	}
}

func TestAPIRecordedWatchMP4MapsAudioForBrowserPlayback(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mp4data", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-v error", "-map 0:v:0 -map 0:a:0?", "-sn -dn", "-c:a aac", "-ac 2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args missing %q: %s", want, joined)
		}
	}
	if gotInput != "tsdata" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
}

func TestAPIRecordedWatchM2TSTranscodeUsesFFmpeg(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mpegtsdata", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0","size":"6","bit_rate":"1600"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?t=30&b:v=1m", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "mpegtsdata" {
		t.Fatalf("m2ts transcode status=%d body=%q", res.Code, res.Body.String())
	}
	if gotInput != "tsdata" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-f mpegts", "-c:v copy", "-b:v 1m", "-t 30"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("m2ts ffmpeg args missing %q: %s", want, joined)
		}
	}
	if got := res.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if got := res.Header().Get("Content-Length"); got != "4300800" {
		t.Fatalf("Content-Length = %q", got)
	}
}

func TestAPIRecordedWatchM2TSTranscodeRangeMapsSourceRange(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	body := strings.Repeat("0123456789", 100)
	if err := os.WriteFile(recordedPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mpegtsdata", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"10.0","size":"1000","bit_rate":"1000"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?b:v=1k&b:a=1k", nil)
	req.Header.Set("Range", "bytes=0-99")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusPartialContent || res.Body.String() != "mpegtsdata" {
		t.Fatalf("m2ts transcode range status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Range"); got != "bytes 0-99/2560" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := res.Header().Get("Content-Length"); got != "100" {
		t.Fatalf("Content-Length = %q", got)
	}
	if gotInput != body[:49] {
		t.Fatalf("ffmpeg ranged input length=%d input=%q", len(gotInput), gotInput)
	}
}

func TestAPIRecordedWatchM2TSLegacyStartOffset(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	body := strings.Repeat("A", 188) + strings.Repeat("B", 188)
	if err := os.WriteFile(recordedPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0","size":"376","bit_rate":"752"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?ss=4", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("m2ts offset status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if got := res.Header().Get("Content-Length"); got != "188" {
		t.Fatalf("Content-Length = %q", got)
	}
	if got := res.Body.String(); got != strings.Repeat("B", 188) {
		t.Fatalf("unexpected offset body: %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?ss=4", nil)
	req.Header.Set("Range", "bytes=0-93")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusPartialContent {
		t.Fatalf("m2ts range status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Range"); got != "bytes 0-93/188" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := res.Header().Get("Content-Length"); got != "94" {
		t.Fatalf("range Content-Length = %q", got)
	}
	if got := res.Body.String(); got != strings.Repeat("A", 94) {
		t.Fatalf("unexpected range body: %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.m2ts?ss=4", nil)
	req.Header.Set("Range", "bytes=377-400")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestedRangeNotSatisfiable || res.Body.String() != "416 Requested Range Not Satisfiable\n" {
		t.Fatalf("m2ts invalid legacy range status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIRecordedWatchMP4HonorsLegacyStartSecond(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mp4data", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4?ss=15", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	if joined := strings.Join(gotArgs, " "); !strings.Contains(joined, "-ss 15") {
		t.Fatalf("ffmpeg args missing legacy ss: %s", joined)
	}

	gotArgs = nil
	req = httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4?ss=1", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mp4 low-ss status=%d body=%q", res.Code, res.Body.String())
	}
	if joined := strings.Join(gotArgs, " "); !strings.Contains(joined, "-ss 2") {
		t.Fatalf("ffmpeg args did not clamp legacy ss: %s", joined)
	}
}

func TestAPIRecordedWatchRejectsStartBeyondDuration(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"10.0"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4?ss=15", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusRequestedRangeNotSatisfiable || res.Body.String() != "416 Requested Range Not Satisfiable\n" {
		t.Fatalf("out-of-range ss status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIRecordedWatchXSPFProbeError(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	restoreProbe := installFakeFFprobe(t, "", fmt.Errorf("fake ffprobe error"))
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.xspf", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError || res.Body.String() != "500 Internal Server Error\n" {
		t.Fatalf("probe error status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestLegacyBitrateBits(t *testing.T) {
	tests := map[string]int64{
		"96k":  96 * 1024,
		"1m":   1024 * 1024,
		"2M":   2 * 1024 * 1024,
		"1000": 0,
		"badk": 0,
	}
	for input, want := range tests {
		if got := legacyBitrateBits(input); got != want {
			t.Fatalf("legacyBitrateBits(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestAPIRecordedWatchMP4UsesVAAPIOptions(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "mp4data", &gotInput, &gotArgs)
	defer restore()
	restoreProbe := installFakeFFprobe(t, `{"format":{"duration":"30.0"}}`, nil)
	defer restoreProbe()
	handler := NewHandler(paths, &config.Config{VAAPIEnabled: true, VAAPIDevice: "/dev/dri/test"})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/abc/watch.mp4?s=1280x720", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-vaapi_device /dev/dri/test", "-hwaccel vaapi", "-vf format=nv12|vaapi,hwupload,deinterlace_vaapi,scale_vaapi=w=1280:h=720", "-c:v h264_vaapi"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ffmpeg args missing %q: %s", want, joined)
		}
	}
	if gotInput != "tsdata" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
}

func TestAPIProgramPreview(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := installFakeFFmpeg(t, "preview-image")
	defer restore()

	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "recorded", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "recording", Recorded: filepath.ToSlash(recordedPath), PID: 123}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	for _, target := range []string{"/api/recorded/recorded/preview.png", "/api/recorded/recorded/preview.jpg", "/api/recording/recording/preview.png"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || res.Body.String() != "preview-image" {
			t.Fatalf("%s status=%d body=%q", target, res.Code, res.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/recorded/preview.txt?type=png&size=640x360&pos=9", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "data:image/png;base64,cHJldmlldy1pbWFnZQ==" {
		t.Fatalf("preview txt status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recorded/missing/preview.png", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing preview status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIProgramPreviewLegacyErrors(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	missingPath := filepath.Join(dir, "missing.m2ts")
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{
		{ID: "scrambled", Recorded: filepath.ToSlash(missingPath), Raw: map[string]json.RawMessage{"tuner": json.RawMessage(`{"isScrambling":true}`)}},
		{ID: "gone", Recorded: filepath.ToSlash(missingPath)},
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "nopid", Recorded: filepath.ToSlash(missingPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/api/recording/nopid/preview.png", http.StatusServiceUnavailable},
		{"/api/recorded/scrambled/preview.png", http.StatusConflict},
		{"/api/recorded/gone/preview.png", http.StatusGone},
		{"/api/recorded/gone/preview.gif", http.StatusUnsupportedMediaType},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != tc.code {
			t.Fatalf("%s status=%d body=%q", tc.path, res.Code, res.Body.String())
		}
	}
}

func TestAPIProgramPreviewFFmpegError(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := installFakeFFmpeg(t, "", 9)
	defer restore()
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "recorded", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recorded/recorded/preview.png", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("ffmpeg error status=%d body=%q", res.Code, res.Body.String())
	}
}

func installFakeFFmpeg(t *testing.T, output string, exitCode ...int) func() {
	t.Helper()
	code := 0
	if len(exitCode) > 0 {
		code = exitCode[0]
	}
	old := runFFmpegPreview
	runFFmpegPreview = func(context.Context, ...string) ([]byte, error) {
		if code != 0 {
			return nil, fmt.Errorf("fake ffmpeg exit %d", code)
		}
		return []byte(output), nil
	}
	return func() { runFFmpegPreview = old }
}

func installFakeFFmpegStream(t *testing.T, output string, gotInput *string, gotArgs *[]string) func() {
	t.Helper()
	old := runFFmpegStream
	runFFmpegStream = func(_ context.Context, input io.Reader, args ...string) (io.ReadCloser, func() error, error) {
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, nil, err
		}
		*gotInput = string(data)
		*gotArgs = append((*gotArgs)[:0], args...)
		return io.NopCloser(strings.NewReader(output)), func() error { return nil }, nil
	}
	return func() { runFFmpegStream = old }
}

func TestGrowingFileReaderFollowsAppends(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "recording.m2ts")
	if err := os.WriteFile(filePath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := newGrowingFileReader(ctx, filePath, 0)

	initial := make([]byte, 4)
	if _, err := io.ReadFull(reader, initial); err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if string(initial) != "live" {
		t.Fatalf("initial read = %q", initial)
	}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 6)
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			done <- err
			return
		}
		if string(buf) != "follow" {
			done <- fmt.Errorf("follow read = %q", buf)
			return
		}
		done <- nil
	}()
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("livefollow"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not follow appended data")
	}
}

func installFakeFFprobe(t *testing.T, output string, probeErr error) func() {
	t.Helper()
	old := runFFprobeFormat
	runFFprobeFormat = func(context.Context, string) ([]byte, error) {
		if probeErr != nil {
			return nil, probeErr
		}
		return []byte(output), nil
	}
	return func() { runFFprobeFormat = old }
}

func TestAPIRecordingWatchRequiresPID(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recording.m2ts")
	if err := os.WriteFile(recordedPath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "abc", Title: "Live", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recording/abc/watch.m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing pid status=%d body=%q", res.Code, res.Body.String())
	}

	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "abc", Title: "Live", Recorded: filepath.ToSlash(recordedPath), PID: 123}}, false); err != nil {
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

func TestAPIRecordingWatchMP4UsesGrowingLiveInput(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recording.m2ts")
	if err := os.WriteFile(recordedPath, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "abc", Title: "Live", Recorded: filepath.ToSlash(recordedPath), PID: 123}}, false); err != nil {
		t.Fatal(err)
	}
	old := runFFmpegStream
	var gotInput string
	var gotArgs []string
	runFFmpegStream = func(_ context.Context, input io.Reader, args ...string) (io.ReadCloser, func() error, error) {
		buf := make([]byte, 4)
		if _, err := io.ReadFull(input, buf); err != nil {
			return nil, nil, err
		}
		gotInput = string(buf)
		gotArgs = append(gotArgs[:0], args...)
		return io.NopCloser(strings.NewReader("mp4data")), func() error { return nil }, nil
	}
	defer func() { runFFmpegStream = old }()
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/recording/abc/watch.mp4?b:v=1m", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "mp4data" {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	if gotInput != "live" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-re -i pipe:0", "-b:v 1m", "-c:a aac"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("live recording ffmpeg args missing %q: %s", want, joined)
		}
	}
	for _, notWant := range []string{"-ss", "-bufsize:v", "-b:a 96k", "-bufsize:a"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("live recording ffmpeg args should not contain %q: %s", notWant, joined)
		}
	}
}

func TestAPIProgramWatchRejectsScramblingLikeLegacy(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	recordedPath := filepath.Join(dir, "recorded.m2ts")
	if err := os.WriteFile(recordedPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Recorded), 0o755); err != nil {
		t.Fatal(err)
	}
	recordedJSON := `[{"id":"recorded","title":"Scrambled","recorded":"` + filepath.ToSlash(recordedPath) + `","tuner":{"isScrambling":true}}]`
	if err := os.WriteFile(paths.Recorded, []byte(recordedJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	recordingJSON := `[{"id":"recording","title":"Scrambled","recorded":"` + filepath.ToSlash(recordedPath) + `","tuner":{"isScrambling":true}}]`
	if err := os.WriteFile(paths.Recording, []byte(recordingJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(paths, &config.Config{})

	req := httptest.NewRequest(http.MethodGet, "/api/recorded/recorded/watch.m2ts", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("recorded scrambling status=%d body=%q", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/recording/recording/watch.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("recording without pid status=%d body=%q", res.Code, res.Body.String())
	}

	recordingJSON = `[{"id":"recording","title":"Scrambled","recorded":"` + filepath.ToSlash(recordedPath) + `","pid":123,"tuner":{"isScrambling":true}}]`
	if err := os.WriteFile(paths.Recording, []byte(recordingJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/recording/recording/watch.m2ts", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("recording scrambling status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestAPIChannelLogoAndWatchProxyMirakurun(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []legacy.ChannelSchedule{{
		Channel: legacy.Channel{ID: chid, Name: "Service & One"},
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

func TestAPIChannelWatchMP4UsesMirakurunAndFFmpeg(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []legacy.ChannelSchedule{{Channel: legacy.Channel{ID: chid, Name: "Service"}}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	requests := []string{}
	mirakurunServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		if r.URL.Path == "/api/services/123/stream" {
			_, _ = w.Write([]byte("livets"))
			return
		}
		http.NotFound(w, r)
	}))
	defer mirakurunServer.Close()
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "livemp4", &gotInput, &gotArgs)
	defer restore()
	handler := NewHandler(paths, &config.Config{MirakurunPath: mirakurunServer.URL + "/"})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/"+chid+"/watch.mp4", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "livemp4" {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	if len(requests) != 1 || requests[0] != "/api/services/123/stream?decode=1" {
		t.Fatalf("mirakurun requests = %#v", requests)
	}
	if gotInput != "livets" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "-re -i pipe:0") {
		t.Fatalf("live ffmpeg args missing -re: %v", gotArgs)
	}
}

func TestAPIChannelWatchMP4KeepsLegacyLiveBitrateArgs(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []legacy.ChannelSchedule{{Channel: legacy.Channel{ID: chid, Name: "Service"}}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	mirakurunServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/services/123/stream" {
			_, _ = w.Write([]byte("livets"))
			return
		}
		http.NotFound(w, r)
	}))
	defer mirakurunServer.Close()
	var gotInput string
	var gotArgs []string
	restore := installFakeFFmpegStream(t, "livemp4", &gotInput, &gotArgs)
	defer restore()
	handler := NewHandler(paths, &config.Config{MirakurunPath: mirakurunServer.URL + "/"})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/"+chid+"/watch.mp4?b:v=1m", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("mp4 status=%d body=%q", res.Code, res.Body.String())
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-b:v 1m", "-minrate:v 1m", "-maxrate:v 1m", "-c:a aac"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("live ffmpeg args missing %q: %s", want, joined)
		}
	}
	for _, notWant := range []string{"-bufsize:v", "-b:a 96k", "-bufsize:a"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("live ffmpeg args should not contain %q: %s", notWant, joined)
		}
	}
	if gotInput != "livets" {
		t.Fatalf("ffmpeg input = %q", gotInput)
	}
}

func TestAPIChannelWatchXSPF(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	chid := strconv.FormatInt(123, 36)
	schedule := []legacy.ChannelSchedule{{
		Channel: legacy.Channel{ID: chid, Name: "Service & One"},
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
	if !strings.Contains(res.Body.String(), "Service & One") {
		t.Fatalf("xspf channel title should match legacy unescaped output: %q", res.Body.String())
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
	recordedInfo, err := os.Stat(recordedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recorded, []legacy.Program{{ID: "abc", Recorded: filepath.ToSlash(recordedPath)}}, false); err != nil {
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
	if usage["recorded"].(float64) != float64(allocatedFileSize(recordedInfo)) {
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
	schedule := []legacy.ChannelSchedule{{
		Programs: []legacy.Program{
			{ID: "aaa", Title: "Reserve"},
			{ID: "bbb", Title: "Conflict"},
		},
	}}
	if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
		t.Fatal(err)
	}
	logData := "old\nRUNNING SCHEDULER.\nRESERVE: aaa\n!CONFLICT: bbb\nRESERVE: missing\n"
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
	if !strings.Contains(res.Body.String(), "\n  ") {
		t.Fatalf("scheduler json should use legacy pretty JSON, got %q", res.Body.String())
	}
	var result struct {
		Time      int64            `json:"time"`
		Conflicts []legacy.Program `json:"conflicts"`
		Reserves  []legacy.Program `json:"reserves"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Time == 0 || len(result.Reserves) != 2 || result.Reserves[0].ID != "aaa" || result.Reserves[1].ID != "" || len(result.Conflicts) != 1 || result.Conflicts[0].ID != "bbb" {
		t.Fatalf("unexpected scheduler result: %#v", result)
	}
	if !strings.Contains(res.Body.String(), "null") {
		t.Fatalf("missing scheduler program should be preserved as null: %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/scheduler.txt", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != logData {
		t.Fatalf("scheduler txt status=%d body=%q", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("scheduler txt content-type=%q", got)
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
	if got := res.Body.String(); got != `{}` {
		t.Fatalf("scheduler force body=%q", got)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler force did not run")
	}
}

func TestParseSchedulerLogProgramUsesLegacyIDPattern(t *testing.T) {
	tests := []struct {
		line string
		kind string
		id   string
		ok   bool
	}{
		{"RESERVE: abc-123, title", "RESERVE", "abc-123", true},
		{"!CONFLICT: def456 (rule)", "CONFLICT", "def456", true},
		{"RESERVE: ABC", "", "", false},
		{"RESERVE: ", "", "", false},
	}
	for _, tt := range tests {
		kind, id, ok := parseSchedulerLogProgram(tt.line)
		if kind != tt.kind || id != tt.id || ok != tt.ok {
			t.Fatalf("%q => (%q, %q, %v), want (%q, %q, %v)", tt.line, kind, id, ok, tt.kind, tt.id, tt.ok)
		}
	}
}

func TestAPIStatusReadsPIDFiles(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.OperatorPID = filepath.Join(dir, "operator.pid")
	currentPID := os.Getpid()
	if err := os.WriteFile(paths.OperatorPID, []byte(strconv.Itoa(currentPID)+"\n"), 0o644); err != nil {
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
		WUI       map[string]any `json:"wui"`
		Feature   map[string]any `json:"feature"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Operator["pid"].(float64) != float64(currentPID) || status.Operator["alive"] != true {
		t.Fatalf("unexpected operator status: %#v", status.Operator)
	}
	if status.Scheduler != nil {
		t.Fatalf("legacy status should not expose scheduler: %#v", status.Scheduler)
	}
	if status.WUI["pid"] != nil || status.WUI["alive"] != false {
		t.Fatalf("unexpected legacy wui status: %#v", status.WUI)
	}
	if status.Feature["streamer"] != true || status.Feature["previewer"] != true || status.Feature["filer"] != true || status.Feature["configurator"] != true {
		t.Fatalf("unexpected feature flags: %#v", status.Feature)
	}
	if _, ok := status.Feature["goImplementation"]; ok {
		t.Fatalf("status feature should not expose Go-only flags: %#v", status.Feature)
	}
	if _, ok := status.Feature["partialCompatible"]; ok {
		t.Fatalf("status feature should not expose compatibility flags: %#v", status.Feature)
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
	if res.Body.String() != "401 Unauthorized\n" {
		t.Fatalf("unauthorized body = %q", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/api/schedule.json", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || res.Body.Len() != 0 {
		t.Fatalf("HEAD without auth status=%d body=%q", res.Code, res.Body.String())
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
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, "")
	cfg := &config.Config{
		WUITlsKeyPath:            keyPath,
		WUITlsCertPath:           certPath,
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
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("certificates = %d", len(tlsConfig.Certificates))
	}
}

func TestBuildTLSConfigEncryptedKeyPassphrase(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, "secret")
	cfg := &config.Config{
		WUITlsKeyPath:     keyPath,
		WUITlsCertPath:    certPath,
		WUITlsPassphrase:  "secret",
		WUITlsRequestCert: true,
	}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("certificates = %d", len(tlsConfig.Certificates))
	}
	if tlsConfig.ClientAuth != tls.RequestClientCert {
		t.Fatalf("client auth = %s", tlsConfig.ClientAuth)
	}
}

func TestBuildTLSConfigEncryptedKeyWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, "secret")
	cfg := &config.Config{
		WUITlsKeyPath:    keyPath,
		WUITlsCertPath:   certPath,
		WUITlsPassphrase: "wrong",
	}
	if _, err := buildTLSConfig(cfg); err == nil {
		t.Fatal("expected encrypted key passphrase error")
	}
}

func TestBuildTLSConfigCAError(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, "")
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		WUITlsKeyPath:  keyPath,
		WUITlsCertPath: certPath,
		WUITlsCaPath:   caPath,
	}
	if _, err := buildTLSConfig(cfg); err == nil {
		t.Fatal("expected CA parse error")
	}
}

func writeTestCertificate(t *testing.T, dir, passphrase string) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	keyBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}
	if passphrase != "" {
		keyBlock, err = x509.EncryptPEMBlock(rand.Reader, keyBlock.Type, keyDER, []byte(passphrase), x509.PEMCipherAES256)
		if err != nil {
			t.Fatal(err)
		}
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(keyBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
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

func TestAccessLogUsesLegacyMethodOverride(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths(dir)
	paths.LogDir = filepath.Join(dir, "log")
	handler := NewHandler(paths, &config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/status.json?method=GET", nil)
	req.RemoteAddr = "10.0.0.1:12345"
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
	if !strings.Contains(log, `200 GET:/api/status.json?method=GET 10.0.0.1`) {
		t.Fatalf("access log did not use legacy override method/url: %q", log)
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
		LogDir:    filepath.Join(dir, "log"),
	}
}
