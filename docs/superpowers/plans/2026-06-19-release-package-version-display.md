# Release Package Version Display Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the package version (EVR) badge in the Artifacts tab package list for release contexts, where it currently shows blank.

**Architecture:** A new OBS client method `RepoBinaryVersions` calls the per-repo-arch `_repository?view=binaryversions&withevr=1` API; `buildReleaseArtifacts` fans out one goroutine per distinct `(repo, arch)` pair to fetch versions and merges them into a lookup map passed to `buildReleasePackageArtifacts`; `ReleasePackageArtifact` gains a `Version` field; the frontend one-liner reads `pkg.version` instead of hardcoding `''`.

**Tech Stack:** Go (`encoding/xml`, `sync`, `net/url`), Vue 3 / TypeScript

**User decisions (already made):**
- Approach: per-repo-arch parallel fetch inside `buildReleaseArtifacts` (not separate endpoint, not enriching ProjectBinaryList)
- Version format: full EVR (`version-release`), epoch prefix (`N:`) stripped if present

---

## File Map

| File | Change |
|------|--------|
| `backend/internal/obs/client.go` | Add `RepoBinaryVersions` method + unexported `stripEpoch` helper |
| `backend/internal/obs/client_test.go` | Add 3 tests for `RepoBinaryVersions` |
| `backend/internal/api/release_artifacts.go` | Add `Version` to struct; fan-out in `buildReleaseArtifacts`; update `buildReleasePackageArtifacts` signature |
| `backend/internal/api/release_artifacts_test.go` | Update existing test call-site; add version-population test |
| `frontend/src/components/ArtifactsPanel.vue` | Add `version?` to local TS interface; read `pkg.version` in row mapper |

---

### Task 1: OBS client `RepoBinaryVersions`

**Goal:** Add a method to the OBS client that fetches `filename → evr` for all binaries in a single `(project, repo, arch)` target, with epoch stripped.

**Files:**
- Modify: `backend/internal/obs/client.go`
- Modify: `backend/internal/obs/client_test.go`

**Acceptance Criteria:**
- [ ] `RepoBinaryVersions` calls `/build/{project}/{repo}/{arch}/_repository?view=binaryversions&withevr=1`
- [ ] Returns a `map[string]string` of `filename → evr`
- [ ] Epoch prefix (e.g. `2:`) is stripped — `2:16.4-2.3` → `16.4-2.3`; no epoch → returned as-is
- [ ] Empty `<binaryversionlist>` returns an empty map, not an error
- [ ] `go test ./backend/internal/obs/... -run TestRepoBinaryVersions` passes (3 tests)

**Verify:** `cd backend && go test ./internal/obs/... -run TestRepoBinaryVersions -v` → 3 tests PASS

**Steps:**

- [ ] **Step 1: Write the three failing tests**

Append to `backend/internal/obs/client_test.go`:

```go
func TestRepoBinaryVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/build/isv:percona:ppg:releases:17/openSUSE_Tumbleweed/x86_64/_repository" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("view") != "binaryversions" || r.URL.Query().Get("withevr") != "1" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist>
			<binary name="etcd-3.5.30-2.1.x86_64.rpm" sizek="17985" evr="3.5.30-2.1" arch="x86_64"/>
			<binary name="etcd-debugsource-3.5.30-2.1.x86_64.rpm" sizek="100" evr="3.5.30-2.1" arch="x86_64"/>
		</binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(),
		"isv:percona:ppg:releases:17", "openSUSE_Tumbleweed", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(versions), versions)
	}
	if versions["etcd-3.5.30-2.1.x86_64.rpm"] != "3.5.30-2.1" {
		t.Errorf("expected '3.5.30-2.1', got %q", versions["etcd-3.5.30-2.1.x86_64.rpm"])
	}
	if versions["etcd-debugsource-3.5.30-2.1.x86_64.rpm"] != "3.5.30-2.1" {
		t.Errorf("expected '3.5.30-2.1', got %q", versions["etcd-debugsource-3.5.30-2.1.x86_64.rpm"])
	}
}

func TestRepoBinaryVersionsStripsEpoch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist>
			<binary name="postgresql16-16.4-2.3.x86_64.rpm" sizek="1234" evr="2:16.4-2.3" arch="x86_64"/>
		</binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(), "proj", "repo", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if versions["postgresql16-16.4-2.3.x86_64.rpm"] != "16.4-2.3" {
		t.Errorf("expected epoch stripped to '16.4-2.3', got %q",
			versions["postgresql16-16.4-2.3.x86_64.rpm"])
	}
}

func TestRepoBinaryVersionsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist></binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(), "proj", "repo", "arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("expected empty map, got %v", versions)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
cd backend && go test ./internal/obs/... -run TestRepoBinaryVersions -v
```

Expected: compile error `c.RepoBinaryVersions undefined`

- [ ] **Step 3: Add `stripEpoch` helper and `RepoBinaryVersions` to `client.go`**

Add after the `PackageContainerTags` method (end of `backend/internal/obs/client.go`):

```go
// stripEpoch removes the "N:" epoch prefix from an EVR string if present.
// "2:16.4-2.3" → "16.4-2.3"; "3.5.30-2.1" → "3.5.30-2.1" (unchanged).
func stripEpoch(evr string) string {
	if i := strings.Index(evr, ":"); i >= 0 {
		return evr[i+1:]
	}
	return evr
}

// RepoBinaryVersions returns a map of filename → evr for all binaries in the
// given (project, repo, arch) target. Epoch prefixes are stripped from the evr
// values. Returns an empty map (not an error) if the list is empty.
func (c *Client) RepoBinaryVersions(ctx context.Context, project, repo, arch string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/_repository?view=binaryversions&withevr=1",
		url.PathEscape(project), url.PathEscape(repo), url.PathEscape(arch))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Binaries []struct {
			Name string `xml:"name,attr"`
			EVR  string `xml:"evr,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse binaryversionlist for %s/%s/%s: %w", project, repo, arch, err)
	}

	out := make(map[string]string, len(raw.Binaries))
	for _, b := range raw.Binaries {
		if b.Name != "" && b.EVR != "" {
			out[b.Name] = stripEpoch(b.EVR)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
cd backend && go test ./internal/obs/... -run TestRepoBinaryVersions -v
```

Expected: `PASS` for all 3 tests

- [ ] **Step 5: Commit**

```bash
git add backend/internal/obs/client.go backend/internal/obs/client_test.go
git commit -s -m "feat(obs): add RepoBinaryVersions client method"
```

---

### Task 2: Enrich release package artifacts with version

**Goal:** `ReleasePackageArtifact` gains a `Version` field populated from a parallel fan-out of `RepoBinaryVersions` calls, one per distinct `(repo, arch)` in the release.

**Files:**
- Modify: `backend/internal/api/release_artifacts.go`
- Modify: `backend/internal/api/release_artifacts_test.go`

**Acceptance Criteria:**
- [ ] `ReleasePackageArtifact` has `Version string json:"version"`
- [ ] `buildReleaseArtifacts` fans out one goroutine per distinct `(repo, arch)` from distributable binaries, calls `client.RepoBinaryVersions`, merges into a lookup map
- [ ] Version-fetch errors are non-fatal (affected packages get empty `Version`)
- [ ] `buildReleasePackageArtifacts` sets `artifact.Version` from the lookup on the first matching binary filename
- [ ] Existing test `TestBuildReleasePackageArtifactsUsesDistributableMTime` still passes (call-site updated)
- [ ] New test `TestBuildReleasePackageArtifactsVersion` passes
- [ ] `cd backend && go test ./internal/api/... -run TestBuildReleasePackage -v` → all PASS

**Verify:** `cd backend && go test ./internal/api/... -run TestBuildReleasePackage -v` → all tests PASS

**Steps:**

- [ ] **Step 1: Write the failing new test and update the existing test call-site**

In `backend/internal/api/release_artifacts_test.go`, update the existing call and add the new test:

```go
package api

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
)

func TestBuildReleasePackageArtifactsUsesDistributableMTime(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-debugsource-3.5.30-2.1.x86_64.rpm",
			Size:     20,
			MTime:    1779202000,
			BuiltAt:  time.Unix(1779202000, 0).UTC(),
		},
	}

	artifacts := buildReleasePackageArtifacts(items, nil) // nil versions → empty Version
	if len(artifacts) != 1 {
		t.Fatalf("expected one package artifact, got %d", len(artifacts))
	}
	if len(artifacts[0].Binaries) != 1 {
		t.Fatalf("expected one distributable binary, got %d", len(artifacts[0].Binaries))
	}
	if artifacts[0].BuiltAt != "2026-05-19T14:46:13Z" {
		t.Fatalf("BuiltAt = %q", artifacts[0].BuiltAt)
	}
	if artifacts[0].Binaries[0].MTime != 1779201973 {
		t.Fatalf("binary MTime = %d", artifacts[0].Binaries[0].MTime)
	}
}

func TestBuildReleasePackageArtifactsVersion(t *testing.T) {
	items := []obs.BinaryArtifact{
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "openSUSE_Tumbleweed",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd-3.5.30-2.1.x86_64.rpm",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
		{
			Project:  "isv:percona:ppg:releases:17",
			Repo:     "Ubuntu_24.04",
			Arch:     "x86_64",
			Package:  "etcd",
			Filename: "etcd_3.5.30-2ubuntu1_amd64.deb",
			Size:     10,
			MTime:    1779201973,
			BuiltAt:  time.Unix(1779201973, 0).UTC(),
		},
	}

	versions := map[string]string{
		"openSUSE_Tumbleweed\x00x86_64\x00etcd-3.5.30-2.1.x86_64.rpm": "3.5.30-2.1",
		// Ubuntu_24.04 intentionally absent — Version should stay ""
	}

	artifacts := buildReleasePackageArtifacts(items, versions)

	var openSUSE, ubuntu *ReleasePackageArtifact
	for i := range artifacts {
		switch artifacts[i].Repo {
		case "openSUSE_Tumbleweed":
			openSUSE = &artifacts[i]
		case "Ubuntu_24.04":
			ubuntu = &artifacts[i]
		}
	}

	if openSUSE == nil {
		t.Fatal("openSUSE artifact missing")
	}
	if openSUSE.Version != "3.5.30-2.1" {
		t.Errorf("openSUSE Version = %q, want '3.5.30-2.1'", openSUSE.Version)
	}
	if ubuntu == nil {
		t.Fatal("Ubuntu artifact missing")
	}
	if ubuntu.Version != "" {
		t.Errorf("Ubuntu Version = %q, want ''", ubuntu.Version)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```
cd backend && go test ./internal/api/... -run TestBuildReleasePackage -v
```

Expected: compile error (wrong number of arguments to `buildReleasePackageArtifacts`)

- [ ] **Step 3: Add `Version` field to `ReleasePackageArtifact`**

In `backend/internal/api/release_artifacts.go`, update the struct:

```go
type ReleasePackageArtifact struct {
	Project  string           `json:"project"`
	Name     string           `json:"name"`
	Version  string           `json:"version"`
	Repo     string           `json:"repo"`
	RepoName string           `json:"repo_name"`
	RepoType string           `json:"repo_type"`
	Arch     string           `json:"arch"`
	Binaries []ArtifactBinary `json:"binaries"`
	BuiltAt  string           `json:"built_at"`
}
```

- [ ] **Step 4: Update `buildReleasePackageArtifacts` signature and body**

Replace the existing `buildReleasePackageArtifacts` function in `release_artifacts.go` with:

```go
// buildReleasePackageArtifacts groups distributable binaries into per-package
// artifacts. versions is a lookup map keyed by "repo\x00arch\x00filename" → evr;
// pass nil if version data is unavailable.
func buildReleasePackageArtifacts(binaries []obs.BinaryArtifact, versions map[string]string) []ReleasePackageArtifact {
	byKey := map[string]*ReleasePackageArtifact{}
	latestMTime := map[string]int64{}
	for _, binary := range binaries {
		if !obs.IsDistributableBinary(binary.Filename) {
			continue
		}
		key := binary.Project + "\x00" + binary.Package + "\x00" + binary.Repo + "\x00" + binary.Arch
		artifact := byKey[key]
		if artifact == nil {
			artifact = &ReleasePackageArtifact{
				Project:  binary.Project,
				Name:     binary.Package,
				Repo:     binary.Repo,
				RepoName: repoDisplayName(binary.Repo),
				RepoType: repoType(binary.Repo),
				Arch:     binary.Arch,
			}
			byKey[key] = artifact
		}
		if artifact.Version == "" {
			if evr, ok := versions[binary.Repo+"\x00"+binary.Arch+"\x00"+binary.Filename]; ok {
				artifact.Version = evr
			}
		}
		artifact.Binaries = append(artifact.Binaries, releaseBinary(binary))
		if binary.MTime > latestMTime[key] {
			latestMTime[key] = binary.MTime
			artifact.BuiltAt = binary.BuiltAt.Format(time.RFC3339)
		}
	}

	out := make([]ReleasePackageArtifact, 0, len(byKey))
	for _, artifact := range byKey {
		sort.Slice(artifact.Binaries, func(i, j int) bool {
			return artifact.Binaries[i].Filename < artifact.Binaries[j].Filename
		})
		out = append(out, *artifact)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Arch != out[j].Arch {
			return out[i].Arch < out[j].Arch
		}
		return out[i].Name < out[j].Name
	})
	return out
}
```

- [ ] **Step 5: Add parallel version fan-out to `buildReleaseArtifacts`**

Replace the existing `buildReleaseArtifacts` function in `release_artifacts.go` with:

```go
func buildReleaseArtifacts(ctx context.Context, client *obs.Client, root, version string) (ReleaseArtifactsResponse, error) {
	project := fmt.Sprintf("%s:ppg:releases:%s", root, version)
	binaries, err := client.ProjectBinaryList(ctx, project)
	if err != nil {
		return ReleaseArtifactsResponse{}, err
	}

	containerProjects, err := client.SearchProjects(ctx, project+":containers")
	if err != nil {
		return ReleaseArtifactsResponse{}, err
	}

	var containerBinaries []obs.BinaryArtifact
	for _, containerProject := range containerProjects {
		items, err := client.ProjectBinaryList(ctx, containerProject)
		if err != nil {
			return ReleaseArtifactsResponse{}, err
		}
		containerBinaries = append(containerBinaries, items...)
	}

	// Fetch binary EVR versions: one goroutine per distinct (repo, arch) pair.
	type repoArch struct{ repo, arch string }
	pairs := map[repoArch]struct{}{}
	for _, b := range binaries {
		if obs.IsDistributableBinary(b.Filename) {
			pairs[repoArch{b.Repo, b.Arch}] = struct{}{}
		}
	}
	var (
		vmu      sync.Mutex
		versions = make(map[string]string) // repo+"\x00"+arch+"\x00"+filename → evr
		vwg      sync.WaitGroup
	)
	for ra := range pairs {
		ra := ra
		vwg.Add(1)
		go func() {
			defer vwg.Done()
			m, err := client.RepoBinaryVersions(ctx, project, ra.repo, ra.arch)
			if err != nil {
				return // non-fatal: version stays empty for this repo/arch
			}
			vmu.Lock()
			for filename, evr := range m {
				versions[ra.repo+"\x00"+ra.arch+"\x00"+filename] = evr
			}
			vmu.Unlock()
		}()
	}
	vwg.Wait()

	response := ReleaseArtifactsResponse{
		Version:         version,
		RefreshedAt:     time.Now().UTC().Format(time.RFC3339),
		Packages:        buildReleasePackageArtifacts(binaries, versions),
		ContainerImages: buildReleaseContainerArtifacts(ctx, client, containerBinaries),
	}
	return response, nil
}
```

- [ ] **Step 6: Run tests to confirm they pass**

```
cd backend && go test ./internal/api/... -run TestBuildReleasePackage -v
```

Expected: `PASS` for both tests

- [ ] **Step 7: Run the full backend test suite**

```
cd backend && go test ./...
```

Expected: all tests pass, no compile errors

- [ ] **Step 8: Commit**

```bash
git add backend/internal/api/release_artifacts.go backend/internal/api/release_artifacts_test.go
git commit -s -m "feat(api): enrich release package artifacts with binary version"
```

---

### Task 3: Frontend — read version from release artifact

**Goal:** The release row mapper in `ArtifactsPanel.vue` reads `pkg.version` instead of hardcoding `''`, so the existing version badge in `PackagesSubTab.vue` renders the EVR.

**Files:**
- Modify: `frontend/src/components/ArtifactsPanel.vue`

**Acceptance Criteria:**
- [ ] The local `ReleasePackageArtifact` TypeScript interface includes `version?: string`
- [ ] The release row mapper uses `version: pkg.version ?? ''` instead of `version: ''`
- [ ] `cd frontend && npm run build` completes with no TypeScript errors

**Verify:** `cd frontend && npm run build` → `built in X.XXs` with no errors

**Steps:**

- [ ] **Step 1: Add `version?` to the local `ReleasePackageArtifact` interface**

In `frontend/src/components/ArtifactsPanel.vue`, find the local `ReleasePackageArtifact` interface (near the bottom of `<script setup>`):

```typescript
interface ReleasePackageArtifact {
  project: string
  name: string
  repo: string
  repo_name: string
  repo_type: 'rpm' | 'deb'
  arch: string
  binaries: ArtifactBinary[]
  built_at: string
}
```

Replace with:

```typescript
interface ReleasePackageArtifact {
  project: string
  name: string
  version?: string
  repo: string
  repo_name: string
  repo_type: 'rpm' | 'deb'
  arch: string
  binaries: ArtifactBinary[]
  built_at: string
}
```

- [ ] **Step 2: Update the row mapper to use `pkg.version`**

In the `packageRows` computed near the release row mapper, find:

```typescript
      version: '',
```

Replace with:

```typescript
      version: pkg.version ?? '',
```

- [ ] **Step 3: Build to verify no TypeScript errors**

```
cd frontend && npm run build
```

Expected: `✓ built in X.XXs` with no errors

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/ArtifactsPanel.vue
git commit -s -m "feat(frontend): show package version in release artifacts list"
```
