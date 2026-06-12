# SSE Real-Time Stream Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task.

**Goal:** Replace 5-minute frontend polling with a Server-Sent Events stream so the dashboard updates in near-real-time as OBS build events arrive.

**Architecture:** A central `Hub` in `internal/hub` maintains a set of registered SSE client channels. After each store write, the MQ consumer and OBS poller call `hub.Notify(payload)` with a typed JSON message. A single `GET /api/stream` endpoint streams those messages to the browser. The Vue frontend opens one `EventSource` and patches the existing composable state on each delta; the 5-minute `setInterval` is removed.

**Tech Stack:** Go standard library (`net/http`, `sync`, `encoding/json`), Vue 3 Composition API, browser `EventSource` API. No new dependencies.

**User decisions (already made):**
- Server-to-client only — no client-to-server channel needed; SSE is therefore the right transport.
- Stream both package updates and new events (typed delta messages).
- On disconnect: `EventSource` reconnects automatically; on reconnect trigger one full HTTP refresh to catch up on missed updates.

---

## Scope

| File | Change |
|------|--------|
| `backend/internal/hub/hub.go` | New — Hub struct + Register/Unregister/Notify |
| `backend/internal/hub/message.go` | New — typed Msg + PackageUpdate/NewEvent helpers |
| `backend/internal/api/server.go` | Modify — accept `*hub.Hub`, add `GET /api/stream` route |
| `backend/cmd/obsboard/main.go` | Modify — construct Hub, pass to NewRouter/NewPoller/NewConsumer |
| `backend/internal/obs/poller.go` | Modify — accept `*hub.Hub`, call Notify after store writes |
| `backend/internal/mq/consumer.go` | Modify — accept `*hub.Hub`, call Notify after store writes |
| `frontend/src/composables/useRealtimeStream.ts` | New — EventSource lifecycle + delta dispatch |
| `frontend/src/App.vue` | Modify — remove setInterval, call useRealtimeStream |
| `frontend/src/components/ContextBar.vue` | Modify — update "Auto-refresh 5 min" label to "Live" |

No other files change. Existing REST endpoints are untouched.

---

## Backend

### Hub (`internal/hub/hub.go`)

```go
package hub

import "sync"

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

// Notify fans out payload to all registered clients.
// If a client's channel is full, the message is dropped for that client
// to avoid blocking the caller.
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

### Typed messages (`internal/hub/message.go`)

```go
package hub

import "encoding/json"
import "github.com/percona/obs-dashboard/internal/model"

type Msg struct {
    Type string          `json:"type"`
    Data json.RawMessage `json:"data"`
}

func PackageUpdate(pkg *model.Package) []byte {
    d, _ := json.Marshal(pkg)
    out, _ := json.Marshal(Msg{Type: "package_update", Data: d})
    return out
}

func NewEvent(evt *model.Event) []byte {
    d, _ := json.Marshal(evt)
    out, _ := json.Marshal(Msg{Type: "new_event", Data: d})
    return out
}
```

### SSE endpoint

Added to `NewRouter` as `r.Get("/api/stream", streamHandler(h))`:

```go
func streamHandler(h *hub.Hub) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("X-Accel-Buffering", "no")  // disable nginx buffering
        w.Header().Set("Connection", "keep-alive")

        ch := make(chan []byte, 16)
        h.Register(ch)
        defer h.Unregister(ch)

        flusher, ok := w.(http.Flusher)
        if !ok {
            http.Error(w, "streaming unsupported", http.StatusInternalServerError)
            return
        }

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

`NewRouter` signature changes from `func NewRouter(db *sql.DB) http.Handler` to `func NewRouter(db *sql.DB, h *hub.Hub) http.Handler`.

### Notify call sites

**`mq/consumer.go`:**
- `NewConsumer(url string, db *sql.DB)` → `NewConsumer(url string, db *sql.DB, h *hub.Hub)`, store as `c.hub`
- After `store.UpsertPackageState(c.db, pkg)`: `c.hub.Notify(hubmsg.PackageUpdate(pkg))`
- After each `store.AppendEvent(c.db, evt)`: `c.hub.Notify(hubmsg.NewEvent(evt))`

**`obs/poller.go`:**
- `NewPoller(client, db, interval)` → `NewPoller(client, db, interval, h *hub.Hub)`, store as `p.hub`
- After `store.UpsertPackageState(p.db, pkg)` at line 91: `p.hub.Notify(hubmsg.PackageUpdate(pkg))`
- After `store.AppendEvent(p.db, evt)` at line 97: `p.hub.Notify(hubmsg.NewEvent(evt))`

**`cmd/obsboard/main.go`:**
```go
h := hub.New()
poller := obs.NewPoller(obsClient, db, cfg.Poller.Interval, h)
consumer := mq.NewConsumer(cfg.MQ.URL, db, h)
router := api.NewRouter(db, h)
```

---

## Frontend

### Composable (`useRealtimeStream.ts`)

```ts
import { onMounted, onUnmounted, type Ref } from 'vue'
import type { Package, Event } from '../types/api'

type RefreshFn = () => void

export function useRealtimeStream(
  packages: Ref<Package[]>,
  events: Ref<Event[]>,
  refresh: RefreshFn,
) {
  let es: EventSource | null = null
  let wasError = false

  function connect() {
    es = new EventSource('/api/stream')

    es.onopen = () => {
      if (wasError) {
        wasError = false
        refresh()   // catch up on missed updates
      }
    }

    es.onmessage = (e) => {
      const msg = JSON.parse(e.data) as { type: string; data: unknown }
      if (msg.type === 'package_update') {
        const pkg = msg.data as Package
        const idx = packages.value.findIndex(
          p => p.project === pkg.project && p.name === pkg.name,
        )
        if (idx >= 0) packages.value[idx] = pkg
        else packages.value.push(pkg)
      } else if (msg.type === 'new_event') {
        events.value.unshift(msg.data as Event)
        if (events.value.length > 200) events.value.length = 200
      }
    }

    es.onerror = () => {
      wasError = true
      // EventSource reconnects automatically — no manual action needed.
    }
  }

  onMounted(connect)
  onUnmounted(() => es?.close())
}
```

### `App.vue` changes

Remove:
```ts
const intervalId = setInterval(refresh, 5 * 60 * 1000)
onUnmounted(() => clearInterval(intervalId))
```

Add:
```ts
import { useRealtimeStream } from './composables/useRealtimeStream'
// ...
useRealtimeStream(packages, events, refresh)
```

The `refresh` function already exists in `App.vue` and calls `refreshPackages()`, `refreshEvents()`, and `refreshPR()` in parallel.

### `ContextBar.vue` label change

Change the status indicator from:
```html
<span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Auto-refresh 5 min
```
to:
```html
<span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Live
```

---

## Error handling

- **Slow client:** Hub drops the message (non-blocking send). Client gets the next event when it catches up; the full-refresh-on-reconnect handles any gap if the client disconnects.
- **No connected clients:** `Notify` is a no-op (iterates an empty map).
- **Nginx / reverse proxy buffering:** `X-Accel-Buffering: no` header disables it.
- **Consumer/poller errors:** store write errors are already logged and returned before Notify is called, so Notify is only reached on success.

## What does not change

- All existing REST endpoints (`/api/products/...`, `/api/pr/...`) are untouched.
- `usePackages.ts` and `useEvents.ts` composables are untouched — `useRealtimeStream` mutates their `ref` state directly.
- Backend tests for existing handlers are unaffected.
- The `refresh()` call on initial mount in `App.vue` stays — it seeds state before the SSE stream connects.
