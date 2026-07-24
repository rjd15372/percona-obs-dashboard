# Artifacts Container Repo-Dimension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Artifacts tab's container sub-tab distinguishes ubi8 and ubi9 images again by making the base-image repo the distinguishing dimension of a release container artifact (base OS, registry path, keying, and CVE attach), while old-layout `images`-repo releases keep rendering identically.

**Architecture:** All changes in `internal/api/release_artifacts.go` (+ its test). `buildReleaseContainerArtifacts` keys artifacts by `(project, package, repo)`, derives base OS repo-first with a project-name fallback, and builds the registry path with the repo as a path segment (self-adjusting for `images` vs `ubiN`); `attachReleaseCveScans` filters each per-repo artifact's scans by `repo`. The frontend groups on `base_os` already, so it needs no change.

**Tech Stack:** Go.

**User decisions (already made):**
- Approach A: repo is the distinguishing dimension of a container artifact (mirrors the merged CVE repo-dimension fix). (Rejected: base-OS label only, keeping project+package keying — labels the collision without removing it.)
- Base OS derived repo-first, project-fallback (old + new layouts coexist).
- Registry path = `registry.opensuse.org/<project-path>/<repo>/<name>` (self-adjusting).
- Each base image shows as its own card under its own base-OS heading.
- Release has been cut on the new layout, so old (`images`) and new (`ubiN`) release containers coexist and both must work.

Spec: `docs/superpowers/specs/2026-07-24-artifacts-container-repo-dimension-design.md`

**Conventions:** backend commands from `backend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: repo-keyed release container artifacts

**Goal:** `buildReleaseContainerArtifacts` produces one artifact per `(project, package, repo)` with repo-derived base OS and registry path; `attachReleaseCveScans` gives each artifact only its own repo's CVE scans; old-layout `images` releases are byte-identical.

**Files:**
- Modify: `backend/internal/api/release_artifacts.go` (`ReleaseContainerArtifact.Repo`, keying, `containerRegistryPath`, `baseOSFromRepo`, `deriveBaseOS` signature, `attachReleaseCveScans` filter)
- Modify: `backend/internal/api/release_artifacts_test.go` (helper + builder + attach tests)

**Acceptance Criteria:**
- [ ] Two `.containerinfo` binaries with the same project+package but repos `ubi8`/`ubi9` → two artifacts with base OS "UBI 8"/"UBI 9" and registry paths `.../containers/ubi8/<name>` and `.../ubi9/<name>`
- [ ] Old-layout binary (repo `images`, project `…:containers:ubi9`) → one artifact, base OS "UBI 9", registry `.../containers/ubi9/images/<name>` (byte-identical to pre-change)
- [ ] `baseOSFromRepo`: ubi8/ubi9/noble/bookworm mapped; `images`/unknown/"" → ""
- [ ] `attachReleaseCveScans`: a package with ubi8 (clean) and ubi9 (CVEs) scan rows → the ubi9 artifact gets only the ubi9 scan, ubi8 only the ubi8 scan
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `cd backend && go test ./internal/api/ -run 'TestBaseOSFromRepo|TestDeriveBaseOS|TestContainerRegistryPath|TestBuildReleaseContainerArtifactsPerRepo|TestAttachReleaseCveScansFiltersByRepo' -count=1 -v && go build ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/api/release_artifacts_test.go` (ensure the import block includes `context`, `net/http`, `net/http/httptest`, `time`, `github.com/percona/obs-dashboard/internal/model`, `github.com/percona/obs-dashboard/internal/obs`, `github.com/percona/obs-dashboard/internal/store` — add any missing):

```go
func TestBaseOSFromRepo(t *testing.T) {
	cases := map[string]string{
		"ubi8": "UBI 8", "ubi9": "UBI 9",
		"noble": "Ubuntu 24.04 Noble", "bookworm": "Debian 12 Bookworm",
		"images": "", "": "", "weird": "",
	}
	for repo, want := range cases {
		if got := baseOSFromRepo(repo); got != want {
			t.Errorf("baseOSFromRepo(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestDeriveBaseOS(t *testing.T) {
	// New layout: the repo carries the base image.
	if got := deriveBaseOS("isv:percona:ppg:staging:17:containers", "ubi9"); got != "UBI 9" {
		t.Errorf("new layout: got %q, want UBI 9", got)
	}
	// Old layout: repo is "images", base image in the project name.
	if got := deriveBaseOS("isv:percona:ppg:17:containers:ubi8", "images"); got != "UBI 8" {
		t.Errorf("old layout: got %q, want UBI 8", got)
	}
	// Unrecognisable → project-name fallback returns the project string.
	if got := deriveBaseOS("isv:percona:ppg:weird", "images"); got != "isv:percona:ppg:weird" {
		t.Errorf("fallback: got %q, want the project string", got)
	}
}

func TestContainerRegistryPath(t *testing.T) {
	if got := containerRegistryPath("isv:percona:ppg:staging:17:containers", "ubi9", "pg"); got != "registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi9/pg" {
		t.Errorf("new layout: got %q", got)
	}
	// Old layout (repo=images) reproduces the pre-change path exactly.
	if got := containerRegistryPath("isv:percona:ppg:17:containers:ubi9", "images", "pg"); got != "registry.opensuse.org/isv/percona/ppg/17/containers/ubi9/images/pg" {
		t.Errorf("old layout: got %q", got)
	}
}

func TestBuildReleaseContainerArtifactsPerRepo(t *testing.T) {
	// Tags endpoint 404s → tags stay empty (tolerated); we assert keying,
	// base OS, registry, and repo per artifact.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := obs.NewClient(srv.URL, "u", "p")

	binaries := []obs.BinaryArtifact{
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pg", Repo: "ubi8", Arch: "x86_64", Filename: "pg.containerinfo"},
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pg", Repo: "ubi9", Arch: "x86_64", Filename: "pg.containerinfo"},
	}
	out := buildReleaseContainerArtifacts(context.Background(), client, binaries)
	if len(out) != 2 {
		t.Fatalf("got %d artifacts, want 2 (ubi8 + ubi9)", len(out))
	}
	byOS := map[string]ReleaseContainerArtifact{}
	for _, a := range out {
		byOS[a.BaseOS] = a
	}
	if byOS["UBI 8"].Registry != "registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi8/pg" {
		t.Errorf("ubi8 registry = %q", byOS["UBI 8"].Registry)
	}
	if byOS["UBI 9"].Registry != "registry.opensuse.org/isv/percona/ppg/staging/17/containers/ubi9/pg" {
		t.Errorf("ubi9 registry = %q", byOS["UBI 9"].Registry)
	}
	if byOS["UBI 8"].Repo != "ubi8" || byOS["UBI 9"].Repo != "ubi9" {
		t.Errorf("repo not set per artifact: %+v", out)
	}
}

func TestAttachReleaseCveScansFiltersByRepo(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	seed := func(repo string, crit int) {
		if err := store.UpsertCveScan(db, "isv:percona:ppg:staging:17:containers", "pg",
			model.CveScan{Repo: repo, Arch: "x86_64", ImageRef: "r", ScannedAt: now, CriticalCount: crit}); err != nil {
			t.Fatal(err)
		}
	}
	seed("ubi8", 0)
	seed("ubi9", 5)

	images := []ReleaseContainerArtifact{
		{Project: "isv:percona:ppg:staging:17:containers", ImageName: "pg", Repo: "ubi8"},
		{Project: "isv:percona:ppg:staging:17:containers", ImageName: "pg", Repo: "ubi9"},
	}
	attachReleaseCveScans(db, images)
	for _, img := range images {
		if len(img.CveScans) != 1 || img.CveScans[0].Repo != img.Repo {
			t.Fatalf("%s: got %d scans (want 1 for its own repo): %+v", img.Repo, len(img.CveScans), img.CveScans)
		}
	}
	// ubi9 carries the CVE, ubi8 is clean.
	for _, img := range images {
		if img.Repo == "ubi9" && img.CveScans[0].CriticalCount != 5 {
			t.Fatalf("ubi9 critical = %d, want 5", img.CveScans[0].CriticalCount)
		}
		if img.Repo == "ubi8" && img.CveScans[0].CriticalCount != 0 {
			t.Fatalf("ubi8 critical = %d, want 0", img.CveScans[0].CriticalCount)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/api/ -run 'TestBaseOSFromRepo|TestDeriveBaseOS|TestContainerRegistryPath|TestBuildReleaseContainerArtifactsPerRepo|TestAttachReleaseCveScansFiltersByRepo' -v`
Expected: compile failure — `undefined: baseOSFromRepo`, `deriveBaseOS` arg count, `ReleaseContainerArtifact.Repo` unknown.

- [ ] **Step 3: Add the `Repo` field**

In `backend/internal/api/release_artifacts.go`, add `Repo` to `ReleaseContainerArtifact` (after `ImageName`):

```go
type ReleaseContainerArtifact struct {
	Project   string          `json:"project"`
	ImageName string          `json:"image_name"`
	Repo      string          `json:"repo"`
	BaseOS    string          `json:"base_os"`
	Registry  string          `json:"registry"`
	Tags      []string        `json:"tags"`
	PullCmd   string          `json:"pull_cmd"`
	MTime     int64           `json:"mtime"`
	BuiltAt   string          `json:"built_at"`
	CveScans  []model.CveScan `json:"cve_scans,omitempty"`
}
```

- [ ] **Step 4: Add the helpers and rework `deriveBaseOS`**

Replace the existing `deriveBaseOS` function with the two helpers plus the reworked signature:

```go
// baseOSFromRepo maps a base-image build repo (ubi8/ubi9/noble/bookworm) to a
// display label. Returns "" for the legacy "images" repo or an unknown repo,
// so the caller falls back to parsing the project name.
func baseOSFromRepo(repo string) string {
	switch repo {
	case "ubi8":
		return "UBI 8"
	case "ubi9":
		return "UBI 9"
	case "noble":
		return "Ubuntu 24.04 Noble"
	case "bookworm":
		return "Debian 12 Bookworm"
	default:
		return ""
	}
}

// containerRegistryPath builds the OBS registry pull path for a container
// package built in a given repo. The base-image repo (ubi8/ubi9/…) is a path
// segment; legacy release containers use the repo literally named "images",
// reproducing the pre-restructure path. (Same formula as cve.ImageBase; kept
// local to avoid an api→cve import for a one-liner.)
func containerRegistryPath(project, repo, name string) string {
	return "registry.opensuse.org/" + strings.ReplaceAll(project, ":", "/") + "/" + repo + "/" + name
}

// deriveBaseOS returns the base-OS display label for a container build,
// preferring the repo (new layout, where the base image is the build repo)
// and falling back to the project name (legacy layout, where the base image
// is a `containers:<baseos>` project segment). The last resort is the project
// string itself.
func deriveBaseOS(project, repo string) string {
	if os := baseOSFromRepo(repo); os != "" {
		return os
	}
	parts := strings.Split(project, ":")
	for i, part := range parts {
		if part == "containers" && i+1 < len(parts) {
			if os := baseOSFromRepo(parts[i+1]); os != "" {
				return os
			}
			return parts[i+1]
		}
	}
	return project
}
```

Note: `containerRegistryPath` does not lowercase the project — matching the pre-change release path exactly (project names are already lowercase). The `baseOSFromRepo` reuse inside the project-name branch preserves the old label mapping (ubi8/ubi9/noble/bookworm) while DRYing it.

- [ ] **Step 5: Rework the builder keying/construction**

In `buildReleaseContainerArtifacts`, change the key and the artifact construction (the `if artifact == nil` block):

```go
		key := binary.Project + "\x00" + binary.Package + "\x00" + binary.Repo
		artifact := byKey[key]
		if artifact == nil {
			artifact = &ReleaseContainerArtifact{
				Project:   binary.Project,
				ImageName: binary.Package,
				Repo:      binary.Repo,
				BaseOS:    deriveBaseOS(binary.Project, binary.Repo),
				Registry:  containerRegistryPath(binary.Project, binary.Repo, binary.Package),
			}
			byKey[key] = artifact
			seenTags[key] = map[string]bool{}
		}
```

Everything below (`MTime`/`BuiltAt`, the tag accumulation keyed on `key`, the `out`/`PullCmd`/sort block) is unchanged — it now operates per `(project, package, repo)` automatically.

- [ ] **Step 6: Filter CVE scans by repo in `attachReleaseCveScans`**

```go
func attachReleaseCveScans(db *sql.DB, images []ReleaseContainerArtifact) {
	for i := range images {
		scans, err := store.QueryCveScans(db, images[i].Project, images[i].ImageName)
		if err != nil {
			slog.Warn("api: release cve scans", "pkg", images[i].ImageName, "err", err)
			continue
		}
		var filtered []model.CveScan
		for _, s := range scans {
			if s.Repo == images[i].Repo {
				filtered = append(filtered, s)
			}
		}
		images[i].CveScans = filtered
	}
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ -count=1 && go test ./... -count=1 && go build ./... && gofmt -l internal/api`
Expected: all PASS, build OK; gofmt may still list the pre-existing `release_artifacts.go` drift — run `gofmt -w internal/api/release_artifacts.go internal/api/release_artifacts_test.go` on the touched files so at least they are clean, and confirm the diff shows only intended changes.

- [ ] **Step 8: Commit**

```bash
git add internal/api/release_artifacts.go internal/api/release_artifacts_test.go
git commit -s -m "fix(artifacts): distinguish ubi8/ubi9 container images by build repo"
```
