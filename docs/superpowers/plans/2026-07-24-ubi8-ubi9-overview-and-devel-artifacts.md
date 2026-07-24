# ubi8/ubi9 in Overview CVE Table + devel Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Distinguish ubi8 and ubi9 container images in the Overview "CVE exposure by project" table and the devel Artifacts "Container Images" sub-tab, by keying on the build repo (repo-first base-OS derivation, project-name fallback) — consistent with the already-merged release-artifacts and CVE-scan fixes.

**Architecture:** Part A (Overview): `OverviewCveScan`/`OverviewCvePeriod` gain `Repo`; `buildOverviewSnapshot` keys images by `(project, package, repo)` with `BaseOS` via the existing `deriveBaseOS(project, repo)`; the frontend table shows `name · base_os` and filters the per-row report to the row's repo. Part B (devel Artifacts, frontend only): `useArtifacts.ts` fans out each container package into one image per distinct target repo, deriving base OS + registry from the repo. Parts touch disjoint files.

**Tech Stack:** Go (modernc sqlite), Vue 3 + TS.

**User decisions (already made):**
- Both surfaces key on the build repo; base OS repo-first with project-name fallback (old `images`-repo containers keep working).
- Overview rows labelled `name · BaseOS`, retiring the `imageSuffix` project-suffix hack; per-row CVE report filtered to the row's repo.
- devel container images fan out to one image per distinct target repo.
- Combined into one effort (two disjoint-file parts).
- Frontend has no runtime test runner (only `.test-d.ts` type checks via `vue-tsc`); frontend logic is verified by `npm run build` + backend tests + visual check.

Spec: `docs/superpowers/specs/2026-07-24-ubi8-ubi9-overview-and-devel-artifacts-design.md`

**Conventions:** backend commands from `backend/`, frontend from `frontend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: Overview CVE aggregation keyed by repo (backend)

**Goal:** `buildOverviewSnapshot` produces one `OverviewImage` per `(project, package, repo)` with `Repo` + `BaseOS`; store queries carry `repo`.

**Files:**
- Modify: `backend/internal/store/overview.go` (`OverviewCveScan.Repo`, `OverviewCvePeriod.Repo`, both queries)
- Modify: `backend/internal/api/overview.go` (`OverviewImage` fields, `imgKey`, aggregation)
- Modify: `backend/internal/api/overview_test.go` (new split-by-repo test)

**Acceptance Criteria:**
- [ ] Two scan rows (same project+package `isv:percona:ppg:staging:17:containers`/`pdp`, repos `ubi8`/`ubi9`, different counts) → two `OverviewImage`s with distinct `Repo` ("ubi8"/"ubi9") and `BaseOS` ("UBI 8"/"UBI 9") and independent Critical/High/OldestOpenDays
- [ ] Existing `TestOverviewSnapshotBuilder` still passes (its scans have empty `Repo` → one image, `BaseOS` from the project fallback)
- [ ] `QueryAllCveScans`/`QueryAllCvePeriods` select and scan `repo`
- [ ] `go test ./... -count=1 && go build ./...` pass

**Verify:** `cd backend && go test ./internal/api/ ./internal/store/ -count=1 -v && go build ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/api/overview_test.go` (reuses `buildOverviewSnapshot`, `ptrTime`, `day`, `findProject` already in the file):

```go
func TestOverviewSnapshotSplitsByRepo(t *testing.T) {
	now := time.Now().UTC()
	scans := []store.OverviewCveScan{
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pdp", Repo: "ubi8", Arch: "x86_64", Critical: 0, High: 1, CveSince: nil},
		{Project: "isv:percona:ppg:staging:17:containers", Package: "pdp", Repo: "ubi9", Arch: "x86_64", Critical: 3, High: 2, CveSince: ptrTime(day(5))},
	}
	s := buildOverviewSnapshot("isv:percona", "24h", now, nil, nil, scans, nil)

	var imgs []OverviewImage
	for _, pr := range s.Projects {
		for _, img := range pr.Images {
			if img.Name == "pdp" {
				imgs = append(imgs, img)
			}
		}
	}
	if len(imgs) != 2 {
		t.Fatalf("want 2 pdp images (ubi8 + ubi9), got %d: %+v", len(imgs), imgs)
	}
	byOS := map[string]OverviewImage{}
	for _, img := range imgs {
		byOS[img.BaseOS] = img
	}
	if byOS["UBI 8"].Repo != "ubi8" || byOS["UBI 8"].Critical != 0 || byOS["UBI 8"].High != 1 {
		t.Fatalf("ubi8 image wrong: %+v", byOS["UBI 8"])
	}
	if byOS["UBI 9"].Repo != "ubi9" || byOS["UBI 9"].Critical != 3 || byOS["UBI 9"].High != 2 {
		t.Fatalf("ubi9 image wrong: %+v", byOS["UBI 9"])
	}
	if byOS["UBI 9"].OldestOpenDays != 5 {
		t.Fatalf("ubi9 oldest_open_days = %d, want 5", byOS["UBI 9"].OldestOpenDays)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/api/ -run TestOverviewSnapshotSplitsByRepo -v`
Expected: compile failure — `OverviewCveScan.Repo` / `OverviewImage.Repo`/`BaseOS` undefined.

- [ ] **Step 3: Store — add `Repo` to the overview rows**

In `backend/internal/store/overview.go`:

`OverviewCveScan` — add `Repo` after `Package`:
```go
type OverviewCveScan struct {
	Project  string
	Package  string
	Repo     string
	Arch     string
	Critical int
	High     int
	CveSince *time.Time // nil for clean images or pre-age-tracking rows
}
```

`QueryAllCveScans` — select and scan `repo`:
```go
	rows, err := db.Query(`
		SELECT project, package, repo, arch, critical_count, high_count, cve_since FROM cve_scans`)
```
```go
		if err := rows.Scan(&s.Project, &s.Package, &s.Repo, &s.Arch, &s.Critical, &s.High, &since); err != nil {
			return nil, err
		}
```

`OverviewCvePeriod` — add `Repo` after `Package`:
```go
type OverviewCvePeriod struct {
	Project    string
	Package    string
	Repo       string
	CveSince   time.Time
	CleanSince time.Time
}
```

`QueryAllCvePeriods` — select and scan `repo`:
```go
	rows, err := db.Query(`SELECT project, package, repo, cve_since, clean_since FROM cve_periods`)
```
```go
		if err := rows.Scan(&p.Project, &p.Package, &p.Repo, &cs, &cl); err != nil {
			return nil, err
		}
```

- [ ] **Step 4: API — key images by repo**

In `backend/internal/api/overview.go`:

`OverviewImage` — add `Repo` + `BaseOS`:
```go
type OverviewImage struct {
	Project        string `json:"project"` // raw OBS project (logical rows can aggregate several)
	Name           string `json:"name"`
	Repo           string `json:"repo"`
	BaseOS         string `json:"base_os"`
	Critical       int    `json:"critical"`
	High           int    `json:"high"`
	OldestOpenDays int    `json:"oldest_open_days"` // 0 = none open / unknown
	AvgFixDays     int    `json:"avg_fix_days"`     // 0 = no closed episodes yet
}
```

Change `imgKey` and the two aggregation loops. The key gains `repo`; the image is created with `Repo`/`BaseOS`; the periods loop and the final assembly key include `repo`:
```go
	type imgKey struct{ project, pkg, repo string }
	imgSince := map[imgKey]*time.Time{}
	imgAt := map[imgKey]*OverviewImage{}
	imgLogical := map[imgKey]string{}
	for _, s := range scans {
		logical := logicalProject(root, s.Project)
		if logical == "" {
			continue
		}
		k := imgKey{s.Project, s.Package, s.Repo}
		img, ok := imgAt[k]
		if !ok {
			img = &OverviewImage{Project: s.Project, Name: s.Package, Repo: s.Repo, BaseOS: deriveBaseOS(s.Project, s.Repo)}
			imgAt[k] = img
			imgLogical[k] = logical
		}
		if s.Critical > img.Critical {
			img.Critical = s.Critical
		}
		if s.High > img.High {
			img.High = s.High
		}
		if (s.Critical > 0 || s.High > 0) && s.CveSince != nil {
			if cur := imgSince[k]; cur == nil || s.CveSince.Before(*cur) {
				imgSince[k] = s.CveSince
			}
		}
	}
	for k, since := range imgSince {
		imgAt[k].OldestOpenDays = int(now.Sub(*since).Hours() / 24)
	}

	fixDays := map[imgKey][]float64{}
	for _, p := range periods {
		k := imgKey{p.Project, p.Package, p.Repo}
		fixDays[k] = append(fixDays[k], p.CleanSince.Sub(p.CveSince).Hours()/24)
	}
	for k, days := range fixDays {
		img, ok := imgAt[k]
		if !ok {
			continue // period for an image with no current scan row
		}
		sum := 0.0
		for _, d := range days {
			sum += d
		}
		img.AvgFixDays = int(sum/float64(len(days)) + 0.5)
	}

	for k, img := range imgAt {
		getAgg(imgLogical[k]).images[k.project+"/"+k.pkg+"/"+k.repo] = img
	}
```

(`deriveBaseOS(project, repo string)` already exists in `release_artifacts.go`, same `api` package.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && go test ./internal/api/ ./internal/store/ -count=1 && go test ./... -count=1 && go build ./... && gofmt -l internal/api internal/store`
Expected: all PASS (incl. the unchanged `TestOverviewSnapshotBuilder`), build OK; gofmt may list pre-existing drift (release_artifacts.go / packages_test.go) — leave it, the files you edited must be clean.

- [ ] **Step 6: Commit**

```bash
git add internal/store/overview.go internal/api/overview.go internal/api/overview_test.go
git commit -s -m "feat(overview): key CVE-exposure images by build repo (ubi8/ubi9)"
```

---

### Task 2: Overview CVE table repo·base-OS rows (frontend)

**Goal:** The exposure table labels each row `name · base_os`, expands each repo independently, and shows only that repo's findings.

**Files:**
- Modify: `frontend/src/types/overview.ts` (`OverviewImage` fields)
- Modify: `frontend/src/components/CveExposureTable.vue` (label, imgKey, report filter; remove `imageSuffix`)

**Acceptance Criteria:**
- [ ] `OverviewImage` type has `repo: string` and `base_os: string`
- [ ] Rows render `{{ img.name }} · {{ img.base_os }}`; `imageSuffix` and its usage are gone
- [ ] `imgKey` is `project + '/' + name + '/' + repo`; the report fetch filters returned scans to `s.repo === img.repo`
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Type fields**

In `frontend/src/types/overview.ts`, add to `OverviewImage` (after `name`):
```ts
  repo: string
  base_os: string
```

- [ ] **Step 2: Label — remove `imageSuffix`, show `base_os`**

In `frontend/src/components/CveExposureTable.vue`, delete the entire `imageSuffix` function (the `// Always-on …` comment block plus the function). Replace the image-name span (the `{{ img.name }}` block with the `imageSuffix` sub-span) with:
```html
              <span class="font-mono text-[12px] text-text-secondary truncate">
                {{ img.name }}
                <span v-if="img.base_os" class="text-text-muted text-[10.5px]"> · {{ img.base_os }}</span>
              </span>
```

- [ ] **Step 3: Key + filter the report by repo**

Change `imgKey` and its parameter type:
```ts
function imgKey(img: { project: string; name: string; repo: string }): string {
  return img.project + '/' + img.name + '/' + img.repo
}
```
Change `toggleReport` and `fetchReport` parameter types from `{ project: string; name: string }` to `{ project: string; name: string; repo: string }`. In `fetchReport`, filter the fetched scans to the row's repo before caching:
```ts
    const scans: CveScan[] = await res.json()
    if (seq !== reportSeq[key]) return // superseded by a newer request
    reportCache.set(key, { scans: scans.filter(s => s.repo === img.repo), fetchedAt: Date.now() })
```
(The `/api/cve/scans` endpoint has no repo parameter; filtering client-side is correct and the row already carries `repo`.) Template call sites already pass the full `img` (an `OverviewImage`, which now has `repo`), so `imgKey(img)`/`toggleReport(img)` need no change.

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors (the `{project,name,repo}` params are satisfied by `OverviewImage`), exit 0.

- [ ] **Step 5: Commit**

```bash
git add src/types/overview.ts src/components/CveExposureTable.vue
git commit -s -m "feat(overview-ui): label CVE rows by base OS, filter report per repo"
```

---

### Task 3: devel Artifacts container images fan out per repo (frontend)

**Goal:** `useArtifacts.ts` emits one `ContainerImage` per distinct target repo, with base OS + registry derived from the repo and CVE scans filtered to it.

**Files:**
- Modify: `frontend/src/composables/useArtifacts.ts` (`deriveBaseOs` signature, `baseOsFromRepo`, `containerImages` fan-out)

**Acceptance Criteria:**
- [ ] `deriveBaseOs(project, repo?)` returns the repo's base OS when known, else the project-name fallback
- [ ] A container package with targets in repos `ubi8` and `ubi9` yields two `ContainerImage`s: distinct `baseOs` ("UBI 8"/"UBI 9"), `registry` (`…/containers/ubi8/<name>` and `…/ubi9/<name>`), `id` includes the repo, and `cveScans` filtered to that repo
- [ ] A container package whose targets are a single repo (`images`, old layout) yields one image, base OS from the project fallback, registry `…/images/<name>` (unchanged)
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Rework `deriveBaseOs` (repo-first)**

In `frontend/src/composables/useArtifacts.ts`, replace `deriveBaseOs` with a repo-first version plus a helper (repo optional so any existing one-arg caller still compiles):
```ts
function baseOsFromRepo(repo?: string): string {
  switch (repo) {
    case 'ubi8': return 'UBI 8'
    case 'ubi9': return 'UBI 9'
    case 'noble': return 'Ubuntu 24.04 Noble'
    case 'bookworm': return 'Debian 12 Bookworm'
    default: return ''
  }
}

export function deriveBaseOs(project: string, repo?: string): string {
  const fromRepo = baseOsFromRepo(repo)
  if (fromRepo) return fromRepo
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    return baseOsFromRepo(suffix) || suffix
  }
  return project
}
```

- [ ] **Step 2: Fan out `containerImages` per target repo**

Replace the `containerImages` map body (the `.filter(...).map(pkg => {...})`) with a `flatMap` that expands per distinct target repo:
```ts
    return pkgs
      .filter(pkg =>
        pkg.is_container === true &&
        matchesProject(pkg.project, ver)
      )
      .flatMap(pkg => {
        const tags = pkg.container_tags ?? []
        const pullTag = tags[tags.length - 1] ?? ''
        // Distinct build repos = base images. Old-layout containers built against
        // a single "images" repo (base OS in the project); new-layout containers
        // build against ubi8/ubi9/… — one row per repo.
        const targets = pkg.targets ?? []
        const repos = [...new Set(targets.map((t: Target) => t.repo))]
        const effectiveRepos = repos.length > 0 ? repos : ['images']
        return effectiveRepos.map(repo => {
          const baseOs = deriveBaseOs(pkg.project, repo)
          const registryPath = pkg.project.toLowerCase().split(':').join('/')
          const registry = `registry.opensuse.org/${registryPath}/${repo}/${pkg.name}`
          const pullCmd = pullTag
            ? `docker pull ${registry}:${pullTag}`
            : `docker pull ${registry}`
          const published = targets.some((t: Target) => t.repo === repo && t.published === true)
          return {
            id: pkg.project + '/' + pkg.name + '/' + repo,
            project: pkg.project,
            imageName: pkg.name,
            baseOs,
            registry,
            tags,
            pullCmd,
            rollupState: pkg.rollup_state ?? '',
            published,
            cveScans: (pkg.cve_scans ?? []).filter((s: CveScan) => s.repo === repo),
          }
        })
      })
```

- [ ] **Step 3: Confirm no other one-arg `deriveBaseOs` caller broke**

Run: `grep -rn "deriveBaseOs(" frontend/src` — every existing call still compiles (repo is optional). If a caller in another file passes only the project, it keeps the old fallback behavior. No change needed unless a call now should pass a repo (none in scope).

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0. (`CveScan.repo` exists from the merged CVE-UI work; `Target.repo` exists.)

- [ ] **Step 5: Commit**

```bash
git add src/composables/useArtifacts.ts
git commit -s -m "fix(artifacts): fan out devel container images per base-image repo"
```
