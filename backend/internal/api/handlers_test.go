package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/store"
)

func setupTestServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRouter(db, hub.New())
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
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/packages", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events?window=60", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/ppg/17/events?window=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
