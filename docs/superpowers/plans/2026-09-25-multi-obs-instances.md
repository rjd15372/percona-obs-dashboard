# Multiple OBS Instances Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build one unified dashboard view from several OBS instances whose repos are split between them, with every build target labelled by the instance it runs on.

**Architecture:** A new `obs.Fleet` owns one `obs.Client` per configured `obs.Instance`, speaks logical (root-free) project names, and merges or routes each OBS call (merge across hosting instances, route by repo owner, first hosting instance wins). One MQ consumer runs per instance. Above Fleet the working set, task chain, rollup and store keep treating a package as one logical entity; `Target.Instance` / `Event.Instance` carry the instance slug. A one-time migration strips the old root from stored project names.

**Tech Stack:** Go 1.25 (chi, viper, modernc sqlite, amqp091), Vue 3 + TypeScript (no runtime test runner — type-level `*.test-d.ts` checked by `vue-tsc` in `npm run build`).

**Spec:** `docs/superpowers/specs/2026-09-25-multi-obs-instances-design.md`

## Global Constraints

- **Commits:** always `git commit -s`. **Never** add a `Co-Authored-By:` trailer.
- **Backend verify:** `cd backend && go test ./...` must pass at the end of every backend task. **Frontend verify:** `cd frontend && npm run build` must exit 0 at the end of every frontend task.
- **Task order is load-bearing.** Tasks 3–8 move every component onto `obs.Fleet` while running in **identity mode** (`obs.SingleFleet(client, root)`: logical names equal instance names, prefix kept), so behaviour and test fixtures stay unchanged. Task 9 flips the whole backend to root-free names atomically (classifier + store + API + main + migration). The branch is only deployable after Task 10; frontend Tasks 11–14 require Task 9's root-free API.
- **Instance slug** = lowercase `name` with every char outside `[a-z0-9]` replaced by `_` (`openSUSE` → `opensuse`, `Percona OBS` → `percona_obs`). The slug is the identity everywhere (DB `targets_json`, `events.instance`, API, URL state `?instances=`, env vars `OBS_<SLUG_UPPER>_USERNAME` / `_PASSWORD`). `name` is display-only.
- **Legacy instance** (no `obs_instances` in config): name `openSUSE`, slug `opensuse`, root = `obs_root` (default `isv:percona`), web `https://build.opensuse.org`, download `https://download.opensuse.org/repositories`, registry `registry.opensuse.org`, MQ exchange `pubsub`, routing prefix `opensuse.obs`.
- **Logical names** after Task 9: `inst.ToLogical("percona:ppg:17") == ("ppg:17", true)`, `inst.ToInstance("ppg:17") == "percona:ppg:17"`. Out-of-root projects are ignored everywhere.
- **URL formats (binding, backend and frontend identical):**
  - project page `<web_url>/project/show/<instance project>`
  - package page `<web_url>/package/show/<instance project>/<pkg>`
  - live log `<web_url>/package/live_build_log/<instance project>/<pkg>/<repo>/<arch>`
  - download base `<download_url>/<instance project with every ':' → ':/'>/<repo>/`
  - image base `<registry>/<instance project lowercased, ':' → '/'>/<repo>/<name>`
- **Partial failure:** merged calls return an error only when no hosting instance succeeded; 404 = "not hosted there" (neither success nor failure) — but when **every** host 404s the NotFound error is returned (preserves single-instance semantics). Build targets of failed instances are carried forward, never dropped.
- **Health:** `ok = false` after 3 consecutive non-404 API failures, or MQ disconnected for more than 2 minutes. Checked every 15 s; flips are pushed as SSE `{"type":"instance_health","data":{"slug":…,"health":{…}}}`.
- **Single-instance deployments show no instance UI** (no badges, no per-instance links, no filter chips, no By-instance card, no per-instance metrics block).
- **URL state:** no existing URL parameter carries a project name (`ctx`/`actx` hold keys like `staging`, `pr-106`), so the spec's "strip `isv:percona:` from old bookmarks" needs no code. Recorded here so no task adds it.
- **Migration placement deviation:** `store.Open(path)` has no config, so the root-strip migration is an exported `store.MigrateLogicalNames(db, legacyRoot, slug)` called from `main` right after `store.Open`, before anything reads packages. Same idempotence and single-transaction guarantees as the spec.

**User decisions (already made):**
- "Different roots, same subpath" — instances have their own root; packages merge on the root-relative subpath.
- "Own RabbitMQ bus" — every instance publishes standard OBS AMQP events on its own broker.
- "Never — disjoint" — a repo lives on exactly one instance per package; target identity stays `(repo, arch)`.
- "Labels, links + filter" — per-target instance badges, instance-correct links, an instance filter, per-instance Overview/Metrics breakdowns.
- "Yes, let's go with A" — fan-out/routing `obs.Fleet` approach.
- "Let's remove the prefix entirely" — logical project names are root-free subpaths; no primary instance, no `logical_root`.
- Sections 1–4 of the design and the written spec approved as-is.

---

## File Structure

**Backend — create**
- `backend/internal/obs/instance.go` — `InstanceInfo`, `Instance` (root translation, URL builders, health), `LegacyInstance`, `StatusError`/`IsNotFound`.
- `backend/internal/obs/fleet.go` — `Fleet`: membership, repo owners, fan-out helpers, every routed/merged OBS method, metrics aggregation, discovery, health watch.
- `backend/internal/obs/carry.go` — `CarryForward`, `WithoutInstance`.
- `backend/internal/store/migrate_logical.go` — `MigrateLogicalNames`.
- `backend/internal/store/instances.go` — `QueryProjectPackages`, `QueryTargetCountsByInstance`.
- `backend/internal/api/instances.go` — `GET /api/instances`.
- Tests next to each: `instance_test.go`, `fleet_test.go`, `carry_test.go`, `migrate_logical_test.go`, `instances_test.go` (store and api).

**Backend — modify**
- `backend/internal/config/config.go` (+test) — `obs_instances`, `InstanceConfig`, `Slug`, legacy fallback, `CONFIG_FILE` env fix.
- `backend/internal/obs/client.go` — typed `StatusError` from `get`/`getFile`/`post`; `Instance` field on `PackageBuildState`/`BinaryArtifact`; `PublishFlags` delegate.
- `backend/internal/obs/{tasks,env,trigger,poller,classifier}.go` — Fleet, carry-forward, root-free classifier.
- `backend/internal/model/types.go` — `Target.Instance`, `Event.Instance`.
- `backend/internal/store/{db,events,packages}.go` — `events.instance`, root-free query signatures.
- `backend/internal/worker/worker.go`, `backend/internal/mq/consumer.go`, `backend/internal/cve/scanner.go`, `backend/internal/hub/message.go`.
- `backend/internal/api/{server,handlers,release_artifacts,artifact_metadata,metrics,overview}.go`.
- `backend/cmd/obsboard/main.go`, `config.yaml.example`, `backend/config.yaml.example`, `.env.example`.

**Frontend — create**
- `frontend/src/lib/instances.ts` (+ `instances.test-d.ts`) — instance types' URL builders.
- `frontend/src/composables/useInstances.ts` — singleton instance registry + health updates.
- `frontend/src/components/InstanceBadge.vue` — the per-target badge.

**Frontend — modify**
- `frontend/src/lib/project.ts` — replaced: `isPRProject`, `projectInContext` (root-free).
- `frontend/src/lib/{contexts,overview,tarballs}.ts`, `frontend/src/types/{api,overview,metrics}.ts`.
- `frontend/src/composables/{useRealtimeStream,useEvents,usePackages,useArtifacts,useUrlState}.ts`.
- `frontend/src/components/{PackageCard,GreenStrip,PRBoard,PackagesSubTab,TarballsSubTab,ContainersSubTab,ArtifactsPanel,ContextBar,MainGrid,FailureBoard,OverviewPanel,MetricsPanel,EventRow,PackageEventGroup,RebuildBarChart,CveExposureTable}.vue`, `frontend/src/App.vue`.

---

### Task 1: Config — `obs_instances` with legacy fallback

**Goal:** `config.Load` returns `Config.Instances []InstanceConfig` (from `obs_instances`, or one legacy instance built from `obs.*`/`mq.url`/`obs_root`), validated, with per-instance env credentials; existing fields stay for now.

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

**Acceptance Criteria:**
- [ ] Without `obs_instances`, `cfg.Instances` has exactly one entry: name `openSUSE`, slug `opensuse`, root `isv:percona`, api `https://api.opensuse.org`, web/download/registry openSUSE hosts, MQ exchange `pubsub`, prefix `opensuse.obs`, budget 60; `cfg.LegacyRoot == "isv:percona"`.
- [ ] With a YAML file (via `CONFIG_FILE` env) defining two instances, both load in order with slugs, trailing `/` trimmed from URLs, MQ defaults applied, budget defaults to 60 when omitted.
- [ ] `OBS_<SLUG>_USERNAME` / `OBS_<SLUG>_PASSWORD` override file credentials.
- [ ] Load fails for: duplicate slug, empty root, empty api_url/web_url/download_url/registry/mq.url, missing username; legacy path still fails with `OBS_USERNAME is required`.
- [ ] Existing config tests still pass.

**Verify:** `cd backend && go test ./internal/config/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — append to `backend/internal/config/config_test.go`:

```go
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyInstanceFallback(t *testing.T) {
	os.Setenv("OBS_USERNAME", "u")
	defer os.Unsetenv("OBS_USERNAME")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("want 1 legacy instance, got %d", len(cfg.Instances))
	}
	in := cfg.Instances[0]
	if in.Name != "openSUSE" || in.Slug != "opensuse" || in.Root != "isv:percona" {
		t.Errorf("unexpected legacy identity: %+v", in)
	}
	if in.APIURL != "https://api.opensuse.org" || in.WebURL != "https://build.opensuse.org" ||
		in.DownloadURL != "https://download.opensuse.org/repositories" || in.Registry != "registry.opensuse.org" {
		t.Errorf("unexpected legacy hosts: %+v", in)
	}
	if in.MQ.Exchange != "pubsub" || in.MQ.RoutingPrefix != "opensuse.obs" || in.MinuteRequestBudget != 60 {
		t.Errorf("unexpected legacy MQ/budget: %+v", in)
	}
	if in.Username != "u" {
		t.Errorf("legacy username = %q", in.Username)
	}
	if cfg.LegacyRoot != "isv:percona" {
		t.Errorf("LegacyRoot = %q", cfg.LegacyRoot)
	}
}

const twoInstances = `
obs_instances:
  - name: openSUSE
    root: "isv:percona"
    api_url: "https://api.opensuse.org/"
    web_url: "https://build.opensuse.org/"
    download_url: "https://download.opensuse.org/repositories"
    registry: "registry.opensuse.org"
    username: "fileuser"
    mq:
      url: "amqps://a"
  - name: Percona OBS
    root: "percona"
    api_url: "https://api.obs.example.com"
    web_url: "https://obs.example.com"
    download_url: "https://download.obs.example.com/repositories"
    registry: "registry.obs.example.com"
    minute_request_budget: 10
    mq:
      url: "amqps://b"
      exchange: "events"
      routing_prefix: "percona.obs"
`

func TestLoadTwoInstances(t *testing.T) {
	os.Setenv("CONFIG_FILE", writeConfig(t, twoInstances))
	os.Setenv("OBS_PERCONA_OBS_USERNAME", "envuser")
	os.Setenv("OBS_PERCONA_OBS_PASSWORD", "envpass")
	defer os.Unsetenv("CONFIG_FILE")
	defer os.Unsetenv("OBS_PERCONA_OBS_USERNAME")
	defer os.Unsetenv("OBS_PERCONA_OBS_PASSWORD")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("want 2 instances, got %d", len(cfg.Instances))
	}
	a, b := cfg.Instances[0], cfg.Instances[1]
	if a.Slug != "opensuse" || a.APIURL != "https://api.opensuse.org" || a.WebURL != "https://build.opensuse.org" {
		t.Errorf("instance a: %+v", a)
	}
	if a.MQ.Exchange != "pubsub" || a.MQ.RoutingPrefix != "opensuse.obs" || a.MinuteRequestBudget != 60 {
		t.Errorf("instance a defaults: %+v", a)
	}
	if a.Username != "fileuser" {
		t.Errorf("instance a username = %q", a.Username)
	}
	if b.Slug != "percona_obs" || b.Root != "percona" || b.MinuteRequestBudget != 10 {
		t.Errorf("instance b: %+v", b)
	}
	if b.MQ.Exchange != "events" || b.MQ.RoutingPrefix != "percona.obs" {
		t.Errorf("instance b MQ: %+v", b.MQ)
	}
	if b.Username != "envuser" || b.Password != "envpass" {
		t.Errorf("instance b env creds: %q/%q", b.Username, b.Password)
	}
}

func TestLoadInstanceValidation(t *testing.T) {
	cases := map[string]string{
		"duplicate slug": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
  - {name: a, root: s, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
`,
		"empty root": `
obs_instances:
  - {name: A, api_url: x, web_url: x, download_url: x, registry: x, username: u, mq: {url: x}}
`,
		"missing registry": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, username: u, mq: {url: x}}
`,
		"missing mq url": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, username: u}
`,
		"missing username": `
obs_instances:
  - {name: A, root: r, api_url: x, web_url: x, download_url: x, registry: x, mq: {url: x}}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			os.Setenv("CONFIG_FILE", writeConfig(t, body))
			defer os.Unsetenv("CONFIG_FILE")
			if _, err := Load(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"openSUSE": "opensuse", "Percona OBS": "percona_obs", "x-1": "x_1"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
```

Add `"path/filepath"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/config/ -run 'Legacy|TwoInstances|InstanceValidation|Slug' -v`
Expected: FAIL — `cfg.Instances undefined`, `Slug undefined`.

- [ ] **Step 3: Implement** — in `backend/internal/config/config.go`:

Add `"os"` to imports. Add to `Config`:

```go
	// Instances lists every OBS instance the dashboard reads from, in config
	// order. Always at least one (the legacy instance when obs_instances is
	// absent).
	Instances []InstanceConfig
	// LegacyRoot is obs_root: the root stored project names carried before
	// the root-free migration. Used only by that migration.
	LegacyRoot string
```

Add types and helpers:

```go
// MQInstanceConfig is one instance's AMQP event bus.
type MQInstanceConfig struct {
	URL           string `mapstructure:"url"`
	Exchange      string `mapstructure:"exchange"`
	RoutingPrefix string `mapstructure:"routing_prefix"`
}

// InstanceConfig describes one OBS instance.
type InstanceConfig struct {
	Name                string           `mapstructure:"name"`
	Slug                string           `mapstructure:"-"`
	Root                string           `mapstructure:"root"`
	APIURL              string           `mapstructure:"api_url"`
	WebURL              string           `mapstructure:"web_url"`
	DownloadURL         string           `mapstructure:"download_url"`
	Registry            string           `mapstructure:"registry"`
	Username            string           `mapstructure:"username"`
	Password            string           `mapstructure:"password"`
	MinuteRequestBudget int              `mapstructure:"-"`
	MQ                  MQInstanceConfig `mapstructure:"mq"`
}

// rawInstance distinguishes an omitted budget (default 60) from an explicit 0.
type rawInstance struct {
	InstanceConfig      `mapstructure:",squash"`
	MinuteRequestBudget *int `mapstructure:"minute_request_budget"`
}

// Slug derives an instance's stable identifier from its display name:
// lowercase, every character outside [a-z0-9] replaced by '_'.
func Slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func loadInstances(v *viper.Viper, cfg *Config) ([]InstanceConfig, error) {
	var raw []rawInstance
	if v.IsSet("obs_instances") {
		if err := v.UnmarshalKey("obs_instances", &raw); err != nil {
			return nil, fmt.Errorf("obs_instances: %w", err)
		}
	}
	if len(raw) == 0 {
		if cfg.OBS.Username == "" {
			return nil, fmt.Errorf("OBS_USERNAME is required")
		}
		return []InstanceConfig{{
			Name:                "openSUSE",
			Slug:                "opensuse",
			Root:                cfg.OBSRoot,
			APIURL:              cfg.OBS.BaseURL,
			WebURL:              "https://build.opensuse.org",
			DownloadURL:         "https://download.opensuse.org/repositories",
			Registry:            "registry.opensuse.org",
			Username:            cfg.OBS.Username,
			Password:            cfg.OBS.Password,
			MinuteRequestBudget: cfg.OBS.MinuteRequestBudget,
			MQ:                  MQInstanceConfig{URL: cfg.MQ.URL, Exchange: "pubsub", RoutingPrefix: "opensuse.obs"},
		}}, nil
	}

	out := make([]InstanceConfig, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		in := r.InstanceConfig
		in.Slug = Slug(in.Name)
		label := fmt.Sprintf("obs_instances[%d] (%s)", i, in.Name)
		if in.Name == "" {
			return nil, fmt.Errorf("obs_instances[%d]: name is required", i)
		}
		if seen[in.Slug] {
			return nil, fmt.Errorf("%s: duplicate instance slug %q", label, in.Slug)
		}
		seen[in.Slug] = true
		env := "OBS_" + strings.ToUpper(in.Slug)
		if u := os.Getenv(env + "_USERNAME"); u != "" {
			in.Username = u
		}
		if p := os.Getenv(env + "_PASSWORD"); p != "" {
			in.Password = p
		}
		in.MinuteRequestBudget = 60
		if r.MinuteRequestBudget != nil {
			in.MinuteRequestBudget = *r.MinuteRequestBudget
		}
		if in.MQ.Exchange == "" {
			in.MQ.Exchange = "pubsub"
		}
		if in.MQ.RoutingPrefix == "" {
			in.MQ.RoutingPrefix = "opensuse.obs"
		}
		in.APIURL = strings.TrimRight(in.APIURL, "/")
		in.WebURL = strings.TrimRight(in.WebURL, "/")
		in.DownloadURL = strings.TrimRight(in.DownloadURL, "/")
		in.Registry = strings.TrimRight(in.Registry, "/")
		for field, val := range map[string]string{
			"root": in.Root, "api_url": in.APIURL, "web_url": in.WebURL,
			"download_url": in.DownloadURL, "registry": in.Registry, "mq.url": in.MQ.URL,
		} {
			if val == "" {
				return nil, fmt.Errorf("%s: %s is required", label, field)
			}
		}
		if in.Username == "" {
			return nil, fmt.Errorf("%s: username is required (or set %s_USERNAME)", label, env)
		}
		out = append(out, in)
	}
	return out, nil
}
```

Fix `CONFIG_FILE` (viper does not read env before `AutomaticEnv`): replace

```go
	if f := v.GetString("CONFIG_FILE"); f != "" {
```
with
```go
	if f := os.Getenv("CONFIG_FILE"); f != "" {
```

At the end of `Load`, replace

```go
	if cfg.OBS.Username == "" {
		return nil, fmt.Errorf("OBS_USERNAME is required")
	}

	return cfg, nil
```
with
```go
	instances, err := loadInstances(v, cfg)
	if err != nil {
		return nil, err
	}
	cfg.Instances = instances
	cfg.LegacyRoot = cfg.OBSRoot

	return cfg, nil
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/config/ -v`
Expected: PASS (new and existing tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/
git commit -s -m "feat(config): obs_instances with legacy single-instance fallback"
```

---

### Task 2: `obs.Instance` — translation, URL builders, health, typed status errors

**Goal:** An `obs.Instance` type translates logical ↔ instance project names, builds every OBS/download/registry URL, and tracks health; client HTTP errors become a typed `*StatusError` so 404 is detectable.

**Files:**
- Create: `backend/internal/obs/instance.go`
- Modify: `backend/internal/obs/client.go` (`get`, `getFile`, `post`)
- Test: `backend/internal/obs/instance_test.go`

**Acceptance Criteria:**
- [ ] `ToLogical`/`ToInstance` round-trip for colon roots (`isv:percona`) and single-segment roots (`percona`); out-of-root → `("", false)`.
- [ ] Identity instances (`Identity: true`) return names unchanged but still reject out-of-root projects in `ToLogical`; `PathRoot()` is `""` for identity instances.
- [ ] URL builders produce exactly the Global Constraints formats.
- [ ] `IsNotFound` is true for a 404 from `get`; `StatusError.Error()` text is byte-identical to the old `fmt.Errorf` text.
- [ ] Health: `OK` false after 3 consecutive failures; 404 and `context.Canceled` don't count; a success resets; MQ down > 2 min → not OK; MQ reconnect → OK.

**Verify:** `cd backend && go test ./internal/obs/ -run 'Instance|StatusError|Health' -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — create `backend/internal/obs/instance_test.go`:

```go
package obs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testInstance(root string) *Instance {
	return NewInstance(InstanceInfo{
		Name: "Percona", Slug: "percona", Root: root,
		WebURL: "https://obs.example.com", DownloadURL: "https://dl.example.com/repositories",
		Registry: "registry.example.com",
	}, nil)
}

func TestInstanceTranslation(t *testing.T) {
	for _, root := range []string{"isv:percona", "percona"} {
		in := testInstance(root)
		got, ok := in.ToLogical(root + ":ppg:17")
		if !ok || got != "ppg:17" {
			t.Errorf("root %q: ToLogical = %q,%v", root, got, ok)
		}
		if back := in.ToInstance("ppg:17"); back != root+":ppg:17" {
			t.Errorf("root %q: ToInstance = %q", root, back)
		}
		if _, ok := in.ToLogical("home:someone:ppg:17"); ok {
			t.Errorf("root %q: out-of-root project accepted", root)
		}
		if _, ok := in.ToLogical(root); ok {
			t.Errorf("root %q: bare root accepted as a project", root)
		}
	}
}

func TestIdentityInstance(t *testing.T) {
	in := LegacyInstance(nil, "isv:percona")
	if got, ok := in.ToLogical("isv:percona:ppg:17"); !ok || got != "isv:percona:ppg:17" {
		t.Errorf("identity ToLogical = %q,%v", got, ok)
	}
	if _, ok := in.ToLogical("home:x:ppg"); ok {
		t.Error("identity instance accepted out-of-root project")
	}
	if got := in.ToInstance("isv:percona:ppg:17"); got != "isv:percona:ppg:17" {
		t.Errorf("identity ToInstance = %q", got)
	}
	if in.PathRoot() != "" {
		t.Errorf("identity PathRoot = %q", in.PathRoot())
	}
	if testInstance("percona").PathRoot() != "percona" {
		t.Error("PathRoot should be the root for non-identity instances")
	}
}

func TestInstanceURLs(t *testing.T) {
	in := testInstance("percona")
	cases := map[string]string{
		in.ProjectURL("ppg:17"):                         "https://obs.example.com/project/show/percona:ppg:17",
		in.PackageURL("ppg:17", "pg"):                   "https://obs.example.com/package/show/percona:ppg:17/pg",
		in.LiveLogURL("ppg:17", "pg", "RHEL_9", "x86_64"): "https://obs.example.com/package/live_build_log/percona:ppg:17/pg/RHEL_9/x86_64",
		in.DownloadURL("ppg:17", "RHEL_9"):              "https://dl.example.com/repositories/percona:/ppg:/17/RHEL_9/",
		in.ImageBase("ppg:staging:17:containers", "ubi9", "pg"): "registry.example.com/percona/ppg/staging/17/containers/ubi9/pg",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	legacy := LegacyInstance(nil, "")
	if got := legacy.ImageBase("isv:percona:PR:pr-9:ppg", "images", "pg"); got != "registry.opensuse.org/isv/percona/pr/pr-9/ppg/images/pg" {
		t.Errorf("legacy ImageBase = %q", got)
	}
}

func TestStatusErrorNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	_, err := c.PackageBinaries(Interactive(context.Background()), "p", "r", "a", "pkg")
	if !IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
	if want := "OBS /build/p/r/a/pkg: 404 Not Found — gone"; err.Error() != want {
		t.Errorf("error text = %q, want %q", err.Error(), want)
	}
	if IsNotFound(errors.New("x")) {
		t.Error("plain error reported as NotFound")
	}
}

func TestInstanceHealth(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	in := testInstance("percona")
	in.health.now = func() time.Time { return now }
	in.SetMQConnected(true)

	if !in.Health().OK {
		t.Fatal("fresh instance should be OK")
	}
	in.observe(&StatusError{Code: http.StatusNotFound})
	in.observe(context.Canceled)
	in.observe(errors.New("boom"))
	in.observe(errors.New("boom"))
	if !in.Health().OK {
		t.Fatal("2 failures (404/cancel ignored) should still be OK")
	}
	in.observe(errors.New("boom"))
	h := in.Health()
	if h.OK || h.ConsecutiveFailures != 3 || h.LastError != "boom" {
		t.Fatalf("after 3 failures: %+v", h)
	}
	in.observe(nil)
	if h := in.Health(); !h.OK || h.ConsecutiveFailures != 0 || h.LastSuccess == nil {
		t.Fatalf("success should reset: %+v", h)
	}

	in.SetMQConnected(false)
	now = now.Add(time.Minute)
	if !in.Health().OK {
		t.Fatal("MQ down 1m should still be OK")
	}
	now = now.Add(90 * time.Second)
	if in.Health().OK {
		t.Fatal("MQ down 2m30s should not be OK")
	}
	in.SetMQConnected(true)
	if !in.Health().OK {
		t.Fatal("MQ reconnect should restore OK")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/obs/ -run 'Instance|StatusError|Health' -v`
Expected: FAIL — `undefined: NewInstance`, `undefined: IsNotFound`.

- [ ] **Step 3: Implement** — create `backend/internal/obs/instance.go`:

```go
package obs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StatusError is a non-2xx OBS HTTP response.
type StatusError struct {
	Path   string
	Code   int
	Status string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("OBS %s: %s — %s", e.Path, e.Status, e.Body)
}

// IsNotFound reports whether err is an OBS 404 — for a Fleet, "this
// instance does not host that project/package".
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// InstanceInfo is the static description of one OBS instance.
type InstanceInfo struct {
	Name        string
	Slug        string
	Root        string
	WebURL      string
	DownloadURL string
	Registry    string

	MQURL           string
	MQExchange      string
	MQRoutingPrefix string

	// Identity makes logical names equal instance names (root kept). Used by
	// tests and by the pre-migration single-instance wiring; ToLogical still
	// rejects projects outside Root.
	Identity bool
}

const (
	healthFailureThreshold = 3
	mqDownGrace            = 2 * time.Minute
)

// Health is an instance's reachability as shown in the UI.
type Health struct {
	OK                  bool       `json:"ok"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	MQConnected         bool       `json:"mq_connected"`
}

type healthState struct {
	mu          sync.Mutex
	now         func() time.Time
	lastSuccess time.Time
	lastErrorAt time.Time
	lastError   string
	consecutive int
	mqConnected bool
	mqDownSince time.Time
}

// Instance is one OBS instance: its client, name translation, URL builders
// and health.
type Instance struct {
	InstanceInfo
	Client *Client
	health healthState
}

func NewInstance(info InstanceInfo, client *Client) *Instance {
	in := &Instance{InstanceInfo: info, Client: client}
	in.health.now = time.Now
	in.health.mqDownSince = time.Now()
	return in
}

// LegacyInstance is the openSUSE instance in identity mode (logical names
// keep the root).
func LegacyInstance(c *Client, root string) *Instance {
	return NewInstance(InstanceInfo{
		Name:            "openSUSE",
		Slug:            "opensuse",
		Root:            root,
		WebURL:          "https://build.opensuse.org",
		DownloadURL:     "https://download.opensuse.org/repositories",
		Registry:        "registry.opensuse.org",
		MQExchange:      "pubsub",
		MQRoutingPrefix: "opensuse.obs",
		Identity:        true,
	}, c)
}

// ToLogical maps an instance project name to its logical name. ok is false
// for projects outside this instance's root.
func (i *Instance) ToLogical(project string) (string, bool) {
	prefix := i.Root + ":"
	if !strings.HasPrefix(project, prefix) || len(project) == len(prefix) {
		return "", false
	}
	if i.Identity {
		return project, true
	}
	return project[len(prefix):], true
}

// ToInstance maps a logical project name to this instance's project name.
func (i *Instance) ToInstance(logical string) string {
	if i.Identity || i.Root == "" {
		return logical
	}
	return i.Root + ":" + logical
}

// PathRoot is the prefix a client must add to a logical name to get the
// instance project name ("" for identity instances).
func (i *Instance) PathRoot() string {
	if i.Identity {
		return ""
	}
	return i.Root
}

func (i *Instance) ProjectURL(project string) string {
	return i.WebURL + "/project/show/" + i.ToInstance(project)
}

func (i *Instance) PackageURL(project, pkg string) string {
	return i.WebURL + "/package/show/" + i.ToInstance(project) + "/" + pkg
}

func (i *Instance) LiveLogURL(project, pkg, repo, arch string) string {
	return i.WebURL + "/package/live_build_log/" + i.ToInstance(project) + "/" + pkg + "/" + repo + "/" + arch
}

func (i *Instance) DownloadURL(project, repo string) string {
	return i.InstanceInfo.DownloadURL + "/" + strings.ReplaceAll(i.ToInstance(project), ":", ":/") + "/" + repo + "/"
}

func (i *Instance) ImageBase(project, repo, name string) string {
	return i.Registry + "/" + strings.ToLower(strings.ReplaceAll(i.ToInstance(project), ":", "/")) + "/" + repo + "/" + name
}

// observe records the outcome of one API call for health tracking. 404s
// ("not hosted here") and caller cancellations say nothing about health.
func (i *Instance) observe(err error) {
	if err != nil && (IsNotFound(err) || errors.Is(err, context.Canceled)) {
		return
	}
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.lastSuccess = h.now()
		h.consecutive = 0
		return
	}
	h.consecutive++
	h.lastError = err.Error()
	h.lastErrorAt = h.now()
}

// SetMQConnected records the instance's AMQP connection state.
func (i *Instance) SetMQConnected(connected bool) {
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mqConnected && !connected {
		h.mqDownSince = h.now()
	}
	h.mqConnected = connected
}

// Health returns the current health snapshot.
func (i *Instance) Health() Health {
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	out := Health{
		ConsecutiveFailures: h.consecutive,
		LastError:           h.lastError,
		MQConnected:         h.mqConnected,
	}
	if !h.lastSuccess.IsZero() {
		t := h.lastSuccess
		out.LastSuccess = &t
	}
	if !h.lastErrorAt.IsZero() {
		t := h.lastErrorAt
		out.LastErrorAt = &t
	}
	mqOK := h.mqConnected || h.now().Sub(h.mqDownSince) < mqDownGrace
	out.OK = h.consecutive < healthFailureThreshold && mqOK
	return out
}
```

In `backend/internal/obs/client.go`, in each of `get`, `getFile` and `post`, replace the non-2xx return

```go
		return nil, fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
```
(in `post`: `return fmt.Errorf(...)`) with

```go
		return nil, &StatusError{Path: path, Code: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
```
(in `post`: `return &StatusError{...}`). Leave `PackageIsContainer`'s inline request as is (it already maps 404 to `false, nil`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/obs/ -v`
Expected: PASS (new tests and all existing obs tests — error text is unchanged).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/obs/instance.go backend/internal/obs/instance_test.go backend/internal/obs/client.go
git commit -s -m "feat(obs): Instance with root translation, URL builders and health"
```

---

### Task 3: `obs.Fleet` — merge, route and first-wins across instances

**Goal:** `obs.Fleet` exposes every OBS method the app uses, in logical names, fanning out across hosting instances with membership and repo-owner routing, partial-failure reporting, aggregated metrics, discovery and a health watch.

**Files:**
- Create: `backend/internal/obs/fleet.go`
- Modify: `backend/internal/obs/client.go` (`Instance` field on `PackageBuildState` and `BinaryArtifact`; `PublishFlags` delegate)
- Test: `backend/internal/obs/fleet_test.go`

**Acceptance Criteria:**
- [ ] Merged calls (`BuildResults`, `PackageBuildResults`, `RepoPublishStates`, `PackageBlockedReasons`, `ProjectBinaryList`, `ProjectRepos`, `SearchProjects`) query every hosting instance, translate project names, stamp `Instance`, record repo owners.
- [ ] `BuildResults`/`PackageBuildResults` return `failed []string`; error only when no instance succeeded; all-404 returns the NotFound error.
- [ ] Routed calls go only to the repo owner; unknown owner triggers one refresh; single-instance fleets never refresh.
- [ ] `PackageIsContainer`/`SourceHistory` return the first success in config order; `PackageVersionResult` returns the first non-empty version.
- [ ] Composite `ProjectPublishFlags.Publishes(repo)` follows the repo owner's flags.
- [ ] `Discover` rebuilds membership only from instances that answered and reports which answered.
- [ ] `MetricsSnapshot`/`LimiterStats`/`RatePerSecond` sum across instances; `Statuses()` lists per-instance totals and health.
- [ ] `checkHealth` notifies exactly once per OK flip.

**Verify:** `cd backend && go test ./internal/obs/ -run Fleet -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests** — create `backend/internal/obs/fleet_test.go`:

```go
package obs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOBS serves canned XML per path prefix; unknown paths 404.
type fakeOBS struct {
	srv   *httptest.Server
	hits  atomic.Int64
	paths map[string]string // path (with query) prefix → body
	fail  atomic.Bool
}

func newFakeOBS(t *testing.T, paths map[string]string) *fakeOBS {
	f := &fakeOBS{paths: paths}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		full := r.URL.Path
		if r.URL.RawQuery != "" {
			full += "?" + r.URL.RawQuery
		}
		for prefix, body := range f.paths {
			if strings.HasPrefix(full, prefix) {
				fmt.Fprint(w, body)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func fleetOf(t *testing.T, a, b *fakeOBS) *Fleet {
	mk := func(slug, root string, f *fakeOBS) *Instance {
		return NewInstance(InstanceInfo{Name: slug, Slug: slug, Root: root,
			WebURL: "https://" + slug, DownloadURL: "https://dl." + slug, Registry: "reg." + slug},
			NewClient(f.srv.URL, "u", "p"))
	}
	return NewFleet(mk("x", "isv:percona", a), mk("y", "percona", b))
}

const resultX = `<resultlist>
<result project="isv:percona:ppg:17" repository="RHEL_9" arch="x86_64" state="published">
  <status package="pg" code="succeeded"/>
</result></resultlist>`

const resultY = `<resultlist>
<result project="percona:ppg:17" repository="Debian_12" arch="aarch64" state="building">
  <status package="pg" code="building"/>
</result></resultlist>`

func TestFleetBuildResultsMerges(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, map[string]string{"/build/percona:ppg:17/_result": resultY})
	f := fleetOf(t, x, y)

	states, repoStates, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil || len(failed) != 0 {
		t.Fatalf("err=%v failed=%v", err, failed)
	}
	if len(states) != 2 {
		t.Fatalf("want 2 states, got %+v", states)
	}
	byRepo := map[string]PackageBuildState{}
	for _, s := range states {
		byRepo[s.Repo] = s
	}
	if byRepo["RHEL_9"].Instance != "x" || byRepo["Debian_12"].Instance != "y" {
		t.Errorf("instance stamping wrong: %+v", states)
	}
	if byRepo["RHEL_9"].Project != "ppg:17" || byRepo["Debian_12"].Project != "ppg:17" {
		t.Errorf("project not translated to logical: %+v", states)
	}
	if repoStates["RHEL_9/x86_64"] != "published" || repoStates["Debian_12/aarch64"] != "building" {
		t.Errorf("repo states: %v", repoStates)
	}
	if f.Owner("ppg:17", "Debian_12").Slug != "y" || f.Owner("ppg:17", "RHEL_9").Slug != "x" {
		t.Error("repo owners not recorded")
	}
}

func TestFleetPartialFailure(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil)
	y.fail.Store(true)
	f := fleetOf(t, x, y)

	states, _, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil {
		t.Fatalf("partial failure must not error: %v", err)
	}
	if len(states) != 1 || len(failed) != 1 || failed[0] != "y" {
		t.Fatalf("states=%+v failed=%v", states, failed)
	}

	x.fail.Store(true)
	if _, _, _, err := f.BuildResults(context.Background(), "ppg:17"); err == nil {
		t.Fatal("all instances failing must error")
	}
}

func TestFleetNotHostedIsNotFailure(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil) // 404 for everything
	f := fleetOf(t, x, y)
	_, _, failed, err := f.BuildResults(context.Background(), "ppg:17")
	if err != nil || len(failed) != 0 {
		t.Fatalf("404 must be 'not hosted': err=%v failed=%v", err, failed)
	}

	_, _, _, err = f.BuildResults(context.Background(), "ppg:99")
	if !IsNotFound(err) {
		t.Fatalf("all-404 must return NotFound, got %v", err)
	}
}

func TestFleetMembershipLimitsFanOut(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	f.SetMembers("x", []string{"ppg:17"})
	f.SetMembers("y", nil)
	if _, _, _, err := f.BuildResults(context.Background(), "ppg:17"); err != nil {
		t.Fatal(err)
	}
	if y.hits.Load() != 0 {
		t.Errorf("non-hosting instance was queried %d times", y.hits.Load())
	}
	if !f.IsHosted("ppg:17") || f.IsHosted("ppg:18") {
		t.Error("IsHosted wrong")
	}
}

func TestFleetRoutesByOwner(t *testing.T) {
	x := newFakeOBS(t, nil)
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:17/_result?package=pg": resultY,
		"/build/percona:ppg:17/Debian_12/aarch64/pg/_reason": `<reason><explain>source change</explain></reason>`,
	})
	f := fleetOf(t, x, y)

	// Unknown owner → one PackageBuildResults refresh, then route to y.
	r, err := f.PackageBuildReason(context.Background(), "ppg:17", "Debian_12", "aarch64", "pg")
	if err != nil || r.Explain != "source change" {
		t.Fatalf("reason=%+v err=%v", r, err)
	}
	xHits := x.hits.Load()
	if _, err := f.PackageBuildReason(context.Background(), "ppg:17", "Debian_12", "aarch64", "pg"); err != nil {
		t.Fatal(err)
	}
	if x.hits.Load() != xHits {
		t.Error("known owner must not touch other instances")
	}
}

func TestFleetSingleInstanceNeverRefreshes(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/build/isv:percona:ppg:17/RHEL_9/x86_64/pg/_reason": `<reason><explain>new build</explain></reason>`,
	})
	f := SingleFleet(NewClient(x.srv.URL, "u", "p"), "isv:percona")
	if _, err := f.PackageBuildReason(context.Background(), "isv:percona:ppg:17", "RHEL_9", "x86_64", "pg"); err != nil {
		t.Fatal(err)
	}
	if x.hits.Load() != 1 {
		t.Errorf("single-instance fleet made %d requests, want 1", x.hits.Load())
	}
}

func TestFleetFirstWins(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/build/isv:percona:ppg:17/_result?view=versrel": `<resultlist><result repository="RHEL_9" arch="x86_64"><status package="pg" code="building"/></result></resultlist>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:17/_result?view=versrel": `<resultlist><result repository="Debian_12" arch="aarch64"><status package="pg" code="succeeded" versrel="17.5-1"/></result></resultlist>`,
	})
	f := fleetOf(t, x, y)
	v, err := f.PackageVersionResult(context.Background(), "ppg:17", "pg")
	if err != nil || v != "17.5-1" {
		t.Fatalf("version=%q err=%v (want first non-empty)", v, err)
	}
}

func TestFleetCompositePublishFlags(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/source/isv:percona:ppg:17/_meta": `<project><publish><disable/></publish></project>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/source/percona:ppg:17/_meta": `<project/>`,
	})
	f := fleetOf(t, x, y)
	f.SetOwner("ppg:17", "RHEL_9", "x")
	f.SetOwner("ppg:17", "Debian_12", "y")
	flags, err := f.ProjectPublishFlags(context.Background(), "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if flags.Publishes("RHEL_9") {
		t.Error("RHEL_9 is on x where publishing is disabled")
	}
	if !flags.Publishes("Debian_12") {
		t.Error("Debian_12 is on y where publishing is enabled")
	}
}

func TestFleetBinaryListTranslatesAndOwns(t *testing.T) {
	y := newFakeOBS(t, map[string]string{
		"/build/percona:ppg:releases:17/_result?view=binarylist": `<resultlist>
<result project="percona:ppg:releases:17" repository="Debian_12" arch="aarch64">
  <binarylist package="pg"><binary filename="pg_17.5_arm64.deb" size="1" mtime="2"/></binarylist>
</result></resultlist>`,
	})
	x := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	bins, err := f.ProjectBinaryList(context.Background(), "ppg:releases:17")
	if err != nil || len(bins) != 1 {
		t.Fatalf("bins=%+v err=%v", bins, err)
	}
	if bins[0].Project != "ppg:releases:17" || bins[0].Instance != "y" {
		t.Errorf("binary not translated/stamped: %+v", bins[0])
	}
	if f.Owner("ppg:releases:17", "Debian_12").Slug != "y" {
		t.Error("binary list must record repo owner")
	}
}

func TestFleetDiscover(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/search/project/id": `<collection><project name="isv:percona:ppg:17"/><project name="isv:percona:common"/></collection>`,
	})
	y := newFakeOBS(t, map[string]string{
		"/search/project/id": `<collection><project name="percona:ppg:17"/><project name="percona:ppg:18"/></collection>`,
	})
	f := fleetOf(t, x, y)
	projects, answered, err := f.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 || !answered["x"] || !answered["y"] {
		t.Fatalf("projects=%v answered=%v", projects, answered)
	}
	if hs := f.hosts("ppg:18"); len(hs) != 1 || hs[0].Slug != "y" {
		t.Errorf("ppg:18 hosts = %v", hs)
	}

	y.fail.Store(true)
	_, answered, err = f.Discover(context.Background())
	if err != nil || answered["y"] {
		t.Fatalf("err=%v answered=%v", err, answered)
	}
	if hs := f.hosts("ppg:18"); len(hs) != 1 || hs[0].Slug != "y" {
		t.Error("membership of an instance that failed discovery must be kept")
	}
}

func TestFleetMetricsSum(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/build/isv:percona:ppg:17/_result": resultX})
	y := newFakeOBS(t, map[string]string{"/build/percona:ppg:17/_result": resultY})
	f := fleetOf(t, x, y)
	_, _, _, _ = f.BuildResults(context.Background(), "ppg:17")
	if got := f.MetricsSnapshot()["build_results"]; got != 2 {
		t.Errorf("summed build_results = %d, want 2", got)
	}
	st := f.Statuses()
	if len(st) != 2 || st[0].Total != 1 || st[1].Total != 1 {
		t.Errorf("statuses = %+v", st)
	}
}

func TestFleetHealthFlipNotifiesOnce(t *testing.T) {
	x := newFakeOBS(t, nil)
	y := newFakeOBS(t, nil)
	f := fleetOf(t, x, y)
	for _, in := range f.Instances() {
		in.SetMQConnected(true)
	}
	last := f.healthBaseline()
	var got []string
	notify := func(slug string, h Health) { got = append(got, fmt.Sprintf("%s:%v", slug, h.OK)) }

	yi := f.Instance("y")
	for i := 0; i < 3; i++ {
		yi.observe(fmt.Errorf("boom"))
	}
	f.checkHealth(last, notify)
	f.checkHealth(last, notify)
	yi.observe(nil)
	f.checkHealth(last, notify)
	if strings.Join(got, ",") != "y:false,y:true" {
		t.Errorf("notifications = %v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/obs/ -run Fleet -v`
Expected: FAIL — `undefined: NewFleet`, `undefined: SingleFleet`.

- [ ] **Step 3: Extend client types** — in `backend/internal/obs/client.go`:

Add `Instance string // slug of the OBS instance that reported it; set by Fleet` as the last field of both `PackageBuildState` and `BinaryArtifact`.

Replace the `PublishFlags` struct and `Publishes` with:

```go
// PublishFlags answers whether a repository publishes, resolved from a project's
// _meta <publish> block. Zero value = everything publishes (safe default).
// A Fleet-built composite delegates each repo to its owning instance's flags.
type PublishFlags struct {
	hasDefault     bool
	defaultPublish bool
	perRepo        map[string]bool

	delegate map[string]PublishFlags // slug → that instance's flags
	owner    func(repo string) string
}

// Publishes reports whether repo publishes for this project.
func (f PublishFlags) Publishes(repo string) bool {
	if f.owner != nil {
		if d, ok := f.delegate[f.owner(repo)]; ok {
			return d.Publishes(repo)
		}
	}
	if f.perRepo != nil {
		if v, ok := f.perRepo[repo]; ok {
			return v
		}
	}
	if !f.hasDefault {
		return true
	}
	return f.defaultPublish
}
```

- [ ] **Step 4: Implement Fleet** — create `backend/internal/obs/fleet.go`:

```go
package obs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// Fleet fans OBS calls out across every configured instance. Callers speak
// logical project names; Fleet translates them per instance, merges or
// routes each call, and stamps results with the instance slug.
type Fleet struct {
	instances []*Instance
	bySlug    map[string]*Instance

	mu      sync.RWMutex
	members map[string]map[string]bool // logical project → hosting slugs
	owners  map[string]string          // logical project + "\x00" + repo → slug
}

// NewFleet builds a Fleet over instances, in config order.
func NewFleet(instances ...*Instance) *Fleet {
	f := &Fleet{
		instances: instances,
		bySlug:    make(map[string]*Instance, len(instances)),
		members:   map[string]map[string]bool{},
		owners:    map[string]string{},
	}
	for _, in := range instances {
		f.bySlug[in.Slug] = in
	}
	return f
}

// SingleFleet wraps one client as the legacy openSUSE instance in identity
// mode: logical names equal instance names.
func SingleFleet(c *Client, root string) *Fleet {
	return NewFleet(LegacyInstance(c, root))
}

var fallbackInstance = LegacyInstance(nil, "")

func (f *Fleet) Instances() []*Instance { return f.instances }

// Instance returns the instance with slug, or nil.
func (f *Fleet) Instance(slug string) *Instance { return f.bySlug[slug] }

// InstanceOrDefault returns the instance with slug, else the first
// configured instance. Safe on a nil Fleet (tests with no OBS).
func (f *Fleet) InstanceOrDefault(slug string) *Instance {
	if f == nil || len(f.instances) == 0 {
		return fallbackInstance
	}
	if in := f.bySlug[slug]; in != nil {
		return in
	}
	return f.instances[0]
}

// ImageBase and PackageURL let the CVE scanner resolve URLs by slug.
func (f *Fleet) ImageBase(slug, project, repo, name string) string {
	return f.InstanceOrDefault(slug).ImageBase(project, repo, name)
}

func (f *Fleet) PackageURL(slug, project, pkg string) string {
	return f.InstanceOrDefault(slug).PackageURL(project, pkg)
}

// ── membership ──

// SetMembers replaces the set of projects slug hosts.
func (f *Fleet) SetMembers(slug string, projects []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, set := range f.members {
		delete(set, slug)
		if len(set) == 0 {
			delete(f.members, p)
		}
	}
	for _, p := range projects {
		f.addMemberLocked(slug, p)
	}
}

func (f *Fleet) addMemberLocked(slug, project string) {
	set := f.members[project]
	if set == nil {
		set = map[string]bool{}
		f.members[project] = set
	}
	set[slug] = true
}

func (f *Fleet) AddMember(slug, project string) {
	f.mu.Lock()
	f.addMemberLocked(slug, project)
	f.mu.Unlock()
}

func (f *Fleet) RemoveMember(slug, project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if set := f.members[project]; set != nil {
		delete(set, slug)
		if len(set) == 0 {
			delete(f.members, project)
		}
	}
}

// IsHosted reports whether any instance is known to host project.
func (f *Fleet) IsHosted(project string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.members[project]) > 0
}

// hosts returns the instances hosting project in config order, or every
// instance when membership is unknown.
func (f *Fleet) hosts(project string) []*Instance {
	f.mu.RLock()
	set := f.members[project]
	f.mu.RUnlock()
	if len(set) == 0 {
		return f.instances
	}
	out := make([]*Instance, 0, len(set))
	for _, in := range f.instances {
		if set[in.Slug] {
			out = append(out, in)
		}
	}
	return out
}

// ── repo owners ──

func ownerKey(project, repo string) string { return project + "\x00" + repo }

func (f *Fleet) SetOwner(project, repo, slug string) {
	f.mu.Lock()
	f.owners[ownerKey(project, repo)] = slug
	f.mu.Unlock()
}

// Owner returns the instance that builds repo for project, or nil.
func (f *Fleet) Owner(project, repo string) *Instance {
	f.mu.RLock()
	slug := f.owners[ownerKey(project, repo)]
	f.mu.RUnlock()
	return f.bySlug[slug]
}

// SeedOwners records repo owners from stored targets (startup).
func (f *Fleet) SeedOwners(pkgs []*model.Package) {
	for _, p := range pkgs {
		for _, t := range p.Targets {
			if t.Instance != "" {
				f.SetOwner(p.Project, t.Repo, t.Instance)
			}
		}
	}
}

func (f *Fleet) ownerFor(ctx context.Context, project, repo, pkg string) (*Instance, error) {
	if in := f.Owner(project, repo); in != nil {
		return in, nil
	}
	if len(f.instances) == 1 {
		return f.instances[0], nil
	}
	if hs := f.hosts(project); len(hs) == 1 {
		return hs[0], nil
	}
	if pkg != "" {
		_, _, _ = f.PackageBuildResults(ctx, project, pkg)
	} else {
		_, _, _, _ = f.BuildResults(ctx, project)
	}
	if in := f.Owner(project, repo); in != nil {
		return in, nil
	}
	return nil, fmt.Errorf("no OBS instance owns repo %s of %s", repo, project)
}

// ── fan-out helpers ──

type instResult[T any] struct {
	inst *Instance
	val  T
	err  error
}

func fanOut[T any](insts []*Instance, fn func(*Instance) (T, error)) []instResult[T] {
	out := make([]instResult[T], len(insts))
	if len(insts) == 1 {
		v, err := fn(insts[0])
		out[0] = instResult[T]{insts[0], v, err}
		return out
	}
	var wg sync.WaitGroup
	for i, in := range insts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := fn(in)
			out[i] = instResult[T]{in, v, err}
		}()
	}
	wg.Wait()
	return out
}

// settle records health and splits results into successes and the slugs of
// failed instances. 404 means "not hosted there" and counts as neither. err
// is non-nil only when nothing succeeded: the joined failures, or the
// NotFound error when every host answered 404.
func settle[T any](res []instResult[T]) (ok []instResult[T], failed []string, err error) {
	var errs []error
	var notFound error
	for _, r := range res {
		r.inst.observe(r.err)
		switch {
		case r.err == nil:
			ok = append(ok, r)
		case IsNotFound(r.err):
			notFound = r.err
		default:
			failed = append(failed, r.inst.Slug)
			errs = append(errs, fmt.Errorf("%s: %w", r.inst.Slug, r.err))
		}
	}
	if len(ok) == 0 {
		if len(errs) > 0 {
			err = errors.Join(errs...)
		} else {
			err = notFound
		}
	}
	return ok, failed, err
}

// ── merged calls ──

type buildResultsVal struct {
	states     []PackageBuildState
	repoStates map[string]string
}

// BuildResults merges one project's _result across hosting instances.
// failed lists instances that errored (their targets must be carried forward).
func (f *Fleet) BuildResults(ctx context.Context, project string) ([]PackageBuildState, map[string]string, []string, error) {
	res := fanOut(f.hosts(project), func(in *Instance) (buildResultsVal, error) {
		s, r, err := in.Client.BuildResults(ctx, in.ToInstance(project))
		return buildResultsVal{s, r}, err
	})
	ok, failed, err := settle(res)
	if err != nil {
		return nil, nil, failed, err
	}
	var states []PackageBuildState
	repoStates := map[string]string{}
	for _, r := range ok {
		states = append(states, f.stamp(project, r.inst, r.val.states)...)
		for k, v := range r.val.repoStates {
			repoStates[k] = v
			if repo, _, found := cutLast(k, "/"); found {
				f.SetOwner(project, repo, r.inst.Slug)
			}
		}
	}
	return states, repoStates, failed, nil
}

// PackageBuildResults merges one package's build states across instances.
func (f *Fleet) PackageBuildResults(ctx context.Context, project, pkg string) ([]PackageBuildState, []string, error) {
	res := fanOut(f.hosts(project), func(in *Instance) ([]PackageBuildState, error) {
		return in.Client.PackageBuildResults(ctx, in.ToInstance(project), pkg)
	})
	ok, failed, err := settle(res)
	if err != nil {
		return nil, failed, err
	}
	var states []PackageBuildState
	for _, r := range ok {
		states = append(states, f.stamp(project, r.inst, r.val)...)
	}
	return states, failed, nil
}

func (f *Fleet) stamp(project string, in *Instance, states []PackageBuildState) []PackageBuildState {
	out := make([]PackageBuildState, len(states))
	for i, s := range states {
		s.Project = project
		s.Instance = in.Slug
		f.SetOwner(project, s.Repo, in.Slug)
		out[i] = s
	}
	return out
}

func cutLast(s, sep string) (before, after string, found bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

func mergeMaps(ok []instResult[map[string]string]) map[string]string {
	out := map[string]string{}
	for _, r := range ok {
		for k, v := range r.val {
			out[k] = v
		}
	}
	return out
}

func (f *Fleet) RepoPublishStates(ctx context.Context, project, pkg string) (map[string]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) (map[string]string, error) {
		return in.Client.RepoPublishStates(ctx, in.ToInstance(project), pkg)
	}))
	if err != nil {
		return nil, err
	}
	return mergeMaps(ok), nil
}

func (f *Fleet) PackageBlockedReasons(ctx context.Context, project, pkg string) (map[string]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) (map[string]string, error) {
		return in.Client.PackageBlockedReasons(ctx, in.ToInstance(project), pkg)
	}))
	if err != nil {
		return nil, err
	}
	return mergeMaps(ok), nil
}

// ProjectBinaryList merges binary lists, translating each binary's project
// to its logical name and recording repo owners.
func (f *Fleet) ProjectBinaryList(ctx context.Context, project string) ([]BinaryArtifact, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) ([]BinaryArtifact, error) {
		return in.Client.ProjectBinaryList(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return nil, err
	}
	var out []BinaryArtifact
	for _, r := range ok {
		for _, b := range r.val {
			if l, isIn := r.inst.ToLogical(b.Project); isIn {
				b.Project = l
			} else {
				b.Project = project
			}
			b.Instance = r.inst.Slug
			f.SetOwner(b.Project, b.Repo, r.inst.Slug)
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *Fleet) ProjectRepos(ctx context.Context, project string) ([]string, error) {
	ok, _, err := settle(fanOut(f.hosts(project), func(in *Instance) ([]string, error) {
		return in.Client.ProjectRepos(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return nil, err
	}
	return unionStrings(ok, nil), nil
}

// SearchProjects returns the logical names of projects under prefix on
// every instance (deduplicated, first-seen order).
func (f *Fleet) SearchProjects(ctx context.Context, prefix string) ([]string, error) {
	ok, _, err := settle(fanOut(f.instances, func(in *Instance) ([]string, error) {
		return in.Client.SearchProjects(ctx, in.ToInstance(prefix))
	}))
	if err != nil {
		return nil, err
	}
	return unionStrings(ok, func(in *Instance, p string) (string, bool) { return in.ToLogical(p) }), nil
}

func unionStrings(ok []instResult[[]string], translate func(*Instance, string) (string, bool)) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range ok {
		for _, s := range r.val {
			if translate != nil {
				var in bool
				if s, in = translate(r.inst, s); !in {
					continue
				}
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// Discover lists every instance's projects under its root, rebuilds
// membership from the instances that answered, and returns the union of
// logical names plus the set of instances that answered. It errors only
// when no instance answered.
func (f *Fleet) Discover(ctx context.Context) ([]string, map[string]bool, error) {
	res := fanOut(f.instances, func(in *Instance) ([]string, error) {
		return in.Client.SearchProjects(ctx, in.Root)
	})
	answered := map[string]bool{}
	seen := map[string]bool{}
	var out []string
	var errs []error
	for _, r := range res {
		r.inst.observe(r.err)
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.inst.Slug, r.err))
			continue
		}
		answered[r.inst.Slug] = true
		var logical []string
		for _, p := range r.val {
			l, in := r.inst.ToLogical(p)
			if !in {
				continue
			}
			logical = append(logical, l)
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
		f.SetMembers(r.inst.Slug, logical)
	}
	if len(answered) == 0 {
		return nil, nil, errors.Join(errs...)
	}
	return out, answered, nil
}

// ── routed calls (repo owner) ──

func (f *Fleet) Rebuild(ctx context.Context, project, repo, arch, pkg string) error {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return err
	}
	err = in.Client.Rebuild(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return err
}

func (f *Fleet) PackageBuildReason(ctx context.Context, project, repo, arch, pkg string) (BuildReasonResult, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return BuildReasonResult{}, err
	}
	r, err := in.Client.PackageBuildReason(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return r, err
}

func (f *Fleet) PackageContainerInfoFilename(ctx context.Context, project, repo, arch, pkg string) (string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return "", err
	}
	s, err := in.Client.PackageContainerInfoFilename(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return s, err
}

func (f *Fleet) PackageContainerTags(ctx context.Context, project, repo, arch, pkg, filename string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	tags, err := in.Client.PackageContainerTags(ctx, in.ToInstance(project), repo, arch, pkg, filename)
	in.observe(err)
	return tags, err
}

func (f *Fleet) PackageBinaries(ctx context.Context, project, repo, arch, pkg string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	out, err := in.Client.PackageBinaries(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return out, err
}

func (f *Fleet) RepoBinaryVersions(ctx context.Context, project, repo, arch string) (map[string]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.RepoBinaryVersions(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

func (f *Fleet) BuildLog(ctx context.Context, project, repo, arch, pkg string, tailBytes int) (string, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return "", err
	}
	out, err := in.Client.BuildLog(ctx, in.ToInstance(project), repo, arch, pkg, tailBytes)
	in.observe(err)
	return out, err
}

func (f *Fleet) PackageHistory(ctx context.Context, project, repo, arch, pkg string) ([]HistoryEntry, error) {
	in, err := f.ownerFor(ctx, project, repo, pkg)
	if err != nil {
		return nil, err
	}
	out, err := in.Client.PackageHistory(ctx, in.ToInstance(project), repo, arch, pkg)
	in.observe(err)
	return out, err
}

func (f *Fleet) BuildDepInfo(ctx context.Context, project, repo, arch string) ([]DepInfo, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.BuildDepInfo(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

func (f *Fleet) ProjectRepoArchs(ctx context.Context, project, repo string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.ProjectRepoArchs(ctx, in.ToInstance(project), repo)
	in.observe(err)
	return out, err
}

func (f *Fleet) ProjectRepoPackages(ctx context.Context, project, repo, arch string) ([]string, error) {
	in, err := f.ownerFor(ctx, project, repo, "")
	if err != nil {
		return nil, err
	}
	out, err := in.Client.ProjectRepoPackages(ctx, in.ToInstance(project), repo, arch)
	in.observe(err)
	return out, err
}

// ── first hosting instance wins ──

func (f *Fleet) PackageIsContainer(ctx context.Context, project, pkg string) (bool, error) {
	var lastErr error
	for _, in := range f.hosts(project) {
		v, err := in.Client.PackageIsContainer(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err == nil {
			return v, nil
		}
		lastErr = err
	}
	return false, lastErr
}

// PackageVersionResult returns the first non-empty versrel in config order.
func (f *Fleet) PackageVersionResult(ctx context.Context, project, pkg string) (string, error) {
	var lastErr error
	succeeded := false
	for _, in := range f.hosts(project) {
		v, err := in.Client.PackageVersionResult(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err != nil {
			lastErr = err
			continue
		}
		succeeded = true
		if v != "" {
			return v, nil
		}
	}
	if succeeded {
		return "", nil
	}
	return "", lastErr
}

func (f *Fleet) SourceHistory(ctx context.Context, project, pkg string) ([]SourceCommit, error) {
	var lastErr error
	for _, in := range f.hosts(project) {
		v, err := in.Client.SourceHistory(ctx, in.ToInstance(project), pkg)
		in.observe(err)
		if err == nil {
			return v, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ── publish flags ──

// ProjectPublishFlags returns flags whose Publishes(repo) follows the repo
// owner's instance. Any non-404 failure errors (callers then treat flags
// as unknown and keep polling).
func (f *Fleet) ProjectPublishFlags(ctx context.Context, project string) (PublishFlags, error) {
	hs := f.hosts(project)
	if len(hs) == 1 {
		fl, err := hs[0].Client.ProjectPublishFlags(ctx, hs[0].ToInstance(project))
		hs[0].observe(err)
		return fl, err
	}
	ok, failed, err := settle(fanOut(hs, func(in *Instance) (PublishFlags, error) {
		return in.Client.ProjectPublishFlags(ctx, in.ToInstance(project))
	}))
	if err != nil {
		return PublishFlags{}, err
	}
	if len(failed) > 0 {
		return PublishFlags{}, fmt.Errorf("publish flags unavailable on %v", failed)
	}
	delegate := make(map[string]PublishFlags, len(ok))
	for _, r := range ok {
		delegate[r.inst.Slug] = r.val
	}
	return PublishFlags{
		delegate: delegate,
		owner: func(repo string) string {
			if in := f.Owner(project, repo); in != nil {
				return in.Slug
			}
			return ""
		},
	}, nil
}

// EvictPublishFlags drops project's cached flags on every instance.
func (f *Fleet) EvictPublishFlags(project string) {
	for _, in := range f.instances {
		if in.Client != nil {
			in.Client.EvictPublishFlags(in.ToInstance(project))
		}
	}
}

// ── metrics ──

// MetricsSnapshot sums per-operation request counts across instances.
func (f *Fleet) MetricsSnapshot() map[string]int64 {
	out := map[string]int64{}
	for _, in := range f.instances {
		if in.Client == nil {
			continue
		}
		for k, v := range in.Client.MetricsSnapshot() {
			out[k] += v
		}
	}
	return out
}

// LimiterStats sums limiter gauges; enabled when any instance limits.
func (f *Fleet) LimiterStats() LimiterStats {
	var out LimiterStats
	for _, in := range f.instances {
		if in.Client == nil {
			continue
		}
		ls := in.Client.LimiterStats()
		if !ls.Enabled {
			continue
		}
		out.Enabled = true
		out.Budget += ls.Budget
		out.Remaining += ls.Remaining
		out.Waits += ls.Waits
	}
	return out
}

func (f *Fleet) RatePerSecond() float64 {
	var total float64
	for _, in := range f.instances {
		if in.Client != nil {
			total += in.Client.RatePerSecond()
		}
	}
	return total
}

// InstanceStatus is one instance's request totals, limiter and health.
type InstanceStatus struct {
	Name    string       `json:"name"`
	Slug    string       `json:"slug"`
	Total   int64        `json:"total"`
	ReqPerS float64      `json:"req_per_s"`
	Limiter LimiterStats `json:"limiter"`
	Health  Health       `json:"health"`
}

func (f *Fleet) Statuses() []InstanceStatus {
	out := make([]InstanceStatus, 0, len(f.instances))
	for _, in := range f.instances {
		st := InstanceStatus{Name: in.Name, Slug: in.Slug, Health: in.Health()}
		if in.Client != nil {
			for _, v := range in.Client.MetricsSnapshot() {
				st.Total += v
			}
			st.ReqPerS = in.Client.RatePerSecond()
			st.Limiter = in.Client.LimiterStats()
		}
		out = append(out, st)
	}
	return out
}

// ── health watch ──

func (f *Fleet) healthBaseline() map[string]bool {
	last := make(map[string]bool, len(f.instances))
	for _, in := range f.instances {
		last[in.Slug] = in.Health().OK
	}
	return last
}

func (f *Fleet) checkHealth(last map[string]bool, notify func(slug string, h Health)) {
	for _, in := range f.instances {
		h := in.Health()
		if h.OK != last[in.Slug] {
			last[in.Slug] = h.OK
			notify(in.Slug, h)
		}
	}
}

// RunHealthWatch checks every instance's health each interval and calls
// notify when an instance's OK flips. Blocks until ctx is cancelled.
func (f *Fleet) RunHealthWatch(ctx context.Context, every time.Duration, notify func(slug string, h Health)) {
	last := f.healthBaseline()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.checkHealth(last, notify)
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/obs/ -v`
Expected: PASS (Fleet tests and all existing obs tests).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/obs/fleet.go backend/internal/obs/fleet_test.go backend/internal/obs/client.go
git commit -s -m "feat(obs): Fleet fans OBS calls out across instances"
```

---

### Task 4: Instance on targets/events, carry-forward and instance-share helpers

**Goal:** `model.Target`/`model.Event` carry the instance slug, `events.instance` is stored, `buildPackage` stamps target instances, and pure helpers `CarryForward`/`WithoutInstance` plus `store.QueryProjectPackages` exist for later tasks.

**Files:**
- Modify: `backend/internal/model/types.go`, `backend/internal/store/db.go`, `backend/internal/store/events.go`, `backend/internal/obs/poller.go` (`buildPackage` only)
- Create: `backend/internal/obs/carry.go`, `backend/internal/store/instances.go`
- Test: `backend/internal/obs/carry_test.go`, `backend/internal/store/instances_test.go`, `backend/internal/store/events_test.go`

**Acceptance Criteria:**
- [ ] `Target.Instance` round-trips through `targets_json`; `Event.Instance` round-trips through `AppendEvent`/`QueryEvents`/`QueryEventsAny`/`QueryPRBuildEvents`; old DBs gain the nullable `events.instance` column.
- [ ] `buildPackage` copies `PackageBuildState.Instance` to each target.
- [ ] `CarryForward` appends previous targets owned by failed instances only; no-op when `failed` is empty.
- [ ] `WithoutInstance` drops the slug's targets and unstamped targets, recomputes rollup/counts, keeps other targets' enrichment.
- [ ] `QueryProjectPackages(db, "ppg:17")` returns exact-project packages only (not `ppg:17:containers`).

**Verify:** `cd backend && go test ./internal/obs/ ./internal/store/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/obs/carry_test.go`:

```go
package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestCarryForward(t *testing.T) {
	fresh := []PackageBuildState{{Repo: "RHEL_9", Arch: "x86_64", State: "building", Instance: "x"}}
	prev := []model.Target{
		{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "x"},
		{Repo: "Debian_12", Arch: "aarch64", State: "failed", Details: "oops", Instance: "y"},
	}
	if got := CarryForward(fresh, prev, nil, "p", "pkg"); len(got) != 1 {
		t.Fatalf("no failures must be a no-op, got %+v", got)
	}
	got := CarryForward(fresh, prev, []string{"y"}, "p", "pkg")
	if len(got) != 2 {
		t.Fatalf("want fresh + carried, got %+v", got)
	}
	c := got[1]
	if c.Repo != "Debian_12" || c.State != "failed" || c.Details != "oops" || c.Instance != "y" || c.Package != "pkg" {
		t.Errorf("carried state wrong: %+v", c)
	}
}

func TestWithoutInstance(t *testing.T) {
	pkg := &model.Package{
		Project: "ppg:17", Name: "pg", RollupState: model.RollupFailed, TotalTargets: 3,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "failed", Instance: "x"},
			{Repo: "Debian_12", Arch: "aarch64", State: "succeeded", Instance: "y", Published: true},
			{Repo: "Old", Arch: "x86_64", State: "failed"},
		},
	}
	out := WithoutInstance(pkg, "x")
	if len(out.Targets) != 1 || out.Targets[0].Repo != "Debian_12" || !out.Targets[0].Published {
		t.Fatalf("targets = %+v", out.Targets)
	}
	if out.RollupState != model.RollupSucceeded || out.OKTargets != 1 || out.TotalTargets != 1 {
		t.Errorf("rollup/counts = %s %d/%d", out.RollupState, out.OKTargets, out.TotalTargets)
	}
	if len(pkg.Targets) != 3 {
		t.Error("input package must not be mutated")
	}
}

func TestBuildPackageStampsInstance(t *testing.T) {
	p := buildPackage("ppg:17", "pg", nil, []PackageBuildState{{Repo: "R", Arch: "a", State: "succeeded", Instance: "y"}})
	if p.Targets[0].Instance != "y" {
		t.Errorf("instance not stamped: %+v", p.Targets[0])
	}
}
```

Create `backend/internal/store/instances_test.go`:

```go
package store

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestQueryProjectPackagesExact(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	for _, p := range []*model.Package{
		{Project: "ppg:17", Name: "a", RollupState: model.RollupSucceeded, UpdatedAt: now,
			Targets: []model.Target{{Repo: "R", Arch: "x", State: "succeeded", Instance: "y"}}},
		{Project: "ppg:17:containers", Name: "b", RollupState: model.RollupSucceeded, UpdatedAt: now},
	} {
		if err := UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := QueryProjectPackages(db, "ppg:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Targets[0].Instance != "y" {
		t.Errorf("target instance lost in targets_json: %+v", got[0].Targets[0])
	}
}
```

Append to `backend/internal/store/events_test.go`:

```go
func TestEventInstanceRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	evt := &model.Event{ID: "evt_1", Type: model.EventFailed, Project: "PR:pr-1:ppg:17", Package: "pg",
		Repo: "R", Arch: "x", What: "w", Why: "", URL: "u", At: now, Instance: "percona"}
	if err := AppendEvent(db, evt); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]func() ([]*model.Event, error){
		"QueryEvents":    func() ([]*model.Event, error) { return QueryEvents(db, "PR:", now.Add(-time.Minute), now.Add(time.Minute)) },
		"QueryEventsAny": func() ([]*model.Event, error) { return QueryEventsAny(db, []string{"PR:"}, now.Add(-time.Minute), now.Add(time.Minute)) },
	} {
		got, err := query()
		if err != nil || len(got) != 1 || got[0].Instance != "percona" {
			t.Errorf("%s: got %+v err %v", name, got, err)
		}
	}
}
```

(If `events_test.go` lacks the `model`/`time` imports, add them.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/obs/ ./internal/store/ -run 'CarryForward|WithoutInstance|StampsInstance|ProjectPackagesExact|EventInstance' -v`
Expected: FAIL — unknown field `Instance`, undefined `CarryForward`, `QueryProjectPackages`.

- [ ] **Step 3: Implement**

In `backend/internal/model/types.go` add to `Target` (after `Published`):

```go
	// Instance is the slug of the OBS instance that builds this target.
	Instance string `json:"instance,omitempty"`
```
and to `Event` (after `URL`):
```go
	// Instance is the slug of the OBS instance the event came from; empty for
	// logical events (e.g. CVE scans).
	Instance string `json:"instance,omitempty"`
```

In `backend/internal/store/db.go`: add `instance TEXT` as the last column of the `events` table in `schema`, and add to the additive migrations block:
```go
	db.Exec(`ALTER TABLE events ADD COLUMN instance TEXT`)
```

In `backend/internal/store/events.go`:
- `AppendEvent`: insert column list `(id, type, tags, project, package, repo, arch, what, why, url, at, version, instance)`, 13 placeholders, extra arg `nullStr(e.Instance)`.
- In the three SELECTs (`QueryEvents`, `QueryEventsAny`, `QueryPRBuildEvents`) append `, COALESCE(instance,'')` after `COALESCE(version,'')`.
- In `scanEventRows` and the scan loop in `QueryPRBuildEvents`, append `&e.Instance` after `&e.Version`.

Create `backend/internal/store/instances.go`:

```go
package store

import (
	"database/sql"

	"github.com/percona/obs-dashboard/internal/model"
)

// QueryProjectPackages returns the packages of exactly project (no
// subprojects). Used for instance-scoped project deletion.
func QueryProjectPackages(db *sql.DB, project string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? ORDER BY name`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
```

In `backend/internal/obs/poller.go` `buildPackage`, change the target construction to:
```go
		mTargets[i] = model.Target{Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details, Instance: t.Instance}
```

Create `backend/internal/obs/carry.go`:

```go
package obs

import (
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// CarryForward returns fresh plus, for every instance in failed, the
// previous targets that instance owned, converted back to build states. An
// instance outage therefore freezes its targets at their last known state
// instead of dropping them.
func CarryForward(fresh []PackageBuildState, prev []model.Target, failed []string, project, pkg string) []PackageBuildState {
	if len(failed) == 0 || len(prev) == 0 {
		return fresh
	}
	down := make(map[string]bool, len(failed))
	for _, s := range failed {
		down[s] = true
	}
	out := append([]PackageBuildState(nil), fresh...)
	for _, t := range prev {
		if t.Instance != "" && down[t.Instance] {
			out = append(out, PackageBuildState{
				Project: project, Package: pkg,
				Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details,
				Instance: t.Instance,
			})
		}
	}
	return out
}

// WithoutInstance returns a copy of pkg without the targets owned by slug —
// and without unstamped (pre-migration) targets — with rollup and counts
// recomputed. Remaining targets keep their enrichment.
func WithoutInstance(pkg *model.Package, slug string) *model.Package {
	var kept []model.Target
	var states []PackageBuildState
	for _, t := range pkg.Targets {
		if t.Instance == slug || t.Instance == "" {
			continue
		}
		kept = append(kept, t)
		states = append(states, PackageBuildState{Repo: t.Repo, Arch: t.Arch, State: t.State, Details: t.Details, Instance: t.Instance})
	}
	rebuilt := buildPackage(pkg.Project, pkg.Name, pkg.Tags, states)
	out := *pkg
	out.Targets = kept
	out.RollupState = rebuilt.RollupState
	out.OKTargets = rebuilt.OKTargets
	out.TotalTargets = rebuilt.TotalTargets
	out.UpdatedAt = time.Now().UTC()
	return &out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./... `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/model backend/internal/store backend/internal/obs/carry.go backend/internal/obs/carry_test.go backend/internal/obs/poller.go
git commit -s -m "feat(model): instance on targets and events, carry-forward helpers"
```

---

### Task 5: Task chain and worker on Fleet with carry-forward

**Goal:** `worker.Task` and every `obs.*Task` take `*obs.Fleet`; `BuildStateTask` carries forward failed instances' targets (keeping their `Published` flag); the worker's batch fetch passes failed instances through `Env`; build events carry the instance and instance-correct URLs; `main` wraps the client in an identity `SingleFleet`.

**Files:**
- Modify: `backend/internal/obs/tasks.go`, `backend/internal/obs/env.go`, `backend/internal/obs/trigger.go`, `backend/internal/worker/worker.go`, `backend/cmd/obsboard/main.go`
- Test: `backend/internal/obs/tasks_test.go`, `backend/internal/worker/worker_test.go`

**Acceptance Criteria:**
- [ ] `worker.Task.Run(ctx, *obs.Fleet, *model.Package, *obs.Env)`; all tasks compile against Fleet.
- [ ] `BuildStateTask` with a failed instance keeps that instance's previous targets (state, details, enrichment, `Published`) and recomputes rollup over all targets.
- [ ] `Env.FailedInstances` is honoured when `Env.BuildStates` is used.
- [ ] Build events set `Event.Instance = target.Instance` and use `Instance.LiveLogURL`/`PackageURL`; with a nil fleet they fall back to the openSUSE URLs (existing worker tests unchanged in expectations).
- [ ] `main.go` builds `fleet := obs.SingleFleet(obsClient, cfg.OBSRoot)`, seeds repo owners, passes fleet to the pool and the unblocker.

**Verify:** `cd backend && go test ./... ` → PASS

**Steps:**

- [ ] **Step 1: Migrate existing tests mechanically**

```bash
cd backend
sed -i -E 's/obs\.NewClient\(([^()]*)\)/obs.SingleFleet(obs.NewClient(\1), "isv:percona")/g' internal/obs/tasks_test.go internal/worker/worker_test.go
sed -i 's/\*obs\.Client/*obs.Fleet/g' internal/worker/worker_test.go
```

Check `grep -n "SingleFleet" internal/obs/tasks_test.go | head` — every task test now passes a Fleet. If a test calls a Client-only method on the value (e.g. `c.SetMinuteBudget`), change it to `c.Instances()[0].Client.SetMinuteBudget(...)`.

- [ ] **Step 2: Write the failing carry-forward test** — append to `backend/internal/obs/tasks_test.go`:

```go
func TestBuildStateTaskCarriesForwardFailedInstance(t *testing.T) {
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg", CacheWarm: true,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse", Published: true},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona", Details: "boom", BuildReason: "source change"},
		},
	}
	env := &obs.Env{
		BuildStates:     []obs.PackageBuildState{{Project: pkg.Project, Package: "pg", Repo: "RHEL_9", Arch: "x86_64", State: "building", Instance: "opensuse"}},
		FailedInstances: []string{"percona"},
	}
	if err := (obs.BuildStateTask{}).Run(context.Background(), nil, pkg, env); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Targets) != 2 {
		t.Fatalf("carried target dropped: %+v", pkg.Targets)
	}
	var deb model.Target
	for _, tg := range pkg.Targets {
		if tg.Repo == "Debian_12" {
			deb = tg
		}
	}
	if deb.State != "failed" || deb.Instance != "percona" || deb.BuildReason != "source change" {
		t.Errorf("carried target lost state/enrichment: %+v", deb)
	}
	if pkg.RollupState != model.RollupFailed {
		t.Errorf("rollup must include carried targets, got %s", pkg.RollupState)
	}
}

func TestBuildStateTaskKeepsPublishedOnCarriedTarget(t *testing.T) {
	pkg := &model.Package{
		Project: "isv:percona:ppg:17", Name: "pg",
		Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", State: "succeeded", Instance: "percona", Published: true}},
	}
	env := &obs.Env{BuildStates: []obs.PackageBuildState{}, FailedInstances: []string{"percona"}}
	if err := (obs.BuildStateTask{}).Run(context.Background(), nil, pkg, env); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Targets) != 1 || !pkg.Targets[0].Published {
		t.Fatalf("carried target must keep Published: %+v", pkg.Targets)
	}
}
```

Run: `cd backend && go test ./internal/obs/ -run CarriesForward -v` → FAIL (`unknown field FailedInstances`).

- [ ] **Step 3: Implement**

`backend/internal/obs/env.go` — add to `Env`:
```go
	// FailedInstances lists instances whose project-level fetch failed; their
	// previous targets are carried forward by BuildStateTask.
	FailedInstances []string
```

`backend/internal/obs/tasks.go` — change every `client *Client` parameter to `client *Fleet`. Then:

`BuildStateTask.Run` top becomes:
```go
	var results []PackageBuildState
	var failed []string
	if env != nil && env.BuildStates != nil {
		results = env.BuildStates
		failed = env.FailedInstances
	} else {
		var err error
		results, failed, err = client.PackageBuildResults(ctx, pkg.Project, pkg.Name)
		if err != nil {
			return err
		}
	}
	results = CarryForward(results, pkg.Targets, failed, pkg.Project, pkg.Name)
	down := make(map[string]bool, len(failed))
	for _, s := range failed {
		down[s] = true
	}
	updated := buildPackage(pkg.Project, pkg.Name, pkg.Tags, results)
```
and inside the `old.State == updated.Targets[i].State` branch add:
```go
					if down[updated.Targets[i].Instance] {
						// Carried forward: the instance is unreachable, so its
						// publish state can't be re-read this pass.
						updated.Targets[i].Published = old.Published
					}
```

`ContainerTagsTask.Run` release fallback: `results, _, err := client.PackageBuildResults(ctx, pkg.Project, pkg.Name)`; when appending the fallback target include `Instance: r.Instance`.

`backend/internal/obs/trigger.go`: `func InferTrigger(ctx context.Context, c *Fleet, pkg *model.Package) *model.Trigger` (body unchanged; the method names exist on Fleet).

`backend/internal/worker/worker.go`:
- `Task.Run(ctx context.Context, client *obs.Fleet, pkg *model.Package, env *obs.Env) error`; `Pool.client *obs.Fleet`; `NewPool(..., client *obs.Fleet, ...)`.
- `ProcessJob`: `results, repoStates, failed, err := p.client.BuildResults(ctx, job.Project)` and `envs[pkg.Name] = &obs.Env{BuildStates: states, RepoStates: repoStates, FailedInstances: failed}`. A package absent from `byPkg` but with targets on a failed instance must still get an env so its carry-forward happens: after the `for _, pkg := range job.Pkgs` env loop add
  ```go
  			for _, pkg := range job.Pkgs {
  				if _, ok := envs[pkg.Name]; !ok && len(failed) > 0 {
  					envs[pkg.Name] = &obs.Env{BuildStates: []obs.PackageBuildState{}, RepoStates: repoStates, FailedInstances: failed}
  				}
  			}
  ```
- Delete `const obsBase = "https://build.opensuse.org"`. In `emitBuildEvents`, at the top of the target loop add `inst := p.client.InstanceOrDefault(t.Instance)`; replace every `fmt.Sprintf("%s/package/live_build_log/%s/%s/%s/%s", obsBase, …)` with `inst.LiveLogURL(pkg.Project, pkg.Name, t.Repo, t.Arch)` and every `fmt.Sprintf("%s/package/show/%s/%s", obsBase, pkg.Project, pkg.Name)` with `inst.PackageURL(pkg.Project, pkg.Name)`; add `Instance: t.Instance,` to each `model.Event` literal there.

`backend/cmd/obsboard/main.go`:
```go
	obsClient := obs.NewClient(cfg.OBS.BaseURL, cfg.OBS.Username, cfg.OBS.Password)
	obsClient.SetMinuteBudget(cfg.OBS.MinuteRequestBudget)
	fleet := obs.SingleFleet(obsClient, cfg.OBSRoot)
```
after `activePkgs` is loaded: `fleet.SeedOwners(activePkgs)`; `worker.NewPool(..., fleet, db, h, ws, scanner)`; unblocker `Rebuilder: fleet`. Poller, consumer, router, sampler and telemetry keep `obsClient` for now.

- [ ] **Step 4: Run tests**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/obs backend/internal/worker backend/cmd/obsboard/main.go
git commit -s -m "refactor(worker): task chain on Fleet with instance carry-forward"
```

---

### Task 6: Poller on Fleet — per-instance discovery and instance-aware GC

**Goal:** The poller discovers projects on every instance through `Fleet.Discover`, fetches merged build results, carries forward failed instances' targets, and garbage-collects packages/projects only on evidence from instances that answered.

**Files:**
- Modify: `backend/internal/obs/poller.go`, `backend/cmd/obsboard/main.go`
- Test: `backend/internal/obs/poller_test.go`

**Acceptance Criteria:**
- [ ] `NewPoller(fleet *Fleet, db, interval, h, ws, root, gate)`; `discoverProjects` removed.
- [ ] A stored package whose only targets belong to a failed instance survives the tick unchanged (carried forward).
- [ ] A stored project absent from discovery is deleted only when every instance owning its targets answered discovery (unstamped targets require all instances).
- [ ] Existing poller tests pass with `SingleFleet`.

**Verify:** `cd backend && go test ./internal/obs/ -run Poll -v` → PASS

**Steps:**

- [ ] **Step 1: Migrate existing tests**

In `backend/internal/obs/poller_test.go` replace `NewClient(srv.URL, "u", "p")` constructions passed to the poller with `SingleFleet(NewClient(srv.URL, "u", "p"), "isv:percona")` (the struct literal at ~line 214 sets `client:`; wrap it the same way).

- [ ] **Step 2: Write the failing tests** — append to `backend/internal/obs/poller_test.go`:

```go
func TestConfirmedGone(t *testing.T) {
	x := LegacyInstance(nil, "isv:percona")
	y := NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, nil)
	all := []*Instance{x, y}
	existing := []*model.Package{
		{Project: "ppg:17", Targets: []model.Target{{Repo: "D", Arch: "a", Instance: "percona"}}},
		{Project: "ppg:18", Targets: []model.Target{{Repo: "R", Arch: "a"}}},
	}
	if confirmedGone(existing, "ppg:17", map[string]bool{"opensuse": true}, all) {
		t.Error("ppg:17 lives on percona, which did not answer")
	}
	if !confirmedGone(existing, "ppg:17", map[string]bool{"opensuse": true, "percona": true}, all) {
		t.Error("all owners answered: gone")
	}
	if confirmedGone(existing, "ppg:18", map[string]bool{"opensuse": true}, all) {
		t.Error("unstamped targets need every instance to answer")
	}
}

func TestPollerCarriesForwardFailedInstance(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	x := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/project/id"):
			w.Write([]byte(`<collection><project name="isv:percona:ppg:17"/></collection>`))
		case r.URL.Path == "/build/isv:percona:ppg:17/_result":
			w.Write([]byte(`<resultlist><result repository="RHEL_9" arch="x86_64" state="building"><status package="pg" code="building"/></result></resultlist>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer x.Close()
	y := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/project/id") {
			w.Write([]byte(`<collection><project name="percona:ppg:17"/></collection>`))
			return
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer y.Close()
	fleet := NewFleet(
		NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(x.URL, "u", "p")),
		NewInstance(InstanceInfo{Slug: "percona", Root: "percona"}, NewClient(y.URL, "u", "p")),
	)
	now := time.Now().UTC()
	stored := &model.Package{Project: "ppg:17", Name: "deb-only", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"}}}
	if err := store.UpsertPackageState(db, stored, now); err != nil {
		t.Fatal(err)
	}
	ws := workingset.New(64, time.Minute, time.Minute, 4)
	p := NewPoller(fleet, db, time.Minute, hubpkg.New(), ws, "", nil)
	p.tick(context.Background())

	got, err := store.GetPackage(db, "ppg:17", "deb-only")
	if err != nil || got == nil {
		t.Fatalf("package on the failed instance was garbage-collected (err=%v)", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].State != "failed" {
		t.Errorf("carried package changed: %+v", got.Targets)
	}
}
```

Note: until Task 9 the classifier still needs a root, and `Classify("", "ppg:17")` is `KindUnknown`, so today this test passes without exercising carry-forward (the project is skipped, and project-level GC keeps it because discovery reports it live). It is written now so Task 9 — which makes `Classify` root-free and drops the `""` argument — turns it into a real carry-forward check without edits beyond the constructor call. Add the imports `hubpkg "github.com/percona/obs-dashboard/internal/hub"` and `"github.com/percona/obs-dashboard/internal/workingset"` if absent.

Run: `cd backend && go test ./internal/obs/ -run 'ConfirmedGone|PollerCarries' -v` → FAIL (undefined `confirmedGone`; `NewPoller` signature).

- [ ] **Step 3: Implement** — in `backend/internal/obs/poller.go`:

- `Poller.client *Fleet`; `NewPoller(client *Fleet, db *sql.DB, …)`.
- `fetchProjectResults` returns `([]PackageBuildState, map[string]string, []string, error)` via `p.client.BuildResults(Interactive(ctx), project)`.
- Delete `discoverProjects`.
- Replace the body of `tick` with:

```go
func (p *Poller) tick(ctx context.Context) {
	projects, answered, err := p.client.Discover(ctx)
	if err != nil {
		slog.Error("poller: discover projects", "err", err)
		return
	}

	liveProjects := make(map[string]bool, len(projects))
	for _, proj := range projects {
		liveProjects[proj] = true
	}

	existing, err := store.QueryPackages(p.db, p.root)
	if err != nil {
		slog.Error("poller: query packages", "root", p.root, "err", err)
		return
	}
	byKey := make(map[string]*model.Package, len(existing))
	for _, pkg := range existing {
		byKey[pkg.Project+"/"+pkg.Name] = pkg
	}

	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		kind := Classify(p.root, project)
		if kind == KindUnknown {
			continue
		}

		results, repoStates, failed, err := p.fetchProjectResults(ctx, project)
		if err != nil {
			slog.Warn("poller: build results", "project", project, "err", err)
			continue
		}
		down := make(map[string]bool, len(failed))
		for _, s := range failed {
			down[s] = true
		}

		byPkg := map[string][]PackageBuildState{}
		for _, r := range results {
			byPkg[r.Package] = append(byPkg[r.Package], r)
		}
		// Packages whose targets live on an unreachable instance stay in the
		// pass so their targets are carried forward rather than GC'd.
		for _, stored := range existing {
			if stored.Project != project {
				continue
			}
			if _, ok := byPkg[stored.Name]; ok {
				continue
			}
			for _, t := range stored.Targets {
				if down[t.Instance] {
					byPkg[stored.Name] = nil
					break
				}
			}
		}

		tags := ProjectTags(p.root, project)
		for pkgName, targets := range byPkg {
			key := project + "/" + pkgName
			prev := byKey[key]
			var prevTargets []model.Target
			if prev != nil {
				prevTargets = prev.Targets
			}
			pkg := buildPackage(project, pkgName, tags, CarryForward(targets, prevTargets, failed, project, pkgName))
			pkg.IsRelease = kind == KindRelease
			preservePackageEnrichment(prev, pkg)

			// … unchanged from here: the published-preservation guard,
			// rollupChanged/tagsChanged, the real-time vs release branches.
		}

		// Garbage-collect packages removed from this project in OBS.
		for _, stored := range existing {
			if stored.Project != project {
				continue
			}
			if _, live := byPkg[stored.Name]; !live {
				slog.Info("poller: removing stale package", "project", project, "pkg", stored.Name)
				if err := store.DeletePackage(p.db, project, stored.Name); err != nil {
					slog.Error("poller: delete stale package", "project", project, "pkg", stored.Name, "err", err)
				}
				p.ws.Remove(project + "/" + stored.Name)
			}
		}
	}

	// Garbage-collect packages for projects no longer in OBS — only when
	// every instance that owned them answered discovery.
	storedProjects := make(map[string]bool)
	for _, pkg := range existing {
		storedProjects[pkg.Project] = true
	}
	for proj := range storedProjects {
		if liveProjects[proj] || !confirmedGone(existing, proj, answered, p.client.Instances()) {
			continue
		}
		slog.Info("poller: removing packages for deleted project", "project", proj)
		if err := store.DeletePackagesByProject(p.db, proj); err != nil {
			slog.Error("poller: delete packages", "project", proj, "err", err)
		}
		p.client.EvictPublishFlags(proj)
		for _, pkg := range existing {
			if pkg.Project == proj {
				p.ws.Remove(proj + "/" + pkg.Name)
			}
		}
	}
}

// confirmedGone reports whether every instance that owned a target of
// project answered discovery. Unstamped targets and target-less packages
// require every instance to have answered.
func confirmedGone(existing []*model.Package, project string, answered map[string]bool, all []*Instance) bool {
	allAnswered := len(answered) >= len(all)
	for _, pkg := range existing {
		if pkg.Project != project {
			continue
		}
		if len(pkg.Targets) == 0 && !allAnswered {
			return false
		}
		for _, t := range pkg.Targets {
			if t.Instance == "" {
				if !allAnswered {
					return false
				}
				continue
			}
			if !answered[t.Instance] {
				return false
			}
		}
	}
	return true
}
```

(Keep the body of the `for pkgName, targets := range byPkg` loop after `preservePackageEnrichment` exactly as it is today.)

`backend/cmd/obsboard/main.go`: `poller := obs.NewPoller(fleet, db, cfg.Poller.Interval, h, ws, cfg.OBSRoot, gate)`.

- [ ] **Step 4: Run tests**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/obs/poller.go backend/internal/obs/poller_test.go backend/cmd/obsboard/main.go
git commit -s -m "refactor(poller): per-instance discovery and instance-aware GC"
```

---

### Task 7: One MQ consumer per instance

**Goal:** `mq.Consumer` is built from one `*obs.Instance` (its broker, exchange, routing prefix), translates projects to logical names, stamps target/event instances, updates Fleet membership and repo owners, deletes only its instance's share, and reports MQ connection health.

**Files:**
- Modify: `backend/internal/mq/consumer.go`, `backend/cmd/obsboard/main.go`
- Test: `backend/internal/mq/consumer_test.go`

**Acceptance Criteria:**
- [ ] `NewConsumer(inst *obs.Instance, fleet *obs.Fleet, db, h, ws, root string)`; bindings and matching use `inst.MQRoutingPrefix`; exchange `inst.MQExchange`; dial `inst.MQURL`.
- [ ] Out-of-root projects are dropped via `inst.ToLogical`.
- [ ] Build events stamp `Target.Instance` and record `fleet.SetOwner`.
- [ ] `project.delete` with another instance still hosting the project removes only this instance's targets (rows with no targets left are deleted); when nobody hosts it, all rows go (today's behaviour).
- [ ] `package.delete` removes only this instance's targets.
- [ ] Every emitted event carries `Instance` and an instance-built URL; `SetMQConnected(true/false)` wraps the connection lifetime.
- [ ] A custom routing prefix (`percona.obs.package.build_fail`) is handled.

**Verify:** `cd backend && go test ./internal/mq/ -v` → PASS

**Steps:**

- [ ] **Step 1: Migrate existing tests** — in `backend/internal/mq/consumer_test.go`, every `&Consumer{db: db, root: "isv:percona"}` (and similar literals) gains the instance fields:

```go
	fleet := obs.SingleFleet(nil, "isv:percona")
	consumer := &Consumer{db: db, root: "isv:percona", inst: fleet.Default(), fleet: fleet, prefix: "opensuse.obs"}
```
(keep any `hub:`/`ws:` fields the literal already sets; add the `obs` import).

Add to `backend/internal/obs/fleet.go`:
```go
// Default returns the first configured instance.
func (f *Fleet) Default() *Instance { return f.InstanceOrDefault("") }
```

- [ ] **Step 2: Write the failing tests** — append to `backend/internal/mq/consumer_test.go`:

```go
func twoInstanceFleet() (*obs.Fleet, *obs.Instance, *obs.Instance) {
	x := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
	y := obs.NewInstance(obs.InstanceInfo{Name: "Percona", Slug: "percona", Root: "percona", WebURL: "https://obs.example.com", MQRoutingPrefix: "percona.obs"}, nil)
	return obs.NewFleet(x, y), x, y
}

func TestConsumerCustomPrefixStampsInstance(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, _, y := twoInstanceFleet()
	ws := workingset.New(64, time.Minute, time.Minute, 4)
	c := NewConsumer(y, fleet, db, hubpkg.New(), ws, "percona")

	body, _ := json.Marshal(map[string]string{"project": "percona:ppg:17", "package": "pg", "repository": "Debian_12", "arch": "aarch64"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.package.build_fail", Body: body})

	// y is a non-identity instance: "percona:ppg:17" is stored as "ppg:17".
	got, err := store.GetPackage(db, "ppg:17", "pg")
	if err != nil || got == nil {
		t.Fatalf("package not stored under its logical name (err=%v)", err)
	}
	if got.Targets[0].Instance != "percona" {
		t.Errorf("target instance = %q", got.Targets[0].Instance)
	}
	if fleet.Owner("ppg:17", "Debian_12") == nil {
		t.Error("repo owner not recorded")
	}
}

func TestConsumerDropsOutOfRootProjects(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, _, y := twoInstanceFleet()
	c := NewConsumer(y, fleet, db, hubpkg.New(), workingset.New(64, time.Minute, time.Minute, 4), "percona")
	body, _ := json.Marshal(map[string]string{"project": "home:someone:ppg", "package": "pg", "repository": "R", "arch": "a"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.package.build_fail", Body: body})
	pkgs, _ := store.QueryPackages(db, "")
	if len(pkgs) != 0 {
		t.Fatalf("out-of-root project stored: %+v", pkgs)
	}
}

func TestConsumerProjectDeleteIsInstanceScoped(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fleet, x, y := twoInstanceFleet()
	logical, _ := y.ToLogical("percona:ppg:17") // "ppg:17", hosted by both instances
	fleet.AddMember(x.Slug, logical)
	fleet.AddMember(y.Slug, logical)
	now := time.Now().UTC()
	pkg := &model.Package{Project: logical, Name: "pg", RollupState: model.RollupFailed, UpdatedAt: now,
		Targets: []model.Target{
			{Repo: "RHEL_9", Arch: "x86_64", State: "succeeded", Instance: "opensuse"},
			{Repo: "Debian_12", Arch: "aarch64", State: "failed", Instance: "percona"},
		}}
	if err := store.UpsertPackageState(db, pkg, now); err != nil {
		t.Fatal(err)
	}
	c := NewConsumer(y, fleet, db, hubpkg.New(), workingset.New(64, time.Minute, time.Minute, 4), "percona")
	body, _ := json.Marshal(map[string]string{"project": "percona:ppg:17"})
	c.handle(context.Background(), amqp.Delivery{RoutingKey: "percona.obs.project.delete", Body: body})

	got, _ := store.GetPackage(db, logical, "pg")
	if got == nil || len(got.Targets) != 1 || got.Targets[0].Instance != "opensuse" {
		t.Fatalf("only percona's targets should go: %+v", got)
	}
}
```

(Note: before Task 9 `NewConsumer`'s `root` argument drives `Classify`; with root `"percona"` and a non-identity instance the logical name is `ppg:17`, which `Classify("percona", "ppg:17")` rejects — the build-event path still stores the package because `kind == KindRelease` is the only branch that returns early. Keep assertions on storage/instance/owner only. Task 9 turns these into fully root-free tests.)

Run: `cd backend && go test ./internal/mq/ -v` → FAIL (`NewConsumer` signature, `inst` field).

- [ ] **Step 3: Implement** — rewrite the relevant parts of `backend/internal/mq/consumer.go`:

Constants: delete `exchange`, `packageRouteKey`, `repoRouteKey`, `projectRouteKey`.

```go
// Consumer subscribes to one OBS instance's AMQP bus and updates the store
// on build events.
type Consumer struct {
	inst   *obs.Instance
	fleet  *obs.Fleet
	prefix string
	db     *sql.DB
	hub    *hubpkg.Hub
	ws     *workingset.WorkingSet
	root   string
}

func NewConsumer(inst *obs.Instance, fleet *obs.Fleet, db *sql.DB, h *hubpkg.Hub, ws *workingset.WorkingSet, root string) *Consumer {
	return &Consumer{inst: inst, fleet: fleet, prefix: inst.MQRoutingPrefix, db: db, hub: h, ws: ws, root: root}
}
```

In `run`: `amqp.Dial(c.inst.MQURL)`; `ExchangeDeclarePassive(c.inst.MQExchange, …)`; bind keys `c.prefix+".package.#"`, `c.prefix+".repo.published"`, `c.prefix+".project.#"` on `c.inst.MQExchange`; after `slog.Info("mq: connected", …)` (add `"instance", c.inst.Slug`) call `c.inst.SetMQConnected(true)` and `defer c.inst.SetMQConnected(false)`.

In `handle`, replace the root filter with:

```go
	logical, ok := c.inst.ToLogical(m.Project)
	if !ok {
		return
	}
	m.Project = logical
```
and dispatch on the prefix-stripped key:
```go
	key := strings.TrimPrefix(msg.RoutingKey, c.prefix+".")
	switch {
	case key == "repo.published":
		… (unchanged body)
	case key == "project.create":
		if kind == obs.KindRelease {
			return
		}
		c.fleet.AddMember(c.inst.Slug, m.Project)
		c.appendEvent(&model.Event{
			ID: "evt_" + ulid.Make().String(), Type: model.EventCreated,
			Tags: obs.ProjectTags(c.root, m.Project), Project: m.Project,
			What: fmt.Sprintf("project %s created", m.Project), Why: m.Sender,
			URL: c.inst.ProjectURL(m.Project), At: time.Now().UTC(), Instance: c.inst.Slug,
		})
	case key == "project.delete":
		c.fleet.RemoveMember(c.inst.Slug, m.Project)
		c.inst.Client.EvictPublishFlags(c.inst.ToInstance(m.Project)) // guarded: skip when c.inst.Client == nil
		c.deleteProjectShare(m.Project)
		c.appendEvent(&model.Event{
			ID: "evt_" + ulid.Make().String(), Type: model.EventDeleted,
			Tags: obs.ProjectTags(c.root, m.Project), Project: m.Project,
			What: fmt.Sprintf("project %s deleted", m.Project), Why: m.Comment,
			URL: c.inst.ProjectURL(m.Project), At: time.Now().UTC(), Instance: c.inst.Slug,
		})
	case key == "package.create":
		… (unchanged, plus URL: c.inst.PackageURL(m.Project, m.Package), Instance: c.inst.Slug)
	case key == "package.delete":
		c.deletePackageShare(m.Project, m.Package)
		… event with URL: c.inst.PackageURL(m.Project, m.Package), Instance: c.inst.Slug
	case isPackageBuildEvent(key):
		… (unchanged)
	}
```
Write the `EvictPublishFlags` line as:
```go
		if c.inst.Client != nil {
			c.inst.Client.EvictPublishFlags(c.inst.ToInstance(m.Project))
		}
```

Change `isPackageBuildEvent` / `mqStateToRollup` to match the stripped keys (`"package.build_success"`, `"package.build_fail"`, `"package.build_unchanged"`); update the existing `TestMQStateToRollupUnchangedIsFinished` call accordingly.

Add the share-deletion helpers:

```go
// deleteProjectShare removes this instance's share of project. When no
// instance hosts the project any more, every row goes (events, durations,
// CVE rows included); otherwise only this instance's targets are dropped.
func (c *Consumer) deleteProjectShare(project string) {
	if !c.fleet.IsHosted(project) {
		if err := store.DeletePackagesByProject(c.db, project); err != nil {
			slog.Error("mq: delete packages for project", "project", project, "err", err)
		}
		return
	}
	pkgs, err := store.QueryProjectPackages(c.db, project)
	if err != nil {
		slog.Error("mq: query project packages", "project", project, "err", err)
		return
	}
	for _, pkg := range pkgs {
		c.dropShare(pkg)
	}
}

func (c *Consumer) deletePackageShare(project, name string) {
	pkg, err := store.GetPackage(c.db, project, name)
	if err != nil || pkg == nil {
		return
	}
	c.dropShare(pkg)
}

func (c *Consumer) dropShare(pkg *model.Package) {
	rest := obs.WithoutInstance(pkg, c.inst.Slug)
	if len(rest.Targets) == 0 {
		if err := store.DeletePackage(c.db, pkg.Project, pkg.Name); err != nil {
			slog.Error("mq: delete package", "project", pkg.Project, "package", pkg.Name, "err", err)
		}
		c.ws.Remove(pkg.Project + "/" + pkg.Name)
		return
	}
	if err := c.upsertPackage(rest); err != nil {
		slog.Error("mq: upsert package share", "err", err)
		return
	}
	c.ws.Signal(rest)
}
```

In `mergePackageTarget`, every `model.Target{Repo: m.Repo, Arch: m.Arch, State: string(newState)}` literal gains `Instance: c.inst.Slug`, and after building `targets` add `c.fleet.SetOwner(m.Project, m.Repo, c.inst.Slug)` (guard `if c.fleet != nil`).

`backend/cmd/obsboard/main.go`: replace the single consumer with
```go
	for _, inst := range fleet.Instances() {
		go mq.NewConsumer(inst, fleet, db, h, ws, cfg.OBSRoot).Run(ctx)
	}
```
and make `SingleFleet`'s legacy instance carry the MQ URL: in main, after creating `fleet`, set `fleet.Default().MQURL = cfg.MQ.URL` (identity-mode wiring only; replaced in Task 9).

- [ ] **Step 4: Run tests**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/mq backend/internal/obs/fleet.go backend/cmd/obsboard/main.go
git commit -s -m "feat(mq): one consumer per OBS instance with instance-scoped deletes"
```

---

### Task 8: API, CVE, metrics and health on Fleet

**Goal:** The router, release/binary/rebuild/metadata handlers, CVE scanner, metrics sampler and telemetry use the Fleet; new `GET /api/instances`, `/api/metrics.by_instance`, Overview `by_instance`, and SSE `instance_health`.

**Files:**
- Create: `backend/internal/api/instances.go`, `backend/internal/api/instances_test.go`
- Modify: `backend/internal/api/{server,handlers,release_artifacts,artifact_metadata,metrics,overview}.go`, `backend/internal/store/instances.go`, `backend/internal/cve/scanner.go`, `backend/internal/hub/message.go`, `backend/cmd/obsboard/main.go`
- Test: `backend/internal/api/{handlers,metrics,release_artifacts,artifact_metadata,overview}_test.go`, `backend/internal/cve/scanner_test.go`, `backend/internal/store/instances_test.go`

**Acceptance Criteria:**
- [ ] `api.NewRouter(db, h, fleet *obs.Fleet, root, ws, telemetryEnabled, telemetryInterval, gate)`; every handler that took `*obs.Client` takes `*obs.Fleet`.
- [ ] `GET /api/instances` returns `[{name, slug, root, web_url, download_url, registry, health}]` with `root` = `PathRoot()`.
- [ ] `/api/metrics` keeps all existing fields and adds `by_instance` (`[]obs.InstanceStatus`).
- [ ] Release container `registry`/`pull_cmd` use the binary's instance `ImageBase`; release package/container/tarball artifacts include `instance`.
- [ ] CVE image refs and event URLs come from the target's instance via `WithURLs`; CVE events carry `Instance`; default (no option) reproduces today's openSUSE refs.
- [ ] `store.QueryTargetCountsByInstance` groups live non-release targets into ok/failing/building/blocked; Overview JSON gains `by_instance`, unstamped targets counted under the first instance's slug.
- [ ] `hub.InstanceHealth(payload)` emits `{"type":"instance_health","data":…}`; `main` runs `fleet.RunHealthWatch(ctx, 15*time.Second, …)`.
- [ ] `cve.ImageBase` is removed; its test moves to the instance URL tests (already covered in Task 2).

**Verify:** `cd backend && go test ./... ` → PASS

**Steps:**

- [ ] **Step 1: Migrate existing tests**

```bash
cd backend
sed -i -E 's/obs\.NewClient\(([^()]*)\)/obs.SingleFleet(obs.NewClient(\1), "isv:percona")/g' internal/api/handlers_test.go internal/api/metrics_test.go internal/api/release_artifacts_test.go
```
In `internal/api/overview_test.go` update `overviewHandler(db, "isv:percona", newOverviewCache(time.Minute))` → `overviewHandler(db, "isv:percona", "opensuse", newOverviewCache(time.Minute))`. Delete `TestImageBase` from `internal/cve/scanner_test.go`. If `artifact_metadata_test.go` or `release_artifacts_test.go` call package-level builders with a `*obs.Client`, pass the Fleet from the same sed pattern.

- [ ] **Step 2: Write the failing tests**

`backend/internal/api/instances_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/percona/obs-dashboard/internal/obs"
)

func TestInstancesHandler(t *testing.T) {
	fleet := obs.NewFleet(
		obs.LegacyInstance(nil, "isv:percona"),
		obs.NewInstance(obs.InstanceInfo{Name: "Percona", Slug: "percona", Root: "percona",
			WebURL: "https://obs.example.com", DownloadURL: "https://dl.example.com/repositories", Registry: "reg.example.com"}, nil),
	)
	rec := httptest.NewRecorder()
	instancesHandler(fleet)(rec, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
	var got []instanceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Slug != "opensuse" || got[0].Root != "" {
		t.Errorf("identity instance must expose empty root: %+v", got[0])
	}
	if got[1].Root != "percona" || got[1].WebURL != "https://obs.example.com" || got[1].Registry != "reg.example.com" {
		t.Errorf("instance 2: %+v", got[1])
	}
}
```

Append to `backend/internal/store/instances_test.go`:

```go
func TestQueryTargetCountsByInstance(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	pkgs := []*model.Package{
		{Project: "ppg:17", Name: "a", RollupState: model.RollupFailed, UpdatedAt: now, Targets: []model.Target{
			{Repo: "R", Arch: "x", State: "succeeded", Instance: "opensuse"},
			{Repo: "D", Arch: "y", State: "failed", Instance: "percona"},
			{Repo: "D2", Arch: "y", State: "building", Instance: "percona"},
			{Repo: "D3", Arch: "y", State: "blocked", Instance: "percona"},
		}},
		{Project: "ppg:releases:17", Name: "r", IsRelease: true, RollupState: model.RollupFailed, UpdatedAt: now,
			Targets: []model.Target{{Repo: "Z", Arch: "x", State: "failed", Instance: "percona"}}},
	}
	for _, p := range pkgs {
		if err := UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := QueryTargetCountsByInstance(db)
	if err != nil {
		t.Fatal(err)
	}
	want := []InstanceTargetCounts{
		{Instance: "opensuse", OK: 1},
		{Instance: "percona", Failing: 1, Building: 1, Blocked: 1},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v (release packages excluded)", got, want)
	}
}
```

Append to `backend/internal/cve/scanner_test.go`:

```go
type stubURLs struct{}

func (stubURLs) ImageBase(slug, project, repo, name string) string {
	return "reg." + slug + "/" + project + "/" + repo + "/" + name
}
func (stubURLs) PackageURL(slug, project, pkg string) string { return "https://" + slug + "/" + project + "/" + pkg }

func TestScannerUsesTargetInstanceURLs(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var gotRef string
	exec := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		gotRef = args[len(args)-1]
		return []byte(`{"Results":[]}`), nil
	}
	s := cve.NewScanner(db, hubpkg.New(), 1, cve.WithExecFn(exec), cve.WithURLs(stubURLs{}))
	s.ScanNow(context.Background(), cve.ScanRequest{
		Project: "ppg:17:containers", Package: "pg", PrimaryTag: "17.5",
		Targets: []model.Target{{Repo: "ubi9", Arch: "x86_64", State: "succeeded", Instance: "percona"}},
	})
	if gotRef != "reg.percona/ppg:17:containers/ubi9/pg:17.5" {
		t.Errorf("image ref = %q", gotRef)
	}
}
```

(Add imports as needed: `context`, `hubpkg`, `model`, `store`. `ScanNow` is a new exported test seam, defined below.)

Run: `cd backend && go test ./internal/api/ ./internal/store/ ./internal/cve/ -v` → FAIL.

- [ ] **Step 3: Implement**

**`backend/internal/store/instances.go`** — append:

```go
// InstanceTargetCounts is the live target-state tally for one OBS instance.
type InstanceTargetCounts struct {
	Instance string
	OK       int
	Failing  int
	Building int
	Blocked  int
}

// QueryTargetCountsByInstance tallies current targets of non-release
// packages per instance, grouped for the Overview "By instance" card.
func QueryTargetCountsByInstance(db *sql.DB) ([]InstanceTargetCounts, error) {
	rows, err := db.Query(`SELECT targets_json FROM packages WHERE is_release = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*InstanceTargetCounts{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var targets []struct {
			State    string `json:"state"`
			Instance string `json:"instance"`
		}
		if err := json.Unmarshal([]byte(raw), &targets); err != nil {
			continue
		}
		for _, t := range targets {
			c := by[t.Instance]
			if c == nil {
				c = &InstanceTargetCounts{Instance: t.Instance}
				by[t.Instance] = c
			}
			switch t.State {
			case "succeeded", "published":
				c.OK++
			case "failed", "broken", "unresolvable":
				c.Failing++
			case "building", "finished", "scheduled":
				c.Building++
			case "blocked":
				c.Blocked++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]InstanceTargetCounts, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}
```
(imports: `encoding/json`, `sort`.)

**`backend/internal/hub/message.go`** — append:

```go
// InstanceHealth serialises an OBS instance health change for the SSE stream.
func InstanceHealth(payload any) []byte {
	d, _ := json.Marshal(payload)
	out, _ := json.Marshal(Msg{Type: "instance_health", Data: d})
	return out
}
```

**`backend/internal/api/instances.go`**:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/percona/obs-dashboard/internal/obs"
)

type instanceView struct {
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Root        string     `json:"root"`
	WebURL      string     `json:"web_url"`
	DownloadURL string     `json:"download_url"`
	Registry    string     `json:"registry"`
	Health      obs.Health `json:"health"`
}

// instancesHandler serves GET /api/instances: every configured instance's
// display name, URL bases and current health. root is what the client adds
// to a logical project name to reach the instance project.
func instancesHandler(fleet *obs.Fleet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := make([]instanceView, 0, len(fleet.Instances()))
		for _, in := range fleet.Instances() {
			out = append(out, instanceView{
				Name: in.Name, Slug: in.Slug, Root: in.PathRoot(),
				WebURL: in.WebURL, DownloadURL: in.InstanceInfo.DownloadURL, Registry: in.Registry,
				Health: in.Health(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
```

**`backend/internal/api/server.go`** — `NewRouter(db *sql.DB, h *hub.Hub, fleet *obs.Fleet, root string, …)`; pass `fleet` everywhere `obsClient` was passed; register `r.Get("/api/instances", instancesHandler(fleet))`; overview: `overviewHandler(db, root, fleet.Default().Slug, overview)`.

**`backend/internal/api/handlers.go`** — `binariesHandler(fleet *obs.Fleet)` and `rebuildHandler(fleet *obs.Fleet)`: rename the parameter and nil-checks (`if fleet == nil`), call `fleet.PackageBinaries` / `fleet.Rebuild`.

**`backend/internal/api/artifact_metadata.go`** — `artifactMetadataHandler(fleet *obs.Fleet, cache *binaryListCache)`, calling `fleet.ProjectBinaryList`.

**`backend/internal/api/release_artifacts.go`**:
- Add `Instance string \`json:"instance,omitempty"\`` to `ReleasePackageArtifact`, `ReleaseContainerArtifact`, `ReleaseTarballArtifact`; set it from `binary.Instance` where each artifact is first created.
- `releaseArtifactsHandler(db, fleet *obs.Fleet, root, cache)`, `buildReleaseArtifacts(ctx, client *obs.Fleet, root, version)`, `collectSubprojectBinaries(ctx, client *obs.Fleet, …)`, `buildReleaseContainerArtifacts(ctx, client *obs.Fleet, binaries)`.
- Replace `Registry: containerRegistryPath(binary.Project, binary.Repo, binary.Package)` with `Registry: client.InstanceOrDefault(binary.Instance).ImageBase(binary.Project, binary.Repo, binary.Package)` and delete `containerRegistryPath`. Update any `release_artifacts_test.go` expectation that asserted a non-lowercased registry path (release projects are lowercase, so none should change).

**`backend/internal/api/metrics.go`** — `metricsHandler(fleet *obs.Fleet, …)`; use `fleet.MetricsSnapshot()`, `fleet.LimiterStats()`, `fleet.RatePerSecond()`; add `ByInstance []obs.InstanceStatus \`json:"by_instance"\`` to `metricsResponse`, filled with `fleet.Statuses()`.

**`backend/internal/api/overview.go`**:
```go
// OverviewInstance is one instance's live target tally.
type OverviewInstance struct {
	Instance string `json:"instance"`
	OK       int    `json:"ok"`
	Failing  int    `json:"failing"`
	Building int    `json:"building"`
	Blocked  int    `json:"blocked"`
}
```
Add `ByInstance []OverviewInstance \`json:"by_instance"\`` to `OverviewSnapshot`. `overviewHandler(db *sql.DB, root, defaultSlug string, cache *overviewCache)`; inside the fetch closure after `buildOverviewSnapshot(...)`:
```go
			snap := buildOverviewSnapshot(root, window, now, cur, prev, scans, periods)
			counts, err := store.QueryTargetCountsByInstance(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			snap.ByInstance = mergeInstanceCounts(counts, defaultSlug)
			return snap, nil
```
with
```go
// mergeInstanceCounts folds unstamped (pre-migration) targets into the
// default instance and returns rows sorted by slug.
func mergeInstanceCounts(counts []store.InstanceTargetCounts, defaultSlug string) []OverviewInstance {
	by := map[string]*OverviewInstance{}
	for _, c := range counts {
		slug := c.Instance
		if slug == "" {
			slug = defaultSlug
		}
		o := by[slug]
		if o == nil {
			o = &OverviewInstance{Instance: slug}
			by[slug] = o
		}
		o.OK += c.OK
		o.Failing += c.Failing
		o.Building += c.Building
		o.Blocked += c.Blocked
	}
	out := make([]OverviewInstance, 0, len(by))
	for _, o := range by {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}
```

**`backend/internal/cve/scanner.go`**:
```go
// URLResolver builds registry and OBS URLs for a target's instance.
type URLResolver interface {
	ImageBase(instance, project, repo, name string) string
	PackageURL(instance, project, pkg string) string
}

// WithURLs sets the URL resolver (the Fleet in production).
func WithURLs(r URLResolver) Option { return func(s *Scanner) { s.urls = r } }
```
Add `urls URLResolver` to `Scanner`; in `NewScanner` default `urls: obs.SingleFleet(nil, "")` (import `obs`; no import cycle — obs does not import cve). Delete `const obsBase` and `func ImageBase`. In `scanPackage` per target:
```go
		imageRef := s.urls.ImageBase(target.Instance, req.Project, target.Repo, req.Package) + ":" + req.PrimaryTag
		obsURL := s.urls.PackageURL(target.Instance, req.Project, req.Package)
```
and add `Instance: target.Instance,` to the three CVE `model.Event` literals. Add the test seam:
```go
// ScanNow runs one request synchronously (tests).
func (s *Scanner) ScanNow(ctx context.Context, req ScanRequest) { s.scanPackage(ctx, req) }
```

**`backend/cmd/obsboard/main.go`**:
- `scanner := cve.NewScanner(db, h, 2, cve.WithURLs(fleet))` — move the `fleet` construction above the scanner.
- `sampler := &metricsampler.Sampler{DB: db, Snap: fleet}`; telemetry `Snap: fleet, Limiter: fleet`.
- `router := api.NewRouter(db, h, fleet, cfg.OBSRoot, ws, telemetryEnabled, cfg.Telemetry.Interval, gate)`.
- Health watch:
```go
	go fleet.RunHealthWatch(ctx, 15*time.Second, func(slug string, health obs.Health) {
		h.Notify(hub.InstanceHealth(map[string]any{"slug": slug, "health": health}))
	})
```
After this task `obsClient` is only used to build the SingleFleet.

- [ ] **Step 4: Run tests**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/
git commit -s -m "feat(api): instances endpoint, per-instance metrics and health on Fleet"
```

---

### Task 9: Root-free logical names — classifier, store, API, main wiring and migration

**Goal:** Flip the whole backend to root-free logical project names atomically: root-free classifier/overview rows/store queries/handlers, real per-instance translation in `main` (no identity mode), one consumer per configured instance, and the one-time `MigrateLogicalNames` migration.

**Files:**
- Create: `backend/internal/store/migrate_logical.go`, `backend/internal/store/migrate_logical_test.go`
- Modify: `backend/internal/obs/{classifier,poller}.go`, `backend/internal/mq/consumer.go`, `backend/internal/store/{packages,events}.go`, `backend/internal/api/{server,handlers,release_artifacts,overview}.go`, `backend/cmd/obsboard/main.go`
- Test: `backend/internal/obs/classifier_test.go`, `backend/internal/obs/poller_test.go`, `backend/internal/mq/consumer_test.go`, `backend/internal/api/{overview,handlers,release_artifacts}_test.go`, `backend/internal/store/{packages,events,overview}_test.go`

**Acceptance Criteria:**
- [ ] `obs.Classify(project)` and `obs.ProjectTags(project)` take no root: `Classify("ppg:17") == KindDev`, `Classify("PR:pr-1:ppg:17") == KindPR`, `Classify("ppg:releases:17") == KindRelease`, `Classify("common") == KindCommon`, `Classify("isv:percona:ppg:17") == KindUnknown`.
- [ ] `NewPoller(fleet, db, interval, h, ws, gate)` and `NewConsumer(inst, fleet, db, h, ws)` drop `root`; `api.NewRouter` drops `root`.
- [ ] Store: `QueryBuildPackages(db, product, tier, version)`, `QueryPRBuildPackages(db, pr)`, `QueryPRDistinctRepos(db, pr)`, `QueryPRBuildEvents(db, pr, from, to)`; handler prefixes are root-free (`ppg:staging`, `ppg:common`, `common`, `PR:<pr>`, `ppg:releases[:<v>]`).
- [ ] Overview rows are root-free (`ppg:staging:17`, `ppg:staging:17:extras`, `ppg:common`, `common`, `ppg:releases`, `PR:pr-9`).
- [ ] `MigrateLogicalNames(db, "isv:percona", "opensuse")` strips `isv:percona:` from `project` in `packages`, `events`, `target_state_durations`, `cve_scans`, `cve_periods`; stamps missing target instances and `events.instance` for rows with a repo; second run changes nothing; runs in one transaction.
- [ ] `main` builds one `obs.Instance` per `cfg.Instances` (non-identity), runs the migration before seeding, starts one consumer per instance.
- [ ] `TestPollerCarriesForwardFailedInstance` (Task 6) now exercises real carry-forward with root-free names and passes.

**Verify:** `cd backend && go test ./... ` → PASS

**Steps:**

- [ ] **Step 1: Write the migration test** — create `backend/internal/store/migrate_logical_test.go`:

```go
package store

import (
	"testing"
	"time"
)

func TestMigrateLogicalNames(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'failed', '[{"repo":"R","arch":"x","state":"failed"}]', ?)`, now)
	mustExec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('ppg:18', 'already', 'succeeded', '[{"repo":"R","arch":"x","state":"succeeded","instance":"percona"}]', ?)`, now)
	mustExec(`INSERT INTO events (id, type, project, package, repo, arch, what, why, url, at)
		VALUES ('e1', 'failed', 'isv:percona:ppg:17', 'pg', 'R', 'x', 'w', '', 'https://build.opensuse.org/x', ?)`, now)
	mustExec(`INSERT INTO events (id, type, project, package, what, why, url, at)
		VALUES ('e2', 'created', 'isv:percona:ppg:17', '', 'w', '', 'u', ?)`, now)
	mustExec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', 'failed', ?)`, now)
	mustExec(`INSERT INTO cve_scans (project, package, repo, arch, image_ref, scanned_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', 'ref', ?)`, now)
	mustExec(`INSERT INTO cve_periods (project, package, repo, arch, cve_since, clean_since)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', ?, ?)`, now, now)

	for run := 1; run <= 2; run++ {
		if err := MigrateLogicalNames(db, "isv:percona", "opensuse"); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, table := range []string{"packages", "events", "target_state_durations", "cve_scans", "cve_periods"} {
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE project LIKE 'isv:percona:%'`).Scan(&n)
			if n != 0 {
				t.Errorf("run %d: %s still has %d prefixed rows", run, table, n)
			}
		}
		var targets string
		db.QueryRow(`SELECT targets_json FROM packages WHERE project = 'ppg:17'`).Scan(&targets)
		if targets != `[{"repo":"R","arch":"x","state":"failed","instance":"opensuse"}]` {
			t.Errorf("run %d: targets_json = %s", run, targets)
		}
		db.QueryRow(`SELECT targets_json FROM packages WHERE project = 'ppg:18'`).Scan(&targets)
		if targets != `[{"repo":"R","arch":"x","state":"succeeded","instance":"percona"}]` {
			t.Errorf("run %d: stamped targets must be untouched: %s", run, targets)
		}
		var inst1, inst2 string
		db.QueryRow(`SELECT COALESCE(instance,'') FROM events WHERE id = 'e1'`).Scan(&inst1)
		db.QueryRow(`SELECT COALESCE(instance,'') FROM events WHERE id = 'e2'`).Scan(&inst2)
		if inst1 != "opensuse" || inst2 != "" {
			t.Errorf("run %d: event instances = %q, %q", run, inst1, inst2)
		}
	}
}
```

Run: `cd backend && go test ./internal/store/ -run MigrateLogical -v` → FAIL (undefined).

- [ ] **Step 2: Implement the migration** — create `backend/internal/store/migrate_logical.go`:

```go
package store

import (
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/percona/obs-dashboard/internal/model"
)

// MigrateLogicalNames converts a single-instance database to root-free
// logical project names: it strips "<legacyRoot>:" from project columns and
// stamps slug on targets and target-level events that carry no instance.
// Idempotent; runs in one transaction.
func MigrateLogicalNames(db *sql.DB, legacyRoot, slug string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	prefix := legacyRoot + ":"
	for _, table := range []string{"packages", "events", "target_state_durations", "cve_scans", "cve_periods"} {
		res, err := tx.Exec(`UPDATE `+table+` SET project = substr(project, ?)
			WHERE substr(project, 1, ?) = ?`, len(prefix)+1, len(prefix), prefix)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Info("store: stripped root from project names", "table", table, "rows", n)
		}
	}

	rows, err := tx.Query(`SELECT project, name, targets_json FROM packages`)
	if err != nil {
		return err
	}
	type update struct{ project, name, targets string }
	var updates []update
	for rows.Next() {
		var project, name, raw string
		if err := rows.Scan(&project, &name, &raw); err != nil {
			rows.Close()
			return err
		}
		var targets []model.Target
		if json.Unmarshal([]byte(raw), &targets) != nil {
			continue
		}
		changed := false
		for i := range targets {
			if targets[i].Instance == "" {
				targets[i].Instance = slug
				changed = true
			}
		}
		if !changed {
			continue
		}
		out, err := json.Marshal(targets)
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, update{project, name, string(out)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := tx.Exec(`UPDATE packages SET targets_json = ? WHERE project = ? AND name = ?`,
			u.targets, u.project, u.name); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE events SET instance = ?
		WHERE instance IS NULL AND repo IS NOT NULL AND repo != ''`, slug); err != nil {
		return err
	}
	return tx.Commit()
}
```

Run: `cd backend && go test ./internal/store/ -run MigrateLogical -v` → PASS.

- [ ] **Step 3: Root-free classifier** — in `backend/internal/obs/classifier.go`:

```go
// Classify returns the ProjectKind of a logical (root-free) project name,
// e.g. "ppg:17", "PR:pr-42:ppg:17", "ppg:releases:17", "common".
func Classify(project string) ProjectKind {
	parts := strings.Split(project, ":")
	switch parts[0] {
	… (the existing switch body, unchanged)
	}
	return KindUnknown
}

// ProjectTags returns the tag slice to store on packages belonging to project.
func ProjectTags(project string) []string {
	switch Classify(project) {
	… (unchanged)
	}
}
```
Update comments in the `ProjectKind` constants from `<root>:ppg:…` to `ppg:…`. Also update the `PRNumber` doc example to `"PR:pr-42:ppg17" → "42"` (logic unchanged).

- [ ] **Step 4: Drop `root` from poller, consumer, store and API**

- `backend/internal/obs/poller.go`: remove the `root` field/param (`NewPoller(client *Fleet, db *sql.DB, interval time.Duration, h *hubpkg.Hub, ws *workingset.WorkingSet, gate PollGate)`); `store.QueryPackages(p.db, "")`; `Classify(project)`; `ProjectTags(project)`.
- `backend/internal/mq/consumer.go`: remove `root`; `NewConsumer(inst *obs.Instance, fleet *obs.Fleet, db *sql.DB, h *hubpkg.Hub, ws *workingset.WorkingSet)`; `obs.Classify(m.Project)`, `obs.ProjectTags(m.Project)`; `packageTags(project string, existing *model.Package)` calls `obs.ProjectTags(project)`.
- `backend/internal/store/packages.go`:
  ```go
  func QueryPRBuildPackages(db *sql.DB, pr string) ([]*model.Package, error) {
  	p := "PR:" + pr
  	… (rest unchanged)
  }
  func QueryBuildPackages(db *sql.DB, product, tier, version string) ([]*model.Package, error) {
  	gp := "common"
  	cp := product + ":common"
  	… pp := product + ":" + tier … vp := product + ":" + tier + ":" + version …
  }
  func QueryPRDistinctRepos(db *sql.DB, pr string) ([]string, error) { p := "PR:" + pr … }
  ```
  Update the doc comments (`root:product:tier` → `product:tier`, examples `isv:percona:ppg:releases` → `ppg:releases`).
- `backend/internal/store/events.go`: `QueryPRBuildEvents(db *sql.DB, pr string, from, to time.Time)` with `p := "PR:" + pr`.
- `backend/internal/api/handlers.go`:
  - `packagesHandler(db)` → `store.QueryBuildPackages(db, product, tier, version)`.
  - `eventsHandler` prefixes: `product + ":" + tier`, `product + ":common"`, `"common"`.
  - `prContextPackagesHandler(db)`, `prContextEventsHandler(db)`, `prReposHandler(db)` → root-free store calls.
  - `prPackagesHandler`: `store.QueryPackages(db, "PR:")`.
  - `reposHandler`: `prefix := chi.URLParam(r, "product") + ":" + chi.URLParam(r, "tier") + ":" + chi.URLParam(r, "version")`.
  - `releasesPackagesHandler(db)`: `store.QueryReleasePackages(db, "ppg:releases")`; `releasesReposHandler(db)`: `"ppg:releases:" + version`.
  - Update comments that mention `isv:percona:`.
- `backend/internal/api/release_artifacts.go`: `releaseArtifactsHandler(db, fleet, cache)`; `buildReleaseArtifacts(ctx, client, version)` with `project := "ppg:releases:" + version`.
- `backend/internal/api/overview.go`: `logicalProject(project string)` with `rel := strings.Split(project, ":")` (no prefix check; unknown shapes still return `""`), returning `"PR:" + rel[1]`, `"common"`, `"ppg:common"`, `"ppg:releases"`, and `tierRow(tier, version, sub)` building `"ppg:" + tier + ":" + version` (+ `":" + sub[0]` for non-containers). `buildOverviewSnapshot(window, now, …)` and `overviewHandler(db, defaultSlug, cache)` drop `root`.
- `backend/internal/api/server.go`: `NewRouter(db, h, fleet, ws, telemetryEnabled, telemetryInterval, gate)` and the updated handler calls.

- [ ] **Step 5: Wire real instances in main** — in `backend/cmd/obsboard/main.go`, right after `store.Open`:

```go
	instances := make([]*obs.Instance, 0, len(cfg.Instances))
	legacySlug := cfg.Instances[0].Slug
	for _, ic := range cfg.Instances {
		c := obs.NewClient(ic.APIURL, ic.Username, ic.Password)
		c.SetMinuteBudget(ic.MinuteRequestBudget)
		instances = append(instances, obs.NewInstance(obs.InstanceInfo{
			Name: ic.Name, Slug: ic.Slug, Root: ic.Root,
			WebURL: ic.WebURL, DownloadURL: ic.DownloadURL, Registry: ic.Registry,
			MQURL: ic.MQ.URL, MQExchange: ic.MQ.Exchange, MQRoutingPrefix: ic.MQ.RoutingPrefix,
		}, c))
		if ic.Root == cfg.LegacyRoot && legacySlug == cfg.Instances[0].Slug {
			legacySlug = ic.Slug
		}
	}
	fleet := obs.NewFleet(instances...)
	if err := store.MigrateLogicalNames(db, cfg.LegacyRoot, legacySlug); err != nil {
		return fmt.Errorf("migrate logical names: %w", err)
	}
```
Delete the old `obsClient`/`SingleFleet`/`fleet.Default().MQURL` lines. Update: `obs.NewPoller(fleet, db, cfg.Poller.Interval, h, ws, gate)`, `mq.NewConsumer(inst, fleet, db, h, ws)`, `api.NewRouter(db, h, fleet, ws, telemetryEnabled, cfg.Telemetry.Interval, gate)`. Note: `legacySlug` must be the first instance whose root equals `LegacyRoot`, else the first instance — replace the loop condition with an explicit search:
```go
	legacySlug := cfg.Instances[0].Slug
	for _, ic := range cfg.Instances {
		if ic.Root == cfg.LegacyRoot {
			legacySlug = ic.Slug
			break
		}
	}
```
(placed before the instance-building loop, which then drops its `if`).

- [ ] **Step 6: Rewrite root-dependent test fixtures**

```bash
cd backend
sed -i 's/isv:percona://g' internal/obs/classifier_test.go internal/api/overview_test.go internal/api/handlers_test.go internal/store/packages_test.go internal/store/events_test.go internal/store/overview_test.go internal/mq/consumer_test.go
```
Then fix compile errors by hand:
- Remove the root argument from `Classify(root, …)`, `ProjectTags(root, …)`, `logicalProject(root, …)`, `buildOverviewSnapshot("isv:percona", …)`, `overviewHandler(db, "isv:percona", "opensuse", …)` → `overviewHandler(db, "opensuse", …)`, `QueryBuildPackages(db, "isv:percona", …)`, `QueryPRBuildPackages(db, "isv:percona", …)`, `QueryPRDistinctRepos(db, "isv:percona", …)`, `QueryPRBuildEvents(db, "isv:percona", …)`, `NewRouter(…, "isv:percona", …)`, `NewConsumer(…, "percona")`.
- Classifier tests asserting that out-of-root projects are unknown now assert `Classify("isv:percona:ppg:17") == KindUnknown` (root-qualified names are foreign).
- Consumer tests: replace `obs.SingleFleet(nil, "isv:percona")` with a non-identity instance so MQ payloads carrying `isv:percona:…` translate to logical names:
  ```go
  inst := obs.NewInstance(obs.InstanceInfo{Name: "openSUSE", Slug: "opensuse", Root: "isv:percona", WebURL: "https://build.opensuse.org", MQRoutingPrefix: "opensuse.obs"}, nil)
  fleet := obs.NewFleet(inst)
  ```
  and restore the `isv:percona:` prefix in MQ message payload fields (`"project": "isv:percona:…"`) the sed stripped — the consumer receives instance names; stored/asserted names are root-free. In the Task 7 tests, assert against root-free stored names (`store.GetPackage(db, "ppg:17", "pg")`).
- Handler/store tests that build URLs such as `/api/pr/pr-33/_/packages` need no change; fixture project names become `PR:pr-33:ppg:17` etc.
- `internal/obs/poller_test.go`: `NewPoller(fleet, db, time.Minute, hubpkg.New(), ws, nil)`; existing tests that used `SingleFleet(..., "isv:percona")` keep identity mode but their fixtures must be root-free for `Classify`: switch them to `NewFleet(NewInstance(InstanceInfo{Slug: "opensuse", Root: "isv:percona"}, NewClient(srv.URL, "u", "p")))` so the fake server keeps serving `isv:percona:…` paths while stored names are root-free.

Tests elsewhere (`client_test.go`, `tasks_test.go`, `worker_test.go`, `sweeper_test.go`, `cve/*_test.go`) treat project names as opaque strings and stay as they are.

- [ ] **Step 7: Run tests**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: PASS, including `TestPollerCarriesForwardFailedInstance` and `TestMigrateLogicalNames`.

- [ ] **Step 8: Commit**

```bash
git add backend/
git commit -s -m "feat: root-free logical project names across instances"
```

---

### Task 10: Config cleanup and deployment docs

**Goal:** Remove the now-unused legacy config fields from `Config`, document `obs_instances` in both example configs and `.env.example`.

**Files:**
- Modify: `backend/internal/config/config.go`, `backend/internal/config/config_test.go`, `config.yaml.example`, `backend/config.yaml.example`, `.env.example`

**Acceptance Criteria:**
- [ ] `Config` no longer has `OBSRoot`, `OBS`, `MQ` fields; legacy keys are still read (into locals) for the fallback instance and `LegacyRoot`.
- [ ] Both `config.yaml.example` files show a commented two-instance `obs_instances` block with every key and the env credential convention; legacy keys documented as the single-instance fallback.
- [ ] `.env.example` documents `OBS_<SLUG>_USERNAME` / `OBS_<SLUG>_PASSWORD`.
- [ ] `go test ./...` passes.

**Verify:** `cd backend && go build ./... && go test ./...` → PASS

**Steps:**

- [ ] **Step 1: Refactor `config.go`** — delete `OBSRoot`, `OBS`, `MQ` from `Config` and the `OBSConfig`/`MQConfig` types. In `Load`, build a local legacy value:

```go
	legacy := legacyConfig{
		root:     v.GetString("obs_root"),
		baseURL:  strings.TrimRight(v.GetString("obs.base_url"), "/"),
		username: v.GetString("obs.username"),
		password: v.GetString("obs.password"),
		budget:   v.GetInt("obs.minute_request_budget"),
		mqURL:    v.GetString("mq.url"),
	}
```
with
```go
// legacyConfig holds the pre-obs_instances single-instance keys.
type legacyConfig struct {
	root, baseURL, username, password, mqURL string
	budget                                   int
}
```
and change `loadInstances(v, legacy legacyConfig)` to read from it; `cfg.LegacyRoot = legacy.root`. Fix tests referencing removed fields (e.g. `cfg.OBS.MinuteRequestBudget` → `cfg.Instances[0].MinuteRequestBudget`).

- [ ] **Step 2: Docs** — in both `config.yaml.example` files, after the `obs:`/`mq:` blocks add:

```yaml
# Multiple OBS instances. When present, obs_instances replaces the single
# obs/mq/obs_root settings above (those remain the one-instance fallback).
# Repos of one package may be split across instances; the dashboard merges
# them by the project path relative to each instance's root.
# Credentials can come from env: OBS_<SLUG>_USERNAME / OBS_<SLUG>_PASSWORD,
# where SLUG is the name lowercased with non-alphanumerics as '_' and then
# uppercased (e.g. "Percona OBS" → OBS_PERCONA_OBS_USERNAME).
#obs_instances:
#  - name: openSUSE
#    root: "isv:percona"
#    api_url: "https://api.opensuse.org"
#    web_url: "https://build.opensuse.org"
#    download_url: "https://download.opensuse.org/repositories"
#    registry: "registry.opensuse.org"
#    minute_request_budget: 60
#    mq:
#      url: "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/"
#      exchange: "pubsub"            # default
#      routing_prefix: "opensuse.obs" # default
#  - name: Percona
#    root: "percona"
#    api_url: "https://api.obs.example.com"
#    web_url: "https://obs.example.com"
#    download_url: "https://download.obs.example.com/repositories"
#    registry: "registry.obs.example.com"
#    mq:
#      url: "amqps://user:pass@rabbit.obs.example.com:5671/"
#      routing_prefix: "percona.obs"
```

In `.env.example` after `OBS_PASSWORD=` add:

```
# With obs_instances in config.yaml, per-instance credentials:
# OBS_OPENSUSE_USERNAME= / OBS_OPENSUSE_PASSWORD=
# OBS_PERCONA_USERNAME=  / OBS_PERCONA_PASSWORD=
```

- [ ] **Step 3: Run tests**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/config config.yaml.example backend/config.yaml.example .env.example
git commit -s -m "chore(config): drop legacy config fields, document obs_instances"
```

---

### Task 11: Frontend — root-free project names

**Goal:** Every frontend context prefix, PR parsing, context membership and category rule works on root-free names; `shortProject` is gone because names are already short.

**Files:**
- Modify: `frontend/src/lib/project.ts` (replace), `frontend/src/lib/contexts.ts`, `frontend/src/lib/overview.ts`, `frontend/src/App.vue`, `frontend/src/types/api.ts`, `frontend/src/composables/useRealtimeStream.ts`, `frontend/src/composables/useEvents.ts`, `frontend/src/components/{PRBoard,PackagesSubTab,GreenStrip,PackageEventGroup,OverviewPanel,RebuildBarChart,PackageCard,EventRow,CveExposureTable}.vue`, `frontend/src/lib/tarballs.test-d.ts`
- Create: `frontend/src/lib/project.test-d.ts`

**Acceptance Criteria:**
- [ ] Context prefixes: `ppg:devel`, `ppg:staging`, `ppg:releases`, board PR `PR:<seg>`, artifacts PR `PR:<seg>:ppg:<tier>`; PR contexts still sort by PR number desc, staging before devel.
- [ ] `projectInContext` (shared by stream and events) includes the context subtree, PR `PR:<seg>:common`, product `<product>:common` and global `common`, excludes others for releases.
- [ ] `categoryOf("PR:pr-9")` → `'PRs'`.
- [ ] No `shortProject` import remains; no `isv:percona` literal remains in `frontend/src`.
- [ ] `npm run build` exits 0.

**Verify:** `cd frontend && npm run build && ! grep -rn "isv:percona\|shortProject" src` → build exit 0, grep finds nothing

**Steps:**

- [ ] **Step 1: Replace `frontend/src/lib/project.ts`:**

```ts
// Logical project names are root-free: "ppg:staging:17", "ppg:common",
// "common", "PR:pr-92:ppg:staging:17", "ppg:releases:17".

export function isPRProject(project: string): boolean {
  return project.split(':')[0] === 'PR'
}

function inTree(project: string, base: string): boolean {
  return project === base || project.startsWith(base + ':')
}

// Does project belong to the context with this prefix? A context covers its
// own subtree plus shared common trees: PR contexts add PR:<pr>:common;
// product (devel/staging) contexts add <product>:common and the global
// common tree. Release contexts are exact subtrees.
export function projectInContext(project: string, prefix: string): boolean {
  if (!prefix || inTree(project, prefix)) return true
  const parts = prefix.split(':')
  if (parts[0] === 'PR') return inTree(project, `${parts.slice(0, 2).join(':')}:common`)
  if (parts[1] === 'releases') return false
  return inTree(project, `${parts[0]}:common`) || inTree(project, 'common')
}
```

Create `frontend/src/lib/project.test-d.ts`:

```ts
import { isPRProject, projectInContext } from './project'

const a: boolean = isPRProject('PR:pr-92:ppg:staging:17')
const b: boolean = projectInContext('ppg:common', 'ppg:staging')
const c: boolean = projectInContext('PR:pr-92:common', 'PR:pr-92:ppg:staging')
void a; void b; void c
```

- [ ] **Step 2: Update consumers**

- `useRealtimeStream.ts`: delete the local `projectInContext`; `import { projectInContext } from '../lib/project'`.
- `useEvents.ts`: delete `matchesContext`; use `projectInContext(e.project, ctx.prefix)` in `filterEvents`.
- `lib/contexts.ts`: prefixes `'ppg:devel'`, `'ppg:staging'`, `'ppg:releases'`; in `prArtifactsContexts` push `prefix: \`PR:${prSegment}:ppg:${tier}\``; the sort reads the PR number from `split(':')[1]` and the tier from `split(':')[3]`; update comments (`PR:<pr>:ppg:<tier>:<version>[:<sub>]`).
- `App.vue`: board PR context `prefix: \`PR:${prSegment}\``; sort reads `split(':')[1]`.
- `types/api.ts`: comment example `"ppg:staging" or "PR:pr-92"`.
- `lib/overview.ts`: `import { isPRProject } from './project'`; `if (isPRProject(project)) return 'PRs'`.
- `PRBoard.vue`: `subprojectLabel` comment example `"PR:pr-42:ppg17" → "ppg17"` (logic unchanged). Leave `obsUrl`/`prProjectUrl` for Task 12 but change the literal `isv:percona:PR:pr-${pr}` to `PR:pr-${pr}` so no root literal remains (the link is made instance-correct in Task 12).
- `PackagesSubTab.vue`: fallback `\`ppg:${props.version}\`` instead of `\`isv:percona:ppg:${props.version}\`` (Task 12 makes the URL instance-correct).
- `GreenStrip.vue`, `PackageEventGroup.vue`, `OverviewPanel.vue`, `RebuildBarChart.vue`, `PackageCard.vue`, `EventRow.vue`, `CveExposureTable.vue`: remove the `shortProject` import and render the raw project (`{{ group.project }}`, `{{ project }}`, `{{ topPackage.project }}`, `{{ bar.project }}`, `{{ pkg.project }}`, `{{ props.event.project }}`, `{{ p.project }}`).
- `lib/tarballs.test-d.ts`: `'ppg:staging:18:tarballs'` in both calls.

- [ ] **Step 3: Build**

Run: `cd frontend && npm run build && ! grep -rn "isv:percona\|shortProject" src`
Expected: build exit 0; grep prints nothing.

- [ ] **Step 4: Commit**

```bash
git add frontend/src
git commit -s -m "feat(frontend): root-free project names"
```

---

### Task 12: Frontend — instance registry and instance-correct links

**Goal:** The frontend loads `/api/instances`, keeps health live from SSE, and builds every OBS/download/registry URL from the target's instance.

**Files:**
- Create: `frontend/src/lib/instances.ts`, `frontend/src/lib/instances.test-d.ts`, `frontend/src/composables/useInstances.ts`
- Modify: `frontend/src/types/api.ts`, `frontend/src/lib/tarballs.ts`, `frontend/src/composables/useRealtimeStream.ts`, `frontend/src/composables/useArtifacts.ts`, `frontend/src/components/{PackageCard,GreenStrip,PRBoard,PackagesSubTab,TarballsSubTab,ArtifactsPanel}.vue`

**Acceptance Criteria:**
- [ ] `Target.instance?`, `Event.instance?`, `ObsInstance`, `InstanceHealth` types exist.
- [ ] `useInstances()` fetches once per page, exposes `instances`, `instanceFor(slug)` (falls back to the first instance), `multi`, `packageInstances(pkg)`; `instance_health` SSE messages update health.
- [ ] URL helpers produce the Global Constraints formats; while instances are not loaded they return `''` and anchors render without `href`.
- [ ] PackageCard "OBS ↗" shows one link per instance when a package spans instances (label = instance name); live log links use the target's instance.
- [ ] GreenStrip project header links per instance; package pills link to the package's first instance.
- [ ] PackagesSubTab repo snippet/download links, TarballsSubTab download links and live-context container registry use the row's instance; release artifacts keep the backend `registry` and carry `instance`.
- [ ] No `opensuse.org` literal remains in `frontend/src` except `lib/instances.ts`' test file.
- [ ] `npm run build` exits 0.

**Verify:** `cd frontend && npm run build && ! grep -rn "opensuse.org" src --include=*.vue --include=*.ts | grep -v test-d` → exit 0, no matches

**Steps:**

- [ ] **Step 1: Types** — in `frontend/src/types/api.ts` add `instance?: string` to `Target` and `Event`, and:

```ts
export interface InstanceHealth {
  ok: boolean
  last_success?: string
  last_error?: string
  last_error_at?: string
  consecutive_failures: number
  mq_connected: boolean
}

export interface ObsInstance {
  name: string
  slug: string
  root: string        // prefix for logical names; '' = names are already instance names
  web_url: string
  download_url: string
  registry: string
  health: InstanceHealth
}
```

- [ ] **Step 2: URL builders** — create `frontend/src/lib/instances.ts`:

```ts
import type { ObsInstance } from '../types/api'

// Instance project name for a logical project on inst.
export function obsProject(inst: ObsInstance, project: string): string {
  return inst.root ? `${inst.root}:${project}` : project
}

export function projectUrl(inst: ObsInstance | undefined, project: string): string {
  return inst ? `${inst.web_url}/project/show/${obsProject(inst, project)}` : ''
}

export function packageUrl(inst: ObsInstance | undefined, project: string, name: string): string {
  return inst ? `${inst.web_url}/package/show/${obsProject(inst, project)}/${name}` : ''
}

export function liveLogUrl(inst: ObsInstance | undefined, project: string, name: string, repo: string, arch: string): string {
  return inst ? `${inst.web_url}/package/live_build_log/${obsProject(inst, project)}/${name}/${repo}/${arch}` : ''
}

// Repository base URL (trailing slash) for a project's repo on inst.
export function downloadBase(inst: ObsInstance | undefined, project: string, repo: string): string {
  return inst ? `${inst.download_url}/${obsProject(inst, project).split(':').join(':/')}/${repo}/` : ''
}

export function registryRef(inst: ObsInstance | undefined, project: string, repo: string, name: string): string {
  return inst ? `${inst.registry}/${obsProject(inst, project).toLowerCase().split(':').join('/')}/${repo}/${name}` : ''
}
```

Create `frontend/src/lib/instances.test-d.ts`:

```ts
import type { ObsInstance } from '../types/api'
import { obsProject, packageUrl, downloadBase, registryRef, liveLogUrl, projectUrl } from './instances'

const inst: ObsInstance = {
  name: 'Percona', slug: 'percona', root: 'percona',
  web_url: 'https://obs.example.com', download_url: 'https://dl.example.com/repositories',
  registry: 'registry.example.com',
  health: { ok: true, consecutive_failures: 0, mq_connected: true },
}
const a: string = obsProject(inst, 'ppg:17')                            // 'percona:ppg:17'
const b: string = packageUrl(inst, 'ppg:17', 'pg')                      // https://obs.example.com/package/show/percona:ppg:17/pg
const c: string = downloadBase(inst, 'ppg:17', 'RHEL_9')                // https://dl.example.com/repositories/percona:/ppg:/17/RHEL_9/
const d: string = registryRef(inst, 'ppg:17:containers', 'ubi9', 'pg')  // registry.example.com/percona/ppg/17/containers/ubi9/pg
const e: string = liveLogUrl(undefined, 'ppg:17', 'pg', 'R', 'x')       // '' while instances load
const f: string = projectUrl(inst, 'ppg:17')
void a; void b; void c; void d; void e; void f
```

- [ ] **Step 3: Registry composable** — create `frontend/src/composables/useInstances.ts`:

```ts
import { computed, ref } from 'vue'
import type { InstanceHealth, ObsInstance, Package } from '../types/api'

// Module-level singleton: the instance list is static for a page lifetime;
// only health changes (via SSE instance_health messages).
const instances = ref<ObsInstance[]>([])
let started = false

async function load(): Promise<void> {
  try {
    const res = await fetch('/api/instances')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    instances.value = await res.json() as ObsInstance[]
  } catch {
    started = false // retry on the next useInstances() call
  }
}

export function applyInstanceHealth(slug: string, health: InstanceHealth): void {
  const idx = instances.value.findIndex(i => i.slug === slug)
  if (idx >= 0) instances.value.splice(idx, 1, { ...instances.value[idx], health })
}

export function useInstances() {
  if (!started) {
    started = true
    void load()
  }
  const bySlug = computed(() => new Map(instances.value.map(i => [i.slug, i])))
  const multi = computed(() => instances.value.length > 1)

  function instanceFor(slug?: string): ObsInstance | undefined {
    return (slug ? bySlug.value.get(slug) : undefined) ?? instances.value[0]
  }

  // Distinct instances a package builds on, in config order (the first
  // instance when no target is stamped yet).
  function packageInstances(pkg: Package): ObsInstance[] {
    const slugs = new Set((pkg.targets ?? []).map(t => t.instance).filter((s): s is string => !!s))
    const list = instances.value.filter(i => slugs.has(i.slug))
    if (list.length > 0) return list
    const first = instances.value[0]
    return first ? [first] : []
  }

  return { instances, multi, instanceFor, packageInstances }
}
```

In `useRealtimeStream.ts`, `import { applyInstanceHealth } from './useInstances'` and add to `es.onmessage`:

```ts
      } else if (msg.type === 'instance_health') {
        const { slug, health } = msg.data as { slug: string; health: InstanceHealth }
        applyInstanceHealth(slug, health)
      }
```
(import the `InstanceHealth` type). Also include `instance` in `mergeTarget` (it comes from `next` via spread — no change needed).

- [ ] **Step 4: Replace hardcoded hosts**

- `PackageCard.vue`: `const { instanceFor, packageInstances } = useInstances()`;
  ```ts
  const obsLinks = computed(() => packageInstances(props.pkg).map(inst => ({
    name: inst.name, url: packageUrl(inst, props.pkg.project, props.pkg.name),
  })))
  function logUrl(t: Target): string {
    return liveLogUrl(instanceFor(t.instance), props.pkg.project, props.pkg.name, t.repo, t.arch)
  }
  ```
  Replace the single `OBS ↗` anchor with:
  ```vue
  <template v-for="(link, i) in obsLinks" :key="link.name">
    <a :href="link.url || undefined" target="_blank" rel="noopener"
       :style="{ marginLeft: i === 0 && !stateAge ? 'auto' : '0' }"
       class="text-[11.5px] font-bold text-brand-purple no-underline whitespace-nowrap flex-shrink-0"
    >{{ obsLinks.length > 1 ? `${link.name} ↗` : 'OBS ↗' }}</a>
  </template>
  ```
  and the log anchor `:href="logUrl(t) || undefined"`. Delete the old `obsUrl`.
- `GreenStrip.vue`: group header renders one anchor per instance hosting any package of the group:
  ```ts
  const { instances, packageInstances } = useInstances()
  function groupInstances(pkgs: Package[]): ObsInstance[] {
    const slugs = new Set(pkgs.flatMap(p => packageInstances(p).map(i => i.slug)))
    return instances.value.filter(i => slugs.has(i.slug))
  }
  ```
  ```vue
  <div class="flex items-center gap-[10px] flex-wrap">
    <a v-for="inst in groupInstances(group.pkgs)" :key="inst.slug"
       :href="projectUrl(inst, group.project) || undefined" target="_blank" rel="noopener"
       class="project-link font-mono text-[11px] text-text-muted no-underline inline-flex items-center gap-[3px]"
    >{{ group.project }}<template v-if="groupInstances(group.pkgs).length > 1"> · {{ inst.name }}</template> ↗</a>
  </div>
  ```
  Pills: `:href="packageUrl(packageInstances(pkg)[0], group.project, pkg.name) || undefined"`. Delete the local `projectUrl`/`packageUrl` functions.
- `PRBoard.vue`: `obsUrl(pkg)` → `packageUrl(packageInstances(pkg)[0], pkg.project, pkg.name)`; `prProjectUrl(pr)` → `projectUrl(instanceFor(), \`PR:pr-${pr}\`)`.
- `PackagesSubTab.vue`: add `instance?: string` to `PackageRow` (in `useArtifacts.ts`) and set it in `packageRows` from `target.instance` (`instance: target.instance`). Replace the top of the `snippet` computed (everything before `if (repo.obs.startsWith('openSUSE'))`) with:
  ```ts
  const { instanceFor } = useInstances()

  const snippet = computed(() => {
    const repo = props.selectedRepo
    if (!repo) return ''
    const project = props.packageRows[0]?.project ?? `ppg:${props.version}`
    const inst = instanceFor(props.packageRows[0]?.instance)
    if (!inst) return ''
    const obsProjectName = obsProject(inst, project) // repo alias / file names, as today
    const baseUrl = downloadBase(inst, project, repo.obs)
    const projectId = obsProjectName.split(':').join('_')
  ```
  and in the three returned templates replace every `${obsProject}` with `${obsProjectName}` (the variable was renamed; `baseUrl`/`projectId` keep their names). Replace `downloadUrl` with:
  ```ts
  function downloadUrl(row: PackageRow, filename: string): string {
    const base = downloadBase(instanceFor(row.instance), row.project, row.repo.obs)
    return base ? `${base}${row.arch}/${filename}` : ''
  }
  ```
  and bind download anchors as `:href="downloadUrl(row, b.filename) || undefined"` (keep the existing loop variable names). Imports: `useInstances`, `obsProject`, `downloadBase`. For the `/api/binaries` request keep `project: row.project` (the backend routes by owner).
- `lib/tarballs.ts`: change `tarballDownloadUrl` to take the base:
  ```ts
  export function tarballDownloadUrl(base: string, repo: string, name: string, version: string, arch: string): string {
    if (!base) return ''
    const prefix = name.replace(/-tarball$/, '')
    const upstream = version.split('-')[0]
    return `${base}${prefix}-${upstream}-${repo}-linux-${arch}.tar.gz`
  }
  ```
  Update the doc comment and `tarballs.test-d.ts` (`tarballDownloadUrl('https://dl/x/', 'ssl1.1', 'percona-postgresql-tarball', '18.6-3', 'x86_64')`). Add `instance?: string` to `Tarball` (set from the repo's targets' first `instance` in `useArtifacts`, and from `t.instance` in ArtifactsPanel's release mapping). `TarballsSubTab.vue`: `:href="tarballDownloadUrl(downloadBase(instanceFor(tarball.instance), tarball.project, tarball.repo), tarball.repo, tarball.name, tarball.version, arch) || undefined"`.
- `useArtifacts.ts` container images: `const inst = instanceFor(targets.find(t => t.repo === repo)?.instance)`; `const registry = registryRef(inst, pkg.project, repo, pkg.name)`; add `instance?: string` to `ContainerImage` and set it.
- `ArtifactsPanel.vue`: add `instance?: string` to the three `Release*Artifact` interfaces and map it through to rows/images/tarballs (`instance: pkg.instance`, etc.). Container release rows keep `registry: img.registry` from the backend.

- [ ] **Step 5: Build**

Run: `cd frontend && npm run build && ! grep -rn "opensuse.org" src --include=*.vue --include=*.ts | grep -v test-d`
Expected: exit 0; grep prints nothing.

- [ ] **Step 6: Commit**

```bash
git add frontend/src
git commit -s -m "feat(frontend): instance registry and instance-correct links"
```

---

### Task 13: Frontend — instance badges and instance filter

**Goal:** Multi-instance deployments show an instance badge on every target row and artifact row (amber when the instance is unhealthy) and an instance chip filter for the Builds board and event log, persisted in the URL; single-instance deployments show none of it.

**Files:**
- Create: `frontend/src/components/InstanceBadge.vue`
- Modify: `frontend/src/components/{PackageCard,FailureBoard,MainGrid,ContextBar,EventRow,PackagesSubTab,ContainersSubTab,TarballsSubTab}.vue`, `frontend/src/composables/{usePackages,useEvents,useUrlState}.ts`, `frontend/src/App.vue`

**Acceptance Criteria:**
- [ ] `InstanceBadge` renders nothing when `multi` is false; shows the instance name; amber styling plus `title="<name> unreachable — last update <age> ago"` when `health.ok` is false.
- [ ] Badges appear on PackageCard target rows, EventRow (when `event.instance` is set), and on package/container/tarball artifact rows.
- [ ] ContextBar shows an "Instances" chip row only when `multi`; toggling emits `toggle-instance`.
- [ ] Board: a package passes when it has at least one target on a selected instance; in PackageCard, target rows on unselected instances render at `opacity: 0.45`; counts/rollup unchanged.
- [ ] Event log: events whose `instance` is set and not selected are hidden; events without `instance` always show.
- [ ] URL `?instances=a,b` round-trips; omitted when empty.
- [ ] `npm run build` exits 0.

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Badge** — create `frontend/src/components/InstanceBadge.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { useInstances } from '../composables/useInstances'

const props = defineProps<{ slug?: string }>()
const { multi, instanceFor } = useInstances()

const inst = computed(() => instanceFor(props.slug))

function ago(iso?: string): string {
  if (!iso) return 'a while'
  const m = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 60000))
  if (m < 60) return `${m}m`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

const stale = computed(() => inst.value !== undefined && !inst.value.health.ok)
const title = computed(() => stale.value && inst.value
  ? `${inst.value.name} unreachable — last update ${ago(inst.value.health.last_success)} ago`
  : inst.value?.name)
</script>

<template>
  <span
    v-if="multi && inst"
    class="inline-flex items-center gap-[4px] font-mono text-[9.5px] font-bold py-[2px] px-[6px] rounded-[5px] border whitespace-nowrap flex-shrink-0"
    :style="stale
      ? { color: 'var(--warn)', background: 'var(--warn-tint)', borderColor: 'var(--warn)' }
      : { color: 'var(--text-secondary)', background: 'var(--bg-muted, var(--blocked-tint))', borderColor: 'var(--border)' }"
    :title="title"
  >{{ inst.name }}<template v-if="stale"> · stale</template></span>
</template>
```

- [ ] **Step 2: Place badges**

- `PackageCard.vue` target header row: after the `<code>{{ t.repo }}/{{ t.arch }}</code>` add `<InstanceBadge :slug="t.instance" />`.
- `EventRow.vue`: next to the project `<code>` add `<InstanceBadge v-if="props.event.instance" :slug="props.event.instance" />`.
- `PackagesSubTab.vue` package row name cell: `<InstanceBadge :slug="row.instance" />`; `ContainersSubTab.vue` next to `image.imageName`: `<InstanceBadge :slug="image.instance" />`; `TarballsSubTab.vue` next to the tarball name: `<InstanceBadge :slug="tarball.instance" />`.

- [ ] **Step 3: Filter state**

- `App.vue`: `const activeInstances = ref<string[]>([])`, `toggleInstance(slug)` mirroring `toggleTag`; clear it in `selectContext`; pass `:active-instances` + `@toggle-instance` to ContextBar and `:active-instances` to MainGrid; `filteredPackages = computed(() => filterByTags(activeTags.value, activeInstances.value))`; `filteredEvents = computed(() => filterEvents(activeTags.value, version.value, selectedContext.value, activeInstances.value))`; pass `activeInstances` to `useUrlState`.
- `usePackages.ts`:
  ```ts
  function filterByTags(tags: string[], instances: string[] = []): Package[] {
    return sorted.value.filter(p =>
      (tags.length === 0 || tags.every(t => t === 'tarball' ? isTarballPkg(p) : (p.tags ?? []).includes(t))) &&
      (instances.length === 0 || (p.targets ?? []).some(t => !!t.instance && instances.includes(t.instance))))
  }
  ```
- `useEvents.ts` `filterEvents(tags, version, ctx, instances: string[] = [])`: add `if (instances.length > 0 && e.instance && !instances.includes(e.instance)) return false`.
- `useUrlState.ts`: add `activeInstances: Ref<string[]>` to options; read `params.get('instances')` like `tags`; write `if (activeInstances.value.length > 0) params.set('instances', activeInstances.value.join(','))`.
- `ContextBar.vue`: props `activeInstances: string[]`, emit `'toggle-instance': [slug: string]`; `const { instances, multi } = useInstances()`; below the Tags row:
  ```vue
  <div v-if="multi" class="flex items-center gap-[9px] flex-wrap">
    <span class="text-[11px] text-text-muted font-semibold uppercase tracking-[0.06em] mr-0.5">Instances</span>
    <button
      v-for="inst in instances"
      :key="inst.slug"
      @click="emit('toggle-instance', inst.slug)"
      :style="tagStyle(inst.slug, activeInstances.includes(inst.slug))"
    >{{ inst.name }}</button>
  </div>
  ```
- `MainGrid.vue` → `FailureBoard.vue` → `PackageCard.vue`: thread `activeInstances: string[]` as a prop. In PackageCard:
  ```ts
  function targetDimmed(t: Target): boolean {
    const sel = props.activeInstances ?? []
    return sel.length > 0 && !!t.instance && !sel.includes(t.instance)
  }
  ```
  and on the target wrapper div add `opacity: targetDimmed(t) ? 0.45 : 1` to its `:style`.

- [ ] **Step 4: Build**

Run: `cd frontend && npm run build`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src
git commit -s -m "feat(frontend): instance badges and instance filter"
```

---

### Task 14: Frontend — By-instance Overview card and per-instance metrics

**Goal:** Overview shows a "By instance" card with live target counts per instance, and the Metrics panel lists per-instance request rate and health — both only in multi-instance deployments.

**Files:**
- Modify: `frontend/src/types/overview.ts`, `frontend/src/types/metrics.ts`, `frontend/src/components/OverviewPanel.vue`, `frontend/src/components/MetricsPanel.vue`

**Acceptance Criteria:**
- [ ] `OverviewSnapshot.by_instance?: OverviewInstance[]`; `MetricsSnapshot.by_instance?: InstanceStatus[]`.
- [ ] Overview renders, when `multi`, one `StatCard` titled "By instance" per the existing card grid, containing a `CategoryBreakdown` per instance (segments ok/failing/building/blocked with `var(--ok)`, `var(--fail)`, `var(--info)`, `var(--blocked)`), labelled with the instance name.
- [ ] Metrics panel, when `multi`, shows a "By instance" block: name, `req/s` (2 decimals), total requests, and an OK/unreachable pill.
- [ ] `npm run build` exits 0.

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Types**

`types/overview.ts`:
```ts
export interface OverviewInstance {
  instance: string
  ok: number
  failing: number
  building: number
  blocked: number
}
```
and `by_instance?: OverviewInstance[]` on `OverviewSnapshot`.

`types/metrics.ts`:
```ts
export interface InstanceStatus {
  name: string
  slug: string
  total: number
  req_per_s: number
  limiter: { Enabled: boolean; Budget: number; Remaining: number; Waits: number }
  health: { ok: boolean; last_success?: string; consecutive_failures: number; mq_connected: boolean }
}
```
(the backend `LimiterStats` has no JSON tags, so its keys are capitalised) and `by_instance?: InstanceStatus[]` on `MetricsSnapshot`.

- [ ] **Step 2: Overview card** — in `OverviewPanel.vue`:

```ts
import { useInstances } from '../composables/useInstances'
const { multi, instanceFor } = useInstances()
const instanceRows = computed(() => (snapshot.value?.by_instance ?? []).map(r => ({
  name: instanceFor(r.instance)?.name ?? r.instance,
  segments: [
    { label: 'ok', count: r.ok, colorVar: 'var(--ok)' },
    { label: 'failing', count: r.failing, colorVar: 'var(--fail)' },
    { label: 'building', count: r.building, colorVar: 'var(--info)' },
    { label: 'blocked', count: r.blocked, colorVar: 'var(--blocked)' },
  ],
})))
```
(`snapshot` comes from `useOverviewData`; expose it from the composable's return value if it is not already returned.) After the stat-card grid:
```vue
<div v-if="multi && instanceRows.length > 0" class="bg-bg-card border border-border rounded-[14px] p-[15px_20px] flex flex-col gap-3">
  <span class="text-[11.5px] font-bold uppercase tracking-[0.05em] text-text-muted">By instance · live targets</span>
  <div class="grid grid-cols-1 min-[760px]:grid-cols-2 gap-4">
    <div v-for="row in instanceRows" :key="row.name" class="flex flex-col gap-[6px]">
      <span class="text-[13px] font-bold text-text-primary">{{ row.name }}</span>
      <CategoryBreakdown :segments="row.segments" />
    </div>
  </div>
</div>
```

- [ ] **Step 3: Metrics block** — in `MetricsPanel.vue`:

```ts
import { useInstances } from '../composables/useInstances'
const { multi } = useInstances()
const instanceStats = computed(() => data.value?.by_instance ?? [])
```
After the window tiles:
```vue
<div v-if="multi && instanceStats.length > 0" class="mt-4">
  <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">By instance</div>
  <div class="flex flex-col gap-[6px] max-w-[560px]">
    <div v-for="s in instanceStats" :key="s.slug" class="flex items-center gap-3 text-[12px]">
      <span class="font-bold text-text-primary min-w-[120px]">{{ s.name }}</span>
      <span class="font-mono tabular-nums text-text-secondary">{{ s.req_per_s.toFixed(2) }} req/s</span>
      <span class="font-mono tabular-nums text-text-muted">{{ fmt(s.total) }} total</span>
      <span
        class="ml-auto text-[10.5px] font-bold px-2 py-[2px] rounded-[6px]"
        :style="s.health.ok
          ? { color: 'var(--ok)', background: 'var(--ok-tint)' }
          : { color: 'var(--warn)', background: 'var(--warn-tint)' }"
      >{{ s.health.ok ? 'OK' : 'unreachable' }}</span>
    </div>
  </div>
</div>
```

- [ ] **Step 4: Build**

Run: `cd frontend && npm run build`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src
git commit -s -m "feat(frontend): per-instance overview and metrics breakdowns"
```

---

### Task 15: End-to-end verification — single-instance regression and two-instance run

**Goal:** Prove the migrated single-instance deployment behaves as before and a two-instance configuration shows merged packages, badges, filter, per-instance links and stale badges.

**Files:**
- Create: `docs/superpowers/plans/2026-09-25-multi-obs-instances-verification.md` (evidence log)

**Acceptance Criteria:**
- [ ] Single-instance (legacy env config) dev stack against a copy of a pre-migration DB: startup log shows `store: stripped root from project names`; Builds board lists the same packages as before with root-free labels; no instance badges, chips, By-instance card or per-instance metrics; OBS/log/download links open `build.opensuse.org`/`download.opensuse.org` pages that exist.
- [ ] `curl -s localhost:4000/api/instances` returns one instance `{"slug":"opensuse","root":"isv:percona",…}`.
- [ ] Two-instance run (`obs_instances` with openSUSE plus the second instance, credentials via `OBS_<SLUG>_*`): a package with repos on both instances shows targets from both with badges; filtering by one instance dims the other's targets; "OBS ↗" offers one link per instance; `/api/metrics` has two `by_instance` entries.
- [ ] Breaking the second instance's password: within ~1 minute its badges turn amber ("stale") and its targets keep their last state; restoring it clears the amber.
- [ ] If second-instance credentials are unavailable, the two-instance items are recorded as pending in the evidence log with the reason, and the user is told.

**Verify:** `cat docs/superpowers/plans/2026-09-25-multi-obs-instances-verification.md` → every criterion marked PASS or PENDING with evidence

**Steps:**

- [ ] **Step 1: Single-instance regression**

```bash
cp data/obsboard.db /tmp/obsboard-premigration.db   # keep a rollback copy
task dev                                            # docker compose up --build
docker compose logs backend | grep -E "stripped root|listening"
curl -s localhost:4000/api/instances
curl -s "localhost:4000/api/products/ppg/staging/_/packages" | head -c 600
```
Open http://localhost:4000, check Overview, Builds (staging + a PR context), Artifacts (packages, containers, tarballs, releases). Record observations and URLs clicked.

- [ ] **Step 2: Two-instance run** — write `config.yaml` with the two-instance block from `config.yaml.example` (real second-instance values), export `OBS_<SLUG>_USERNAME/PASSWORD`, restart, and record: `curl -s localhost:4000/api/instances`, `curl -s localhost:4000/api/metrics | head -c 800`, screenshots or notes of badges/filter/links.

- [ ] **Step 3: Outage drill** — set a wrong `OBS_<SLUG>_PASSWORD` for the second instance, restart, wait 60–90 s, record the amber badges and `health.ok=false` in `/api/instances`; restore and record recovery.

- [ ] **Step 4: Write the evidence log and commit**

```bash
git add docs/superpowers/plans/2026-09-25-multi-obs-instances-verification.md
git commit -s -m "docs(plans): multi-instance verification evidence"
```

---

## Self-Review Notes

- Spec coverage: config/slug/legacy (T1, T10); translation/URLs/health (T2); Fleet routing table, partial failure, membership, owners, discovery, metrics (T3); `Target.Instance`/`Event.Instance`/carry-forward/instance-scoped deletes (T4, T5, T6, T7); per-instance consumers and prefixes (T7); `/api/instances`, `instance_health`, `by_instance` metrics, Overview by-instance, CVE per-instance registry (T8); root-free names + migration (T9); frontend root-free (T11), links (T12), badges/filter/dimming/URL param (T13), overview/metrics UI (T14); manual verification (T15). The spec's bookmark-strip item is recorded as not needed (URL state holds no project names).
- Deviations recorded in Global Constraints: migration is called from `main` (needs config); `useUrlState` needs no legacy strip.
