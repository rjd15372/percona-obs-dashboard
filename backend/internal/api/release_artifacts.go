package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

type ArtifactBinary struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MTime    int64  `json:"mtime"`
	BuiltAt  string `json:"built_at"`
}

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

type ReleaseTarballArtifact struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
	Arch    string `json:"arch"`
	BuiltAt string `json:"built_at"`
}

type ReleaseArtifactsResponse struct {
	Version         string                     `json:"version"`
	RefreshedAt     string                     `json:"refreshed_at"`
	Packages        []ReleasePackageArtifact   `json:"packages"`
	ContainerImages []ReleaseContainerArtifact `json:"container_images"`
	Tarballs        []ReleaseTarballArtifact   `json:"tarballs"`
}

type releaseArtifactsCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]releaseArtifactsCacheEntry
	inflight map[string]chan struct{}
}

type releaseArtifactsCacheEntry struct {
	response ReleaseArtifactsResponse
	expires  time.Time
	err      error
}

func newReleaseArtifactsCache(ttl time.Duration) *releaseArtifactsCache {
	return &releaseArtifactsCache{
		ttl:      ttl,
		entries:  map[string]releaseArtifactsCacheEntry{},
		inflight: map[string]chan struct{}{},
	}
}

func (c *releaseArtifactsCache) Get(ctx context.Context, key string, fetch func(context.Context) (ReleaseArtifactsResponse, error)) (ReleaseArtifactsResponse, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.response, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ReleaseArtifactsResponse{}, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.response, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	response, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = releaseArtifactsCacheEntry{
		response: response,
		expires:  expires,
		err:      err,
	}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return response, err
}

func releaseArtifactsHandler(db *sql.DB, obsClient *obs.Client, root string, cache *releaseArtifactsCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if obsClient == nil {
			http.Error(w, "OBS client not configured", http.StatusServiceUnavailable)
			return
		}
		version := chi.URLParam(r, "version")
		if version == "" || version == "_" {
			http.Error(w, "release version required", http.StatusBadRequest)
			return
		}

		response, err := cache.Get(r.Context(), version, func(ctx context.Context) (ReleaseArtifactsResponse, error) {
			return buildReleaseArtifacts(ctx, obsClient, root, version)
		})
		if err != nil {
			http.Error(w, "failed to fetch release artifacts: "+err.Error(), http.StatusBadGateway)
			return
		}

		// Attach CVE scans from DB after the cache — scan results update
		// frequently and must not be served stale from the OBS artifact cache.
		attachReleaseCveScans(db, response.ContainerImages)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			return
		}
	}
}

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

func buildReleaseArtifacts(ctx context.Context, client *obs.Client, root, version string) (ReleaseArtifactsResponse, error) {
	project := fmt.Sprintf("%s:ppg:releases:%s", root, version)
	binaries, err := client.ProjectBinaryList(ctx, project)
	if err != nil {
		return ReleaseArtifactsResponse{}, err
	}

	containerBinaries := collectSubprojectBinaries(ctx, client, project, "containers")
	tarballBinaries := collectSubprojectBinaries(ctx, client, project, "tarballs")

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
		Tarballs:        buildReleaseTarballArtifacts(tarballBinaries),
	}
	return response, nil
}

// subprojectCandidates lists the projects that may hold a release subproject's
// artifacts, covering BOTH layouts:
//   - flat/new:   <base>:<sub>        (artifacts built against per-flavor repos,
//                                       e.g. …:releases:18:containers on ubi8/ubi9,
//                                       …:releases:18:tarballs on ssl1.1/ssl3/…)
//   - nested/old: <base>:<sub>:<x>    (flavor baked into the project name)
// SearchProjects only returns nested sub-namespaces — its XPath appends ':' — so
// the flat project is added explicitly here (deduped), which is what the flat
// layout needs. Order: flat first, then any nested projects from the search.
func subprojectCandidates(base, sub string, searched []string) []string {
	prefix := base + ":" + sub
	out := []string{prefix}
	seen := map[string]bool{prefix: true}
	for _, p := range searched {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// collectSubprojectBinaries returns the published binaries across every candidate
// project for a release subproject. Per-project list errors are logged and
// skipped — an absent subproject (e.g. a version with no tarballs, or no flat
// project in the old layout) means "no such artifacts", not a failed response.
func collectSubprojectBinaries(ctx context.Context, client *obs.Client, base, sub string) []obs.BinaryArtifact {
	prefix := base + ":" + sub
	searched, err := client.SearchProjects(ctx, prefix)
	if err != nil {
		slog.Warn("api: release subproject search", "prefix", prefix, "err", err)
	}
	var out []obs.BinaryArtifact
	for _, p := range subprojectCandidates(base, sub, searched) {
		items, err := client.ProjectBinaryList(ctx, p)
		if err != nil {
			slog.Warn("api: release subproject binaries", "project", p, "err", err)
			continue
		}
		out = append(out, items...)
	}
	return out
}

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
			if evr, ok := versions[binary.Repo+"\x00"+binary.Arch+"\x00"+binaryBaseName(binary.Filename)]; ok {
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

func buildReleaseContainerArtifacts(ctx context.Context, client *obs.Client, binaries []obs.BinaryArtifact) []ReleaseContainerArtifact {
	byKey := map[string]*ReleaseContainerArtifact{}
	seenTags := map[string]map[string]bool{}
	for _, binary := range binaries {
		if !strings.HasSuffix(binary.Filename, ".containerinfo") {
			continue
		}
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
		if binary.MTime > artifact.MTime {
			artifact.MTime = binary.MTime
			artifact.BuiltAt = binary.BuiltAt.Format(time.RFC3339)
		}
		tags, err := client.PackageContainerTags(ctx, binary.Project, binary.Repo, binary.Arch, binary.Package, binary.Filename)
		if err == nil {
			for _, tag := range tags {
				if !seenTags[key][tag] {
					seenTags[key][tag] = true
					artifact.Tags = append(artifact.Tags, tag)
				}
			}
		}
	}

	out := make([]ReleaseContainerArtifact, 0, len(byKey))
	for _, artifact := range byKey {
		pullTag := ""
		if len(artifact.Tags) > 0 {
			pullTag = artifact.Tags[len(artifact.Tags)-1]
		}
		if pullTag != "" {
			artifact.PullCmd = "docker pull " + artifact.Registry + ":" + pullTag
		} else {
			artifact.PullCmd = "docker pull " + artifact.Registry
		}
		out = append(out, *artifact)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BaseOS != out[j].BaseOS {
			return out[i].BaseOS < out[j].BaseOS
		}
		return out[i].ImageName < out[j].ImageName
	})
	return out
}

// buildReleaseTarballArtifacts groups tarball binaries into per-(name,repo,arch)
// artifacts. A tarball is a .tar.gz binary in a :tarballs subproject built
// against an OpenSSL-versioned repo (ssl1.1 / ssl3 / ssl3.5 / …); binaries built
// against other repos (e.g. RockyLinux_*) or of other types are skipped. Version
// is parsed from the published filename
// (percona-postgresql-<version>-<repo>-linux-<arch>.tar.gz).
func buildReleaseTarballArtifacts(binaries []obs.BinaryArtifact) []ReleaseTarballArtifact {
	byKey := map[string]*ReleaseTarballArtifact{}
	latestMTime := map[string]int64{}
	for _, binary := range binaries {
		if !isTarballRepo(binary.Repo) || !strings.HasSuffix(binary.Filename, ".tar.gz") {
			continue
		}
		key := binary.Project + "\x00" + binary.Package + "\x00" + binary.Repo + "\x00" + binary.Arch
		artifact := byKey[key]
		if artifact == nil {
			artifact = &ReleaseTarballArtifact{
				Project: binary.Project,
				Name:    binary.Package,
				Repo:    binary.Repo,
				Arch:    binary.Arch,
			}
			byKey[key] = artifact
		}
		if artifact.Version == "" {
			artifact.Version = tarballFileVersion(binary.Filename, binary.Repo, binary.Arch)
		}
		if binary.MTime > latestMTime[key] {
			latestMTime[key] = binary.MTime
			artifact.BuiltAt = binary.BuiltAt.Format(time.RFC3339)
		}
	}

	out := make([]ReleaseTarballArtifact, 0, len(byKey))
	for _, artifact := range byKey {
		out = append(out, *artifact)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Arch < out[j].Arch
	})
	return out
}

// isTarballRepo reports whether an OBS build repo is an OpenSSL-versioned
// tarball repo (ssl1.1 / ssl3 / ssl3.5 / …).
func isTarballRepo(repo string) bool {
	return strings.HasPrefix(strings.ToLower(repo), "ssl")
}

// tarballFileVersion extracts the upstream version from a published tarball
// filename of the form <name>-<version>-<repo>-linux-<arch>.tar.gz. The repo and
// arch are known, so the version is the last '-' segment of the head remaining
// after the fixed suffix is stripped (e.g.
// "percona-postgresql-18.6-ssl3-linux-x86_64.tar.gz" -> "18.6"). Returns "" when
// the filename does not match the expected shape.
func tarballFileVersion(filename, repo, arch string) string {
	head := strings.TrimSuffix(filename, "-"+repo+"-linux-"+arch+".tar.gz")
	if head == filename {
		return ""
	}
	if i := strings.LastIndex(head, "-"); i >= 0 {
		return head[i+1:]
	}
	return ""
}

func releaseBinary(binary obs.BinaryArtifact) ArtifactBinary {
	out := ArtifactBinary{
		Filename: binary.Filename,
		Size:     binary.Size,
		MTime:    binary.MTime,
	}
	if binary.MTime > 0 {
		out.BuiltAt = binary.BuiltAt.Format(time.RFC3339)
	}
	return out
}

// binaryBaseName derives the base binary name (package name + extension) from a
// full versioned filename, to match the names returned by OBS binaryversions API.
// RPM: "postgresql16-devel-16.4-2.3.x86_64.rpm" → "postgresql16-devel.rpm"
// DEB: "postgresql-16_16.4-2ubuntu1_amd64.deb"  → "postgresql-16.deb"
func binaryBaseName(filename string) string {
	if strings.HasSuffix(filename, ".rpm") && !strings.HasSuffix(filename, ".src.rpm") {
		base := filename[:len(filename)-4] // strip .rpm
		if lastDot := strings.LastIndex(base, "."); lastDot >= 0 {
			base = base[:lastDot] // strip .arch
		}
		parts := strings.Split(base, "-")
		if len(parts) >= 3 {
			return strings.Join(parts[:len(parts)-2], "-") + ".rpm"
		}
	}
	if strings.HasSuffix(filename, ".deb") {
		if i := strings.Index(filename, "_"); i >= 0 {
			return filename[:i] + ".deb"
		}
	}
	return filename
}

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
