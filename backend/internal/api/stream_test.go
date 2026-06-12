package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/hub"
)

func TestStreamHandler_DeliversEvent(t *testing.T) {
	h := hub.New()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		streamHandler(h)(rec, req)
		close(done)
	}()

	// Wait for the handler to register its channel with the hub.
	time.Sleep(20 * time.Millisecond)

	payload := []byte(`{"type":"package_update","data":{}}`)
	h.Notify(payload)

	// Wait for the handler to write and flush.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", ct, "text/event-stream")
	}
	if !strings.Contains(body, "data: ") {
		t.Errorf("body missing SSE data: prefix; got: %s", body)
	}
	if !strings.Contains(body, `"type":"package_update"`) {
		t.Errorf("body missing package_update; got: %s", body)
	}
}

func TestStreamHandler_CancelDisconnects(t *testing.T) {
	h := hub.New()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		streamHandler(h)(rec, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// correct — handler returned
	case <-time.After(time.Second):
		t.Fatal("handler did not return after context cancel")
	}
}
