package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type spyBeater struct{ beats int }

func (s *spyBeater) Heartbeat() { s.beats++ }

func TestPresenceHandler(t *testing.T) {
	spy := &spyBeater{}
	rec := httptest.NewRecorder()
	presenceHandler(spy)(rec, httptest.NewRequest(http.MethodPost, "/api/presence", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if spy.beats != 1 {
		t.Fatalf("beats = %d, want 1", spy.beats)
	}
}
