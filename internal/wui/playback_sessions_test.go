package wui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlaybackRequestSessionStopsRegisteredRequests(t *testing.T) {
	manager := newPlaybackRequestSessions()
	token := "session_1234567890"
	first, releaseFirst := manager.register(context.Background(), token)
	defer releaseFirst()
	second, releaseSecond := manager.register(context.Background(), token)
	defer releaseSecond()

	if stopped := manager.stop(token); stopped != 2 {
		t.Fatalf("stopped requests = %d, want 2", stopped)
	}
	for index, ctx := range []context.Context{first, second} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("request %d was not cancelled", index)
		}
	}
}

func TestPlaybackRequestSessionRejectsInvalidToken(t *testing.T) {
	manager := newPlaybackRequestSessions()
	ctx, release := manager.register(context.Background(), "short")
	defer release()
	if stopped := manager.stop("short"); stopped != 0 {
		t.Fatalf("stopped requests = %d", stopped)
	}
	select {
	case <-ctx.Done():
		t.Fatal("invalid session token registered a cancellable request")
	default:
	}
}

func TestPlaybackRequestSessionWaitsForRegisteredRequestToFinish(t *testing.T) {
	manager := newPlaybackRequestSessions()
	token := "session_1234567890"
	registered, release := manager.register(context.Background(), token)
	stopped := make(chan int, 1)
	go func() {
		stopped <- manager.stopAndWait(context.Background(), token)
	}()

	select {
	case <-registered.Done():
	case <-time.After(time.Second):
		t.Fatal("stopAndWait did not cancel the request")
	}
	select {
	case <-stopped:
		t.Fatal("stopAndWait returned before the request finished")
	default:
	}

	release()
	select {
	case count := <-stopped:
		if count != 1 {
			t.Fatalf("stopped requests = %d, want 1", count)
		}
	case <-time.After(time.Second):
		t.Fatal("stopAndWait did not return after the request finished")
	}
}

func TestServerPlaybackDeleteCancelsMatchingRequest(t *testing.T) {
	server := &server{playbackRequests: newPlaybackRequestSessions()}
	token := "session_1234567890"
	request := httptest.NewRequest(http.MethodGet, "/api/channel/abc/watch.mp4?session="+token, nil)
	registered, release := server.beginPlaybackRequest(request)
	defer release()

	response := httptest.NewRecorder()
	stop := httptest.NewRequest(http.MethodDelete, "/api/channel/abc/watch.mp4?session="+token, nil)
	finished := make(chan struct{})
	go func() {
		server.stopPlaybackRequest(response, stop)
		close(finished)
	}()

	select {
	case <-registered.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("DELETE did not cancel the matching playback request")
	}
	release()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("DELETE did not finish after the playback request ended")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}
