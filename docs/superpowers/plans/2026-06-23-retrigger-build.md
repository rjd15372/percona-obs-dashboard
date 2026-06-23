# Retrigger Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a restart icon to PackageCard target rows so users can trigger an OBS rebuild from the dashboard UI.

**Architecture:** A new `POST /api/rebuild` endpoint proxies the OBS cmd=rebuild API using existing Basic Auth credentials. A new `useRebuild` Vue composable tracks per-target loading/error state keyed by `"repo/arch"`. PackageCard.vue imports the composable and renders a restart icon (with spinner while in-flight) for `failed/broken/unresolvable/blocked` targets.

**Tech Stack:** Go (chi router, net/http), Vue 3 (Composition API, ref, Map), TypeScript

**User decisions (already made):**
- Show restart icon only for: `failed`, `broken`, `unresolvable`, `blocked` states
- No confirmation dialog before triggering
- Spinner replaces icon while request is in-flight; inline red error text below the row on failure
- Error auto-clears after 4 seconds (driven by the composable)
- Same OBS credentials used for polling work for write operations too
- Composable approach (`useRebuild.ts`) rather than inline component logic

---

## File Map

| File | Change |
|------|--------|
| `backend/internal/obs/client.go` | Add `post()` private helper + `Rebuild()` public method |
| `backend/internal/obs/client_test.go` | Add `TestRebuild_*` tests |
| `backend/internal/api/handlers.go` | Add `rebuildHandler()` |
| `backend/internal/api/handlers_test.go` | Add `TestRebuildHandler_*` tests |
| `backend/internal/api/server.go` | Register `r.Post("/api/rebuild", rebuildHandler(obsClient))` |
| `frontend/src/composables/useRebuild.ts` | New file — per-target loading/error state + `trigger()` |
| `frontend/src/components/PackageCard.vue` | Import composable; add restart button + error row to target rows |

---

### Task 1: OBS client `Rebuild()` method

**Goal:** Add `post()` and `Rebuild()` to `backend/internal/obs/client.go`, tested against a stub HTTP server.

**Files:**
- Modify: `backend/internal/obs/client.go` (after the existing `getFile()` method, ~line 69)
- Modify: `backend/internal/obs/client_test.go` (append new tests)

**Acceptance Criteria:**
- [ ] `Rebuild()` issues `POST /build/<project-path-escaped>?cmd=rebuild&repository=<repo>&arch=<arch>&package=<pkg>` with Basic Auth
- [ ] Returns `nil` on 2xx response
- [ ] Returns an error containing the response body on non-2xx
- [ ] Special characters in project name are path-escaped (`isv:percona:ppg:17` → `isv%3Apercona%3Appg%3A17`)
- [ ] All three tests pass: success, error, URL encoding

**Verify:** `cd backend && go test ./internal/obs/ -run TestRebuild -v` → PASS (3 tests)

**Steps:**

- [ ] **Step 1: Add `post()` and `Rebuild()` to `backend/internal/obs/client.go`**

Insert the following immediately after the `getFile()` method (after line 68, before the `// --- XML response types ---` comment):

```go
// post issues an authenticated POST request to path with no request body.
// Returns nil on 2xx; returns an error with up to 512 bytes of the response body on non-2xx.
func (c *Client) post(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// Rebuild triggers a rebuild of a specific package target on OBS.
// project is path-escaped; repo, arch, and pkg are query-escaped.
func (c *Client) Rebuild(ctx context.Context, project, repo, arch, pkg string) error {
	path := fmt.Sprintf("/build/%s?cmd=rebuild&repository=%s&arch=%s&package=%s",
		url.PathEscape(project),
		url.QueryEscape(repo),
		url.QueryEscape(arch),
		url.QueryEscape(pkg),
	)
	return c.post(ctx, path)
}
```

- [ ] **Step 2: Write tests in `backend/internal/obs/client_test.go`**

Append to the end of the file:

```go
func TestRebuild_Success(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	err := c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if !strings.Contains(gotPath, "cmd=rebuild") {
		t.Errorf("expected cmd=rebuild in URL, got %s", gotPath)
	}
}

func TestRebuild_NonSuccessResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "permission denied", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	err := c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

func TestRebuild_URLEncoding(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	_ = c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")

	// project name colons must be path-escaped in the URL path segment
	if !strings.Contains(capturedURL, "isv%3Apercona%3Appg%3A17") {
		t.Errorf("project not path-escaped in URL: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "repository=RockyLinux_9") {
		t.Errorf("repo missing from URL: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "package=percona-pg_tde") {
		t.Errorf("package missing from URL: %s", capturedURL)
	}
}
```

Note: `client_test.go` is `package obs` (not `package obs_test`), so these tests have direct access to all exported and unexported symbols. The file already imports `context`, `net/http`, `net/http/httptest`, and `testing`. You need to add `"strings"` to the import block.

- [ ] **Step 3: Run the tests**

```bash
cd backend && go test ./internal/obs/ -run TestRebuild -v
```

Expected: 3 tests pass.

- [ ] **Step 4: Run the full obs package tests to check for regressions**

```bash
cd backend && go test ./internal/obs/ -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
cd backend && git add internal/obs/client.go internal/obs/client_test.go
git commit -s -m "feat(obs): add Rebuild() to OBS client"
```

---

### Task 2: `rebuildHandler` and route

**Goal:** Add `POST /api/rebuild` to the backend API, tested for success, missing fields, and OBS errors.

**Files:**
- Modify: `backend/internal/api/handlers.go` (append at end of file)
- Modify: `backend/internal/api/handlers_test.go` (append new tests)
- Modify: `backend/internal/api/server.go` (line 44 — add after existing `r.Get("/api/binaries", ...)`)

**Acceptance Criteria:**
- [ ] `POST /api/rebuild` with valid JSON body calls `obsClient.Rebuild()` and returns `{"status":"ok"}` (200)
- [ ] Missing any of `project`, `repo`, `arch`, `package` returns HTTP 400
- [ ] Invalid JSON body returns HTTP 400
- [ ] When `obsClient.Rebuild()` returns an error, handler returns HTTP 502 with the error message
- [ ] Route is registered in the router

**Verify:** `cd backend && go test ./internal/api/ -run TestRebuild -v` → PASS (3 tests)

**Steps:**

- [ ] **Step 1: Add `rebuildHandler` to `backend/internal/api/handlers.go`**

Append at the very end of the file:

```go
// rebuildHandler returns a handler for POST /api/rebuild.
// Decodes {"project","repo","arch","package"} JSON body and triggers an OBS rebuild.
func rebuildHandler(obsClient *obs.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Project string `json:"project"`
			Repo    string `json:"repo"`
			Arch    string `json:"arch"`
			Package string `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Project == "" || body.Repo == "" || body.Arch == "" || body.Package == "" {
			http.Error(w, "project, repo, arch, package are required", http.StatusBadRequest)
			return
		}
		if err := obsClient.Rebuild(r.Context(), body.Project, body.Repo, body.Arch, body.Package); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
```

- [ ] **Step 2: Register the route in `backend/internal/api/server.go`**

After line 44 (`r.Get("/api/binaries", binariesHandler(obsClient))`), add:

```go
r.Post("/api/rebuild", rebuildHandler(obsClient))
```

The block becomes:
```go
r.Get("/api/stream", streamHandler(h))
r.Get("/api/binaries", binariesHandler(obsClient))
r.Post("/api/rebuild", rebuildHandler(obsClient))
r.Post("/api/artifacts/metadata", artifactMetadataHandler(obsClient, metadataCache))
```

- [ ] **Step 3: Add tests in `backend/internal/api/handlers_test.go`**

Append at the end of the file. The test file is `package api` and already imports `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"testing"`, `"github.com/percona/obs-dashboard/internal/obs"`. Add `"strings"` to the imports.

```go
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
```

- [ ] **Step 4: Run the tests**

```bash
cd backend && go test ./internal/api/ -run TestRebuild -v
```

Expected: 3 tests pass.

- [ ] **Step 5: Run the full API package tests to check for regressions**

```bash
cd backend && go test ./internal/api/ -v
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
cd backend
git add internal/api/handlers.go internal/api/handlers_test.go internal/api/server.go
git commit -s -m "feat(api): add POST /api/rebuild endpoint"
```

---

### Task 3: `useRebuild` composable

**Goal:** Create `frontend/src/composables/useRebuild.ts` with `trigger()`, `isLoading()`, and `errorFor()` that track per-target rebuild state keyed by `"repo/arch"`.

**Files:**
- Create: `frontend/src/composables/useRebuild.ts`

**Acceptance Criteria:**
- [ ] `trigger(project, pkg, repo, arch)` POSTs to `/api/rebuild` with `Content-Type: application/json`
- [ ] `isLoading(repo, arch)` returns `true` between request start and finish
- [ ] `errorFor(repo, arch)` returns the error message string on failure, `null` otherwise
- [ ] Error auto-clears after 4 seconds
- [ ] On success, `isLoading` returns to `false` and `errorFor` returns `null`
- [ ] `trigger()` is safe to call concurrently for different `repo/arch` keys

**Verify:** `cd frontend && npx tsc --noEmit` → no type errors

**Steps:**

- [ ] **Step 1: Create `frontend/src/composables/useRebuild.ts`**

```typescript
import { ref } from 'vue'

export function useRebuild() {
  const loadingMap = ref(new Map<string, boolean>())
  const errorMap = ref(new Map<string, string>())

  function key(repo: string, arch: string): string {
    return `${repo}/${arch}`
  }

  async function trigger(project: string, pkg: string, repo: string, arch: string): Promise<void> {
    const k = key(repo, arch)
    loadingMap.value = new Map(loadingMap.value).set(k, true)
    const cleared = new Map(errorMap.value)
    cleared.delete(k)
    errorMap.value = cleared

    try {
      const res = await fetch('/api/rebuild', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project, repo, arch, package: pkg }),
      })
      if (!res.ok) {
        const msg = (await res.text()).trim() || `HTTP ${res.status}`
        const m = new Map(errorMap.value)
        m.set(k, msg)
        errorMap.value = m
        setTimeout(() => {
          const m2 = new Map(errorMap.value)
          m2.delete(k)
          errorMap.value = m2
        }, 4000)
      }
    } catch {
      const m = new Map(errorMap.value)
      m.set(k, 'Network error')
      errorMap.value = m
      setTimeout(() => {
        const m2 = new Map(errorMap.value)
        m2.delete(k)
        errorMap.value = m2
      }, 4000)
    } finally {
      const m = new Map(loadingMap.value)
      m.set(k, false)
      loadingMap.value = m
    }
  }

  function isLoading(repo: string, arch: string): boolean {
    return loadingMap.value.get(key(repo, arch)) ?? false
  }

  function errorFor(repo: string, arch: string): string | null {
    return errorMap.value.get(key(repo, arch)) ?? null
  }

  return { trigger, isLoading, errorFor }
}
```

- [ ] **Step 2: Type-check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no output (zero errors).

- [ ] **Step 3: Commit**

```bash
cd frontend  # or repo root
git add frontend/src/composables/useRebuild.ts
git commit -s -m "feat(frontend): add useRebuild composable"
```

---

### Task 4: PackageCard.vue rebuild UI

**Goal:** Wire `useRebuild` into `PackageCard.vue` — restart icon on qualifying target rows, spinner during in-flight requests, inline error below the row.

**Files:**
- Modify: `frontend/src/components/PackageCard.vue`

**Acceptance Criteria:**
- [ ] Targets in `failed`, `broken`, `unresolvable`, `blocked` states show a `↺` button at the right end of the target header row
- [ ] Clicking `↺` calls `trigger(pkg.project, pkg.name, t.repo, t.arch)`; the click does not toggle the expand/collapse on the row
- [ ] While `isLoading(t.repo, t.arch)` is `true`, the `↺` is replaced by a spinning `↺`
- [ ] When `errorFor(t.repo, t.arch)` is non-null, a red error line appears inside the target container below the collapsible body; it disappears after 4 seconds automatically
- [ ] Targets not in the four qualifying states do not show the button
- [ ] `npx tsc --noEmit` passes

**Verify:** `cd frontend && npx tsc --noEmit` → no type errors. Manual test: load the dashboard, find a target in `failed`/`blocked`/`broken`/`unresolvable`, verify `↺` appears; click it to verify the spinner shows briefly.

**Steps:**

- [ ] **Step 1: Add import and composable instantiation in the `<script setup>` block**

After the existing import line:
```typescript
import { displayVersion, TAG_LABEL } from '../composables/useEventDisplay'
```

Add:
```typescript
import { useRebuild } from '../composables/useRebuild'
```

After the existing `const props = defineProps<{ pkg: Package }>()` line, add:
```typescript
const { trigger: triggerRebuild, isLoading: isRebuildLoading, errorFor: rebuildErrorFor } = useRebuild()
```

After the existing `const SKIP_STATES` and `const IN_PROGRESS_STATES` constants, add:
```typescript
const REBUILD_STATES = new Set(['failed', 'broken', 'unresolvable', 'blocked'])
```

- [ ] **Step 2: Add `<style scoped>` block for the spinner animation**

Before the closing `</script>` tag is NOT right — `<style>` is a top-level SFC block. Append a `<style scoped>` block at the very end of the file, after `</template>`:

```html
<style scoped>
@keyframes rebuild-spin {
  to { transform: rotate(360deg); }
}
.rebuild-spinning {
  display: inline-block;
  animation: rebuild-spin 0.7s linear infinite;
}
</style>
```

- [ ] **Step 3: Add rebuild button in the target header row template**

The target header row currently ends with:
```html
            <a
              :href="logUrl(t.repo, t.arch)"
              target="_blank"
              rel="noopener"
              style="font-size: 10.5px; color: var(--brand-purple); font-weight: 700; flex-shrink: 0; text-decoration: none;"
              @click.stop
            >log ↗</a>
            <span
              v-if="hasDetail(t)"
              style="font-size: 10px; color: var(--text-muted); flex-shrink: 0; width: 12px; text-align: center;"
            >{{ isExpanded(t) ? '▾' : '▸' }}</span>
```

Insert the rebuild button **after** the `log ↗` link and **before** the chevron `<span>`:

```html
            <button
              v-if="REBUILD_STATES.has(t.state)"
              :style="{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                padding: '0 2px',
                fontSize: '13px',
                color: 'var(--text-muted)',
                flexShrink: '0',
                lineHeight: '1',
              }"
              title="Retrigger build"
              @click.stop="triggerRebuild(pkg.project, pkg.name, t.repo, t.arch)"
            >
              <span :class="{ 'rebuild-spinning': isRebuildLoading(t.repo, t.arch) }">↺</span>
            </button>
```

- [ ] **Step 4: Add error row inside the target container div**

The target container `<div>` currently ends with the collapsible body:
```html
          <!-- Target body (collapsible) -->
          <div
            v-show="isExpanded(t)"
            style="padding: 0 9px 8px calc(9px + 8px + 9px); display: flex; flex-direction: column; gap: 5px;"
          >
            ...
          </div>
        </div>
```

Insert the error row **after** the collapsible body `<div>` and **inside** the target container `<div>` (before its closing `</div>`):

```html
          <!-- Rebuild error -->
          <div
            v-if="rebuildErrorFor(t.repo, t.arch)"
            :style="{
              padding: '3px 9px 6px',
              fontSize: '10.5px',
              color: 'var(--fail)',
              fontFamily: 'var(--font-mono)',
            }"
          >{{ rebuildErrorFor(t.repo, t.arch) }}</div>
```

- [ ] **Step 5: Type-check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no output (zero errors).

- [ ] **Step 6: Build to verify no runtime warnings**

```bash
cd frontend && npm run build 2>&1 | tail -20
```

Expected: build completes without errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/PackageCard.vue
git commit -s -m "feat(frontend): add rebuild button to PackageCard target rows"
```
