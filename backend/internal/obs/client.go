package obs

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is an authenticated OBS HTTP client.
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
}

func NewClient(base, username, password string) *Client {
	return &Client{
		base:     strings.TrimRight(base, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// getFile fetches a binary artifact from OBS without setting an Accept header,
// so OBS serves the raw file content (e.g. JSON containerinfo files).
func (c *Client) getFile(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

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

// --- XML response types ---

type directoryListing struct {
	Entries []struct {
		Name string `xml:"name,attr"`
	} `xml:"entry"`
}

type resultList struct {
	Results []buildResult `xml:"result"`
}

type buildResult struct {
	Project    string        `xml:"project,attr"`
	Repository string        `xml:"repository,attr"`
	Arch       string        `xml:"arch,attr"`
	State      string        `xml:"state,attr"`
	Statuses   []buildStatus `xml:"status"`
}

type buildStatus struct {
	Package string `xml:"package,attr"`
	Code    string `xml:"code,attr"`
	Versrel string `xml:"versrel,attr"`
	Details string `xml:"details"`
}

// HistoryEntry represents one entry from /_history.
type HistoryEntry struct {
	Revision int    `xml:"rev,attr"`
	Reason   string `xml:"reason"`
}

// DepInfo represents a package dependency from /_builddepinfo.
type DepInfo struct {
	Package string   `xml:"name,attr"`
	Deps    []string `xml:"pkgdep"`
	Error   string   `xml:"error"`
}

// SourceCommit represents one entry from /source/<project>/<pkg>/_history.
type SourceCommit struct {
	Rev     int    `xml:"rev,attr"`
	Comment string `xml:"comment"`
	Time    int64  `xml:"time"`
}

// PackageBuildState is a flattened build result.
type PackageBuildState struct {
	Project string
	Repo    string
	Arch    string
	Package string
	State   string
	Details string
	Versrel string // version-release string, e.g. "17.5-1"; empty if not available
}

// BinaryArtifact is one binary entry from OBS _result?view=binarylist.
type BinaryArtifact struct {
	Project  string
	Repo     string
	Arch     string
	Package  string
	Filename string
	Size     int64
	MTime    int64
	BuiltAt  time.Time
}

// BuildReasonResult represents the result of a build reason query.
type BuildReasonResult struct {
	Explain  string
	Packages []string
}

type buildReasonChangeXML struct {
	Key string `xml:"key,attr"`
}

type buildReasonXML struct {
	Explain string                 `xml:"explain"`
	Changes []buildReasonChangeXML `xml:"packagechange"`
}

// SearchProjects returns all OBS projects whose names start with the given prefix
// (exclusive of the prefix itself). Uses the OBS search API.
func (c *Client) SearchProjects(ctx context.Context, prefix string) ([]string, error) {
	// XPath: starts-with(@name,'prefix:') to catch all sub-namespaces
	path := "/search/project/id?match=starts-with(@name,'" + prefix + ":" + "')"
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var col struct {
		Projects []struct {
			Name string `xml:"name,attr"`
		} `xml:"project"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&col); err != nil {
		return nil, fmt.Errorf("parse search/project/id: %w", err)
	}

	names := make([]string, 0, len(col.Projects))
	for _, p := range col.Projects {
		names = append(names, p.Name)
	}
	return names, nil
}

// BuildResults fetches all package build states for a project.
func (c *Client) BuildResults(ctx context.Context, project string) ([]PackageBuildState, error) {
	resp, err := c.get(ctx, "/build/"+project+"/_result")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
			})
		}
	}
	return out, nil
}

// projectDir fetches a /build/… directory listing and returns the entry names.
func (c *Client) projectDir(ctx context.Context, path string) ([]string, error) {
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var dir directoryListing
	if err := xml.NewDecoder(resp.Body).Decode(&dir); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	names := make([]string, 0, len(dir.Entries))
	for _, e := range dir.Entries {
		if e.Name != "" {
			names = append(names, e.Name)
		}
	}
	return names, nil
}

// ProjectRepos returns the repository names configured for an OBS project.
// Calls GET /build/{project}/.
func (c *Client) ProjectRepos(ctx context.Context, project string) ([]string, error) {
	return c.projectDir(ctx, "/build/"+url.PathEscape(project)+"/")
}

// ProjectRepoArchs returns the architectures available for a repository.
// Calls GET /build/{project}/{repo}/.
func (c *Client) ProjectRepoArchs(ctx context.Context, project, repo string) ([]string, error) {
	return c.projectDir(ctx, "/build/"+url.PathEscape(project)+"/"+url.PathEscape(repo)+"/")
}

// ProjectRepoPackages returns the package names built in a specific repo/arch.
// Calls GET /build/{project}/{repo}/{arch}/.
func (c *Client) ProjectRepoPackages(ctx context.Context, project, repo, arch string) ([]string, error) {
	return c.projectDir(ctx, "/build/"+url.PathEscape(project)+"/"+
		url.PathEscape(repo)+"/"+url.PathEscape(arch)+"/")
}

// ProjectBuildResults fetches all build states for a project with version info
// (view=versrel). Use this for release projects where the version comes from
// OBS rather than from the DB.
func (c *Client) ProjectBuildResults(ctx context.Context, project string) ([]PackageBuildState, error) {
	path := fmt.Sprintf("/build/%s/_result?view=versrel", url.PathEscape(project))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
				Versrel: s.Versrel,
			})
		}
	}
	return out, nil
}

// BuildLog returns the tail of a package build log.
func (c *Client) BuildLog(ctx context.Context, project, repo, arch, pkg string, tailBytes int) (string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_log?last=1&nostream=1", project, repo, arch, pkg)
	resp, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(tailBytes)))
	return string(body), err
}

// PackageHistory returns build history entries for a package target.
func (c *Client) PackageHistory(ctx context.Context, project, repo, arch, pkg string) ([]HistoryEntry, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_history", project, repo, arch, pkg)
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var hist struct {
		Entries []HistoryEntry `xml:"entry"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&hist); err != nil {
		return nil, err
	}
	return hist.Entries, nil
}

// BuildDepInfo returns dependency info for a repo+arch.
func (c *Client) BuildDepInfo(ctx context.Context, project, repo, arch string) ([]DepInfo, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/_builddepinfo", project, repo, arch)
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Packages []DepInfo `xml:"package"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Packages, nil
}

// PackageBlockedReasons returns a map of "repo/arch" → blocked reason for all
// blocked targets of pkg in project, using the _result?view=status endpoint.
// Targets with no details or not in blocked state are omitted from the map.
func (c *Client) PackageBlockedReasons(ctx context.Context, project, pkg string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	reasons := make(map[string]string)
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			if s.Code == "blocked" && s.Details != "" {
				reasons[r.Repository+"/"+r.Arch] = s.Details
			}
		}
	}
	return reasons, nil
}

// SourceHistory returns commit history for a source package.
func (c *Client) SourceHistory(ctx context.Context, project, pkg string) ([]SourceCommit, error) {
	path := fmt.Sprintf("/source/%s/%s/_history", project, pkg)
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var hist struct {
		Revisions []SourceCommit `xml:"revision"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&hist); err != nil {
		return nil, err
	}
	return hist.Revisions, nil
}

// PackageBuildResults fetches build states for a specific package across all targets.
func (c *Client) PackageBuildResults(ctx context.Context, project, pkg string) ([]PackageBuildState, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
			})
		}
	}
	return out, nil
}

// RepoPublishStates returns a map of "repo/arch" → publish state by reading
// the r.State attribute from the OBS _result?package=…&view=status XML.
func (c *Client) RepoPublishStates(ctx context.Context, project, pkg string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	states := make(map[string]string, len(rl.Results))
	for _, r := range rl.Results {
		states[r.Repository+"/"+r.Arch] = r.State
	}
	return states, nil
}

// PackageBuildReason fetches the build reason for a specific package target.
func (c *Client) PackageBuildReason(ctx context.Context, project, repo, arch, pkg string) (BuildReasonResult, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_reason",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return BuildReasonResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BuildReasonResult{}, err
	}

	var raw buildReasonXML
	if err := xml.Unmarshal(body, &raw); err != nil {
		return BuildReasonResult{}, fmt.Errorf("parse build reason: %w", err)
	}

	result := BuildReasonResult{Explain: raw.Explain}
	for _, ch := range raw.Changes {
		if ch.Key != "" {
			result.Packages = append(result.Packages, ch.Key)
		}
	}
	return result, nil
}

// PackageIsContainer returns true if the package's source contains a Dockerfile
// or a .kiwi file, indicating it produces a container image.
// Uses /source/{project}/{package}?view=info which returns a <sourceinfo>
// element listing all source filenames.
func (c *Client) PackageIsContainer(ctx context.Context, project, pkg string) (bool, error) {
	path := fmt.Sprintf("/source/%s/%s?view=info",
		url.PathEscape(project), url.PathEscape(pkg))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}

	var info struct {
		Filenames []string `xml:"filename"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, fmt.Errorf("parse source info: %w", err)
	}
	for _, fn := range info.Filenames {
		if fn == "Dockerfile" || strings.HasSuffix(fn, ".kiwi") {
			return true, nil
		}
	}
	return false, nil
}

// PackageVersionResult returns the versrel string (e.g. "17.5-1") from the first
// successfully built target, or "" if the package has not been built yet.
func (c *Client) PackageVersionResult(ctx context.Context, project, pkg string) (string, error) {
	path := fmt.Sprintf("/build/%s/_result?view=versrel&package=%s",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return "", fmt.Errorf("parse versrel result: %w", err)
	}
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			if s.Versrel != "" {
				return s.Versrel, nil
			}
		}
	}
	return "", nil
}

// ProjectBinaryList returns every binary entry from _result?view=binarylist.
func (c *Client) ProjectBinaryList(ctx context.Context, project string) ([]BinaryArtifact, error) {
	path := fmt.Sprintf("/build/%s/_result?view=binarylist", url.PathEscape(project))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Results []struct {
			Project    string `xml:"project,attr"`
			Repository string `xml:"repository,attr"`
			Arch       string `xml:"arch,attr"`
			BinaryList []struct {
				Package  string `xml:"package,attr"`
				Binaries []struct {
					Filename string `xml:"filename,attr"`
					Size     int64  `xml:"size,attr"`
					MTime    int64  `xml:"mtime,attr"`
				} `xml:"binary"`
			} `xml:"binarylist"`
		} `xml:"result"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result?view=binarylist: %w", project, err)
	}

	var out []BinaryArtifact
	for _, result := range raw.Results {
		resultProject := result.Project
		if resultProject == "" {
			resultProject = project
		}
		for _, list := range result.BinaryList {
			for _, binary := range list.Binaries {
				artifact := BinaryArtifact{
					Project:  resultProject,
					Repo:     result.Repository,
					Arch:     result.Arch,
					Package:  list.Package,
					Filename: binary.Filename,
					Size:     binary.Size,
					MTime:    binary.MTime,
				}
				if binary.MTime > 0 {
					artifact.BuiltAt = time.Unix(binary.MTime, 0).UTC()
				}
				out = append(out, artifact)
			}
		}
	}
	return out, nil
}

// IsDistributableBinary returns true for binary files that users can install:
// .rpm (excluding .src.rpm and debuginfo/debugsource) and .deb (excluding dbgsym/dbg).
func IsDistributableBinary(filename string) bool {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".src.rpm"):
		return false
	case strings.HasSuffix(lower, ".rpm"):
		return !strings.Contains(lower, "-debuginfo") && !strings.Contains(lower, "-debugsource")
	case strings.HasSuffix(lower, ".deb"):
		return !strings.Contains(lower, "-dbgsym_") && !strings.Contains(lower, "-dbg_")
	default:
		return false
	}
}

// PackageBinaries returns the distributable binary filenames for a package build target.
// Source packages, debug info/symbols, and non-package artifacts are excluded.
func (c *Client) PackageBinaries(ctx context.Context, project, repo, arch, pkg string) ([]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var listing struct {
		Binaries []struct {
			Filename string `xml:"filename,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil, fmt.Errorf("parse binary list: %w", err)
	}

	var out []string
	for _, b := range listing.Binaries {
		if IsDistributableBinary(b.Filename) {
			out = append(out, b.Filename)
		}
	}
	return out, nil
}

// PackageContainerInfoFilename returns the filename of the .containerinfo binary
// artifact for the given package target, or "" if the build hasn't produced one yet.
func (c *Client) PackageContainerInfoFilename(ctx context.Context, project, repo, arch, pkg string) (string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var listing struct {
		Binaries []struct {
			Filename string `xml:"filename,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return "", fmt.Errorf("parse binary list: %w", err)
	}
	for _, b := range listing.Binaries {
		if strings.HasSuffix(b.Filename, ".containerinfo") {
			return b.Filename, nil
		}
	}
	return "", nil
}

// PackageContainerTags fetches a .containerinfo JSON file and returns all tags
// with the "imagename:" prefix stripped (everything up to and including the last
// ":"), e.g. ["18.4-1-1.7", "18.4-1"]. Returns nil if tags is empty.
func (c *Client) PackageContainerTags(ctx context.Context, project, repo, arch, pkg, filename string) ([]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg),
		url.PathEscape(filename))
	resp, err := c.getFile(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info struct {
		Tags []string `json:"tags"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse containerinfo: %w", err)
	}
	if len(info.Tags) == 0 {
		return nil, nil
	}
	tags := make([]string, 0, len(info.Tags))
	for _, raw := range info.Tags {
		tag := raw
		if idx := strings.LastIndex(raw, ":"); idx >= 0 {
			tag = raw[idx+1:]
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

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
