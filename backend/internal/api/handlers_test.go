package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

// stubOBSServer returns a test HTTP server that replies with an empty OBS
// _result?view=versrel XML response for any request. Used so that releases
// handlers (which require an OBS client) return empty data rather than 503.
func stubOBSServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<resultlist state=""></resultlist>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupTestServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	obsSrv := stubOBSServer(t)
	obsClient := obs.NewClient(obsSrv.URL, "user", "pass")
	return NewRouter(db, hub.New(), obsClient, "isv:percona")
}

func TestPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Must be a JSON array (not null).
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_WindowParam(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/17/events?window=1440", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_DateRangeParam(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/17/events?from=2026-01-01&to=2026-12-31", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_DefaultWindow(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/17/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_InvalidWindow(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/17/events?window=notanumber", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPRContextPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}
	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}
	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_WindowParam(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events?window=60", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_InvalidWindow(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events?window=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestReleasesPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestReleasesReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}

func TestPRReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}

func TestRebuildHandler_Success(t *testing.T) {
	obsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer obsSrv.Close()

	obsClient := obs.NewClient(obsSrv.URL, "user", "pass")
	handler := rebuildHandler(obsClient)

	body := `{"project":"isv:percona:ppg:17","repo":"RockyLinux_9","arch":"x86_64","package":"percona-pg_tde"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp)
	}
}

func TestRebuildHandler_MissingField(t *testing.T) {
	obsClient := obs.NewClient("http://example.com", "user", "pass")
	handler := rebuildHandler(obsClient)

	// missing arch and package
	body := `{"project":"isv:percona:ppg:17","repo":"RockyLinux_9"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRebuildHandler_OBSError(t *testing.T) {
	obsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "permission denied", http.StatusForbidden)
	}))
	defer obsSrv.Close()

	obsClient := obs.NewClient(obsSrv.URL, "user", "pass")
	handler := rebuildHandler(obsClient)

	body := `{"project":"isv:percona:ppg:17","repo":"RockyLinux_9","arch":"x86_64","package":"percona-pg_tde"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRebuildHandler_InvalidJSON(t *testing.T) {
	obsClient := obs.NewClient("http://example.com", "user", "pass")
	handler := rebuildHandler(obsClient)

	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}
