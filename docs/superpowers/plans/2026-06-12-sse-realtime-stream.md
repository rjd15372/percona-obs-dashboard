# SSE Real-Time Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 5-minute polling interval with a Server-Sent Events stream so the dashboard updates in near-real-time as OBS build events arrive.

**Architecture:** A central `Hub` in `internal/hub` maintains registered SSE client channels. After each successful store write, the MQ consumer and OBS poller call `hub.Notify(payload)`. A `GET /api/stream` endpoint fans those payloads to all connected browsers. The Vue frontend replaces `setInterval` with `new EventSource('/api/stream')` and patches composable state on each delta.

**Tech Stack:** Go standard library (`net/http`, `sync`, `encoding/json`), Vue 3 Composition API + TypeScript, browser `EventSource` API. No new dependencies for either backend or frontend.

**User decisions (already made):**
- Server-to-client only — no client-to-server channel needed, so SSE is the right transport (not WebSocket).
- Stream both `package_update` and `new_event` typed delta messages.
- On disconnect: `EventSource` reconnects automatically; on reconnect, trigger one full HTTP refresh to catch up on missed updates.

---

## File Structure

| File | Change |
|------|--------|
| `backend/internal/hub/hub.go` | New — Hub struct with Register/Unregister/Notify |
| `backend/internal/hub/message.go` | New — Msg type + PackageUpdate/NewEvent helpers |
| `backend/internal/hub/hub_test.go` | New — unit tests for hub |
| `backend/internal/api/server.go` | Modify — add hub param, add `/api/stream` route + handler |
| `backend/internal/api/stream_test.go` | New — test for SSE handler |
| `backend/internal/mq/consumer.go` | Modify — add hub field, helper methods, Notify after writes |
| `backend/internal/obs/poller.go` | Modify — add hub field, Notify after writes |
| `backend/cmd/obsboard/main.go` | Modify — construct Hub, pass to NewRouter/NewPoller/NewConsumer |
| `frontend/src/composables/useRealtimeStream.ts` | New — EventSource lifecycle + delta dispatch |
| `frontend/src/App.vue` | Modify — remove setInterval, call useRealtimeStream |
| `frontend/src/components/ContextBar.vue` | Modify — update "Auto-refresh 5 min" label to "Live" |

---

### Task 1: Hub package with unit tests

**Goal:** Create `internal/hub` with the Hub struct, typed message helpers, and passing unit tests.

**Files:**
- Create: `backend/internal/hub/hub.go`
- Create: `backend/internal/hub/message.go`
- Create: `backend/internal/hub/hub_test.go`

**Acceptance Criteria:**
- [ ] `go test ./internal/hub/...` exits 0 with all tests passing
- [ ] `Hub.Notify` fans out to all registered channels
- [ ] `Hub.Notify` drops the message for a full/slow client without blocking
- [ ] `Hub.Unregister` stops delivery to that channel
- [ ] `PackageUpdate` and `NewEvent` produce valid JSON with `"type"` and `"data"` fields

**Verify:** `cd backend && go test ./internal/hub/... -v` → `PASS` for all three test functions

**Steps:**

- [ ] **Step 1: Create `backend/internal/hub/hub.go`**

  ```go
  package hub

  import "sync"

  // Hub fans out SSE payloads to all registered clients.
  type Hub struct {
  	mu      sync.RWMutex
  	clients map[chan<- []byte]struct{}
  }

  func New() *Hub { return &Hub{clients: make(map[chan<- []byte]struct{})} }

  func (h *Hub) Register(ch chan<- []byte) {
  	h.mu.Lock()
  	h.clients[ch] = struct{}{}
  	h.mu.Unlock()
  }

  func (h *Hub) Unregister(ch chan<- []byte) {
  	h.mu.Lock()
  	delete(h.clients, ch)
  	h.mu.Unlock()
  }

  // Notify sends payload to every registered client.
  // If a client's channel buffer is full the message is dropped for that
  // client — the non-blocking select prevents Notify from stalling callers.
  func (h *Hub) Notify(payload []byte) {
  	h.mu.RLock()
  	defer h.mu.RUnlock()
  	for ch := range h.clients {
  		select {
  		case ch <- payload:
  		default:
  		}
  	}
  }
  ```

- [ ] **Step 2: Create `backend/internal/hub/message.go`**

  ```go
  package hub

  import (
  	"encoding/json"

  	"github.com/percona/obs-dashboard/internal/model"
  )

  // Msg is the typed envelope sent over the SSE stream.
  type Msg struct {
  	Type string          `json:"type"`
  	Data json.RawMessage `json:"data"`
  }

  // PackageUpdate serialises a package delta for the SSE stream.
  func PackageUpdate(pkg *model.Package) []byte {
  	d, _ := json.Marshal(pkg)
  	out, _ := json.Marshal(Msg{Type: "package_update", Data: d})
  	return out
  }

  // NewEvent serialises an event delta for the SSE stream.
  func NewEvent(evt *model.Event) []byte {
  	d, _ := json.Marshal(evt)
  	out, _ := json.Marshal(Msg{Type: "new_event", Data: d})
  	return out
  }
  ```

- [ ] **Step 3: Create `backend/internal/hub/hub_test.go`**

  ```go
  package hub_test

  import (
  	"testing"

  	"github.com/percona/obs-dashboard/internal/hub"
  )

  func TestHub_NotifyFansOut(t *testing.T) {
  	h := hub.New()
  	ch1 := make(chan []byte, 1)
  	ch2 := make(chan []byte, 1)
  	h.Register((chan<- []byte)(ch1))
  	h.Register((chan<- []byte)(ch2))

  	msg := []byte(`{"type":"test"}`)
  	h.Notify(msg)

  	if got := <-ch1; string(got) != string(msg) {
  		t.Errorf("ch1: got %s, want %s", got, msg)
  	}
  	if got := <-ch2; string(got) != string(msg) {
  		t.Errorf("ch2: got %s, want %s", got, msg)
  	}
  }

  func TestHub_UnregisterStopsDelivery(t *testing.T) {
  	h := hub.New()
  	ch := make(chan []byte, 1)
  	h.Register((chan<- []byte)(ch))
  	h.Unregister((chan<- []byte)(ch))

  	h.Notify([]byte(`{"type":"test"}`))

  	select {
  	case got := <-ch:
  		t.Errorf("received message after unregister: %s", got)
  	default:
  		// correct — nothing delivered
  	}
  }

  func TestHub_SlowClientDoesNotBlock(t *testing.T) {
  	h := hub.New()
  	// unbuffered — a blocking send would deadlock
  	slow := make(chan []byte)
  	fast := make(chan []byte, 1)
  	h.Register((chan<- []byte)(slow))
  	h.Register((chan<- []byte)(fast))

  	done := make(chan struct{})
  	go func() {
  		h.Notify([]byte(`{"type":"test"}`))
  		close(done)
  	}()
  	<-done // must return without blocking on slow

  	if len(fast) != 1 {
  		t.Error("fast client did not receive the message")
  	}
  	if len(slow) != 0 {
  		t.Error("slow client should have been dropped, not buffered")
  	}
  }
  ```

- [ ] **Step 4: Run the tests**

  ```bash
  cd backend && go test ./internal/hub/... -v
  ```

  Expected output:
  ```
  --- PASS: TestHub_NotifyFansOut (0.00s)
  --- PASS: TestHub_UnregisterStopsDelivery (0.00s)
  --- PASS: TestHub_SlowClientDoesNotBlock (0.00s)
  PASS
  ```

- [ ] **Step 5: Commit**

  ```bash
  git add backend/internal/hub/
  git commit -s -m "feat(backend): add SSE hub package with fan-out and typed message helpers"
  ```

---

### Task 2: SSE stream handler in API server

**Goal:** Add `GET /api/stream` to the chi router that upgrades the connection to SSE and streams hub payloads.

**Files:**
- Modify: `backend/internal/api/server.go`
- Create: `backend/internal/api/stream_test.go`

**Acceptance Criteria:**
- [ ] `go test ./internal/api/... -run TestStreamHandler` exits 0
- [ ] `GET /api/stream` response has `Content-Type: text/event-stream`
- [ ] A payload sent via `hub.Notify` appears in the response body as `data: <payload>\n\n`
- [ ] The handler returns when the request context is cancelled
- [ ] `NewRouter` signature is `func NewRouter(db *sql.DB, h *hub.Hub) http.Handler`

**Verify:** `cd backend && go test ./internal/api/... -run TestStreamHandler -v` → `PASS`

**Steps:**

- [ ] **Step 1: Replace `backend/internal/api/server.go`**

  Full new content:

  ```go
  package api

  import (
  	"database/sql"
  	"fmt"
  	"net/http"

  	"github.com/go-chi/chi/v5"
  	"github.com/go-chi/chi/v5/middleware"
  	"github.com/percona/obs-dashboard/internal/hub"
  )

  // NewRouter creates the chi router with all API routes registered.
  func NewRouter(db *sql.DB, h *hub.Hub) http.Handler {
  	r := chi.NewRouter()
  	r.Use(middleware.Logger)
  	r.Use(middleware.Recoverer)

  	r.Route("/api/products/{product}/{version}", func(r chi.Router) {
  		r.Get("/packages", packagesHandler(db))
  		r.Get("/events", eventsHandler(db))
  	})

  	r.Get("/api/pr/packages", prPackagesHandler(db))

  	r.Route("/api/pr/{pr}/{subproject}/{version}", func(r chi.Router) {
  		r.Get("/packages", prContextPackagesHandler(db))
  		r.Get("/events", prContextEventsHandler(db))
  	})

  	r.Get("/api/stream", streamHandler(h))

  	return r
  }

  func streamHandler(h *hub.Hub) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		w.Header().Set("Content-Type", "text/event-stream")
  		w.Header().Set("Cache-Control", "no-cache")
  		w.Header().Set("X-Accel-Buffering", "no")
  		w.Header().Set("Connection", "keep-alive")

  		flusher, ok := w.(http.Flusher)
  		if !ok {
  			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
  			return
  		}

  		ch := make(chan []byte, 16)
  		h.Register((chan<- []byte)(ch))
  		defer h.Unregister((chan<- []byte)(ch))

  		for {
  			select {
  			case <-r.Context().Done():
  				return
  			case payload := <-ch:
  				fmt.Fprintf(w, "data: %s\n\n", payload)
  				flusher.Flush()
  			}
  		}
  	}
  }
  ```

- [ ] **Step 2: Create `backend/internal/api/stream_test.go`**

  ```go
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
  ```

- [ ] **Step 3: Run the tests**

  ```bash
  cd backend && go test ./internal/api/... -run TestStreamHandler -v
  ```

  Expected:
  ```
  --- PASS: TestStreamHandler_DeliversEvent (0.04s)
  --- PASS: TestStreamHandler_CancelDisconnects (0.02s)
  PASS
  ```

- [ ] **Step 4: Commit**

  ```bash
  git add backend/internal/api/server.go backend/internal/api/stream_test.go
  git commit -s -m "feat(backend): add GET /api/stream SSE endpoint"
  ```

---

### Task 3: Wire MQ consumer to hub

**Goal:** Modify `mq/consumer.go` to accept a `*hub.Hub` and notify it after each successful store write.

**Files:**
- Modify: `backend/internal/mq/consumer.go`

**Acceptance Criteria:**
- [ ] `go build ./internal/mq/...` exits 0
- [ ] `Consumer` struct has a `hub *hub.Hub` field
- [ ] `NewConsumer` signature is `func NewConsumer(url string, db *sql.DB, h *hub.Hub) *Consumer`
- [ ] A helper `appendEvent(evt)` calls `hub.Notify` after a successful `store.AppendEvent`
- [ ] A helper `upsertPackage(pkg)` calls `hub.Notify` after a successful `store.UpsertPackageState`
- [ ] All existing `store.AppendEvent` + error-log patterns replaced with `c.appendEvent`
- [ ] The `store.UpsertPackageState` call in the build event branch replaced with `c.upsertPackage`

**Verify:** `cd backend && go build ./internal/mq/...` → exits 0 (no output)

**Steps:**

- [ ] **Step 1: Add hub field and update constructor**

  In `backend/internal/mq/consumer.go`, change the `Consumer` struct and `NewConsumer`:

  ```go
  // Consumer subscribes to the OBS AMQP bus and updates the store on build events.
  type Consumer struct {
  	url string
  	db  *sql.DB
  	hub *hubpkg.Hub
  }

  func NewConsumer(url string, db *sql.DB, h *hubpkg.Hub) *Consumer {
  	return &Consumer{url: url, db: db, hub: h}
  }
  ```

  Add the import alias at the top of the import block:

  ```go
  hubpkg "github.com/percona/obs-dashboard/internal/hub"
  ```

- [ ] **Step 2: Add helper methods after the `NewConsumer` function**

  ```go
  // appendEvent writes evt to the store and notifies SSE clients.
  func (c *Consumer) appendEvent(evt *model.Event) {
  	if err := store.AppendEvent(c.db, evt); err != nil {
  		slog.Error("mq: append event", "err", err)
  		return
  	}
  	c.hub.Notify(hubpkg.NewEvent(evt))
  }

  // upsertPackage writes pkg to the store and notifies SSE clients.
  // Returns the store error (if any) so callers can handle it.
  func (c *Consumer) upsertPackage(pkg *model.Package) error {
  	if err := store.UpsertPackageState(c.db, pkg); err != nil {
  		return err
  	}
  	c.hub.Notify(hubpkg.PackageUpdate(pkg))
  	return nil
  }
  ```

- [ ] **Step 3: Replace all `store.AppendEvent` call sites in `handle()`**

  There are 8 `store.AppendEvent` call sites. Replace each pattern:

  ```go
  // BEFORE (same pattern repeated 8 times, with varying slog message text):
  if err := store.AppendEvent(c.db, evt); err != nil {
      slog.Error("mq: append event", "err", err)
  }

  // AFTER:
  c.appendEvent(evt)
  ```

  The 8 locations are:
  1. `repoRouteKey` branch (~line 176)
  2. `repoBuildStartedKey` branch (~line 193)
  3. `repoBuildFinishedKey` branch (~line 209)
  4. `opensuse.obs.project.create` branch (~line 226)
  5. `opensuse.obs.project.delete` branch (~line 243)
  6. `opensuse.obs.package.version_change` branch (~line 260)
  7. `build_unchanged` early-return branch (~line 282)
  8. The package build event branch (~line 312) — the one after `UpsertPackageState`

- [ ] **Step 4: Replace the `store.UpsertPackageState` call in the build event branch**

  Find the block (~line 293–314):

  ```go
  // BEFORE:
  pkg := c.mergePackageTarget(m, scope, rollup)

  if err := store.UpsertPackageState(c.db, pkg); err != nil {
      slog.Error("mq: upsert package", "err", err)
      return
  }
  evt := &model.Event{ ... }
  if err := store.AppendEvent(c.db, evt); err != nil {
      slog.Error("mq: append event", "err", err)
  }

  // AFTER:
  pkg := c.mergePackageTarget(m, scope, rollup)

  if err := c.upsertPackage(pkg); err != nil {
      slog.Error("mq: upsert package", "err", err)
      return
  }
  evt := &model.Event{ ... }
  c.appendEvent(evt)
  ```

- [ ] **Step 5: Verify it compiles**

  ```bash
  cd backend && go build ./internal/mq/...
  ```

  Expected: exits 0, no output.

- [ ] **Step 6: Commit**

  ```bash
  git add backend/internal/mq/consumer.go
  git commit -s -m "feat(backend): wire MQ consumer to SSE hub"
  ```

---

### Task 4: Wire OBS poller to hub

**Goal:** Modify `obs/poller.go` to accept a `*hub.Hub` and notify it after each successful store write.

**Files:**
- Modify: `backend/internal/obs/poller.go`

**Acceptance Criteria:**
- [ ] `go build ./internal/obs/...` exits 0
- [ ] `Poller` struct has a `hub *hub.Hub` field
- [ ] `NewPoller` signature is `func NewPoller(client *Client, db *sql.DB, interval time.Duration, h *hub.Hub) *Poller`
- [ ] After successful `UpsertPackageState` in `tick()`, `p.hub.Notify(hubpkg.PackageUpdate(pkg))` is called
- [ ] After successful `AppendEvent` in `tick()`, `p.hub.Notify(hubpkg.NewEvent(evt))` is called

**Verify:** `cd backend && go build ./internal/obs/...` → exits 0 (no output)

**Steps:**

- [ ] **Step 1: Add hub field and update `NewPoller`**

  In `backend/internal/obs/poller.go`, update the struct and constructor:

  ```go
  // Poller periodically fetches OBS build results and reconciles them with the store.
  type Poller struct {
  	client   *Client
  	db       *sql.DB
  	interval time.Duration
  	root     string
  	hub      *hubpkg.Hub
  }

  func NewPoller(client *Client, db *sql.DB, interval time.Duration, h *hubpkg.Hub) *Poller {
  	return &Poller{client: client, db: db, interval: interval, root: "isv:percona", hub: h}
  }
  ```

  Add the import alias:

  ```go
  hubpkg "github.com/percona/obs-dashboard/internal/hub"
  ```

- [ ] **Step 2: Add Notify calls in `tick()` after the two store writes**

  The relevant block in `tick()` currently (lines ~91–100):

  ```go
  // BEFORE:
  if err := store.UpsertPackageState(p.db, pkg); err != nil {
      slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
      continue
  }
  if rollupChanged {
      evt := stateChangeEvent(pkg, prev)
      if err := store.AppendEvent(p.db, evt); err != nil {
          slog.Error("poller: append event", "err", err)
      }
  }

  // AFTER:
  if err := store.UpsertPackageState(p.db, pkg); err != nil {
      slog.Error("poller: upsert package", "pkg", pkgName, "err", err)
      continue
  }
  p.hub.Notify(hubpkg.PackageUpdate(pkg))
  if rollupChanged {
      evt := stateChangeEvent(pkg, prev)
      if err := store.AppendEvent(p.db, evt); err != nil {
          slog.Error("poller: append event", "err", err)
      } else {
          p.hub.Notify(hubpkg.NewEvent(evt))
      }
  }
  ```

- [ ] **Step 3: Verify it compiles**

  ```bash
  cd backend && go build ./internal/obs/...
  ```

  Expected: exits 0, no output.

- [ ] **Step 4: Commit**

  ```bash
  git add backend/internal/obs/poller.go
  git commit -s -m "feat(backend): wire OBS poller to SSE hub"
  ```

---

### Task 5: Wire hub into main and verify full backend build

**Goal:** Construct the Hub in `main.go`, pass it to NewRouter/NewPoller/NewConsumer, and verify the full backend builds and tests pass.

**Files:**
- Modify: `backend/cmd/obsboard/main.go`

**Acceptance Criteria:**
- [ ] `go build ./...` from `backend/` exits 0
- [ ] `go test ./...` from `backend/` exits 0 (all existing + new tests pass)
- [ ] `main.go` imports `internal/hub` and constructs `hub.New()` before the poller/consumer/router

**Verify:** `cd backend && go build ./... && go test ./...` → exits 0

**Steps:**

- [ ] **Step 1: Update `backend/cmd/obsboard/main.go`**

  Add the hub import and construct it before the poller/consumer/router lines. The current `run()` function has:

  ```go
  // BEFORE:
  obsClient := obs.NewClient(cfg.OBS.BaseURL, cfg.OBS.Username, cfg.OBS.Password)
  poller := obs.NewPoller(obsClient, db, cfg.Poller.Interval)
  consumer := mq.NewConsumer(cfg.MQ.URL, db)
  ...
  router := api.NewRouter(db)

  // AFTER:
  obsClient := obs.NewClient(cfg.OBS.BaseURL, cfg.OBS.Username, cfg.OBS.Password)
  h := hub.New()
  poller := obs.NewPoller(obsClient, db, cfg.Poller.Interval, h)
  consumer := mq.NewConsumer(cfg.MQ.URL, db, h)
  ...
  router := api.NewRouter(db, h)
  ```

  Add to the import block:

  ```go
  "github.com/percona/obs-dashboard/internal/hub"
  ```

- [ ] **Step 2: Build and test**

  ```bash
  cd backend && go build ./... && go test ./...
  ```

  Expected: exits 0. All tests pass including the new hub and stream tests.

- [ ] **Step 3: Commit**

  ```bash
  git add backend/cmd/obsboard/main.go
  git commit -s -m "feat(backend): wire hub into main — backend SSE complete"
  ```

---

### Task 6: Frontend realtime stream composable

**Goal:** Create `useRealtimeStream.ts` that opens an `EventSource`, dispatches typed deltas to the composable state refs, and triggers a full refresh on reconnect.

**Files:**
- Create: `frontend/src/composables/useRealtimeStream.ts`

**Acceptance Criteria:**
- [ ] `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` exits 0
- [ ] `package_update` messages upsert into the packages ref (update by project+name, or append if new)
- [ ] `new_event` messages prepend into the events ref and cap at 200 entries
- [ ] A full refresh is triggered once on the first reconnect after a connection error
- [ ] The `EventSource` is closed on component unmount

**Verify:** `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` → exits 0, no output

**Steps:**

- [ ] **Step 1: Create `frontend/src/composables/useRealtimeStream.ts`**

  ```ts
  import { onMounted, onUnmounted, type Ref } from 'vue'
  import type { Package, Event } from '../types/api'

  export function useRealtimeStream(
    packages: Ref<Package[]>,
    events: Ref<Event[]>,
    refresh: () => void,
  ): void {
    let es: EventSource | null = null
    let wasError = false

    function connect(): void {
      es = new EventSource('/api/stream')

      es.onopen = (): void => {
        if (wasError) {
          wasError = false
          refresh()
        }
      }

      es.onmessage = (e: MessageEvent): void => {
        const msg = JSON.parse(e.data as string) as { type: string; data: unknown }

        if (msg.type === 'package_update') {
          const pkg = msg.data as Package
          const idx = packages.value.findIndex(
            (p) => p.project === pkg.project && p.name === pkg.name,
          )
          if (idx >= 0) {
            packages.value[idx] = pkg
          } else {
            packages.value.push(pkg)
          }
        } else if (msg.type === 'new_event') {
          events.value.unshift(msg.data as Event)
          if (events.value.length > 200) {
            events.value.length = 200
          }
        }
      }

      es.onerror = (): void => {
        wasError = true
        // EventSource reconnects automatically — no manual action needed.
      }
    }

    onMounted(connect)
    onUnmounted((): void => {
      es?.close()
    })
  }
  ```

- [ ] **Step 2: Run the type check**

  ```bash
  cd frontend && ./node_modules/.bin/vue-tsc --noEmit
  ```

  Expected: exits 0, no output.

- [ ] **Step 3: Commit**

  ```bash
  git add frontend/src/composables/useRealtimeStream.ts
  git commit -s -m "feat(frontend): add useRealtimeStream composable for SSE delta dispatch"
  ```

---

### Task 7: Wire frontend — replace polling with SSE stream

**Goal:** Remove the 5-minute `setInterval` from `App.vue`, call `useRealtimeStream`, and update the ContextBar status label to "Live".

**Files:**
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/components/ContextBar.vue`

**Acceptance Criteria:**
- [ ] `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` exits 0
- [ ] `App.vue` no longer contains `setInterval` or `clearInterval`
- [ ] `App.vue` imports and calls `useRealtimeStream(allPackages, events, refresh)`
- [ ] The initial `refresh()` call on mount is preserved
- [ ] `ContextBar.vue` shows "Live" instead of "Auto-refresh 5 min"

**Verify:** `cd frontend && ./node_modules/.bin/vue-tsc --noEmit` → exits 0, no output

**Steps:**

- [ ] **Step 1: Update `frontend/src/App.vue`**

  Add the import at the top of the `<script setup>` block, alongside the other composable imports:

  ```ts
  import { useRealtimeStream } from './composables/useRealtimeStream'
  ```

  Replace the polling block:

  ```ts
  // REMOVE these three lines:
  let timer: ReturnType<typeof setInterval>
  onMounted(() => {
    refresh()
    timer = setInterval(refresh, 5 * 60 * 1000)
  })
  onUnmounted(() => clearInterval(timer))

  // REPLACE WITH:
  onMounted(() => refresh())
  useRealtimeStream(allPackages, events, refresh)
  ```

  Note: `allPackages` is the `data` ref returned by `usePackages` (line 64), and `events` is the `data` ref returned by `useEvents` (line 65). The `refresh` function is defined at line 111.

  Also remove `onUnmounted` from the `vue` import if it is no longer used elsewhere in the file. Check the import line:

  ```ts
  // BEFORE:
  import { ref, computed, onMounted, onUnmounted, watch } from 'vue'

  // AFTER (remove onUnmounted):
  import { ref, computed, onMounted, watch } from 'vue'
  ```

- [ ] **Step 2: Update `frontend/src/components/ContextBar.vue`**

  Find the status indicator span at the bottom of the top row div (~line 98):

  ```html
  <!-- BEFORE: -->
  <span style="display: inline-flex; align-items: center; gap: 6px;">
    <span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Auto-refresh 5 min
  </span>

  <!-- AFTER: -->
  <span style="display: inline-flex; align-items: center; gap: 6px;">
    <span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Live
  </span>
  ```

- [ ] **Step 3: Run the type check**

  ```bash
  cd frontend && ./node_modules/.bin/vue-tsc --noEmit
  ```

  Expected: exits 0, no output.

- [ ] **Step 4: Commit**

  ```bash
  git add frontend/src/App.vue frontend/src/components/ContextBar.vue
  git commit -s -m "feat(frontend): replace 5-min polling with SSE realtime stream"
  ```
