package obs

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
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
}

// HistoryEntry represents one entry from /_history.
type HistoryEntry struct {
	Revision int    `xml:"rev,attr"`
	Reason   string `xml:"reason"`
}

// DepInfo represents a package dependency from /_builddepinfo.
type DepInfo struct {
	Package string   `xml:"package,attr"`
	Deps    []string `xml:"pkgdep"`
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
}

// ListSubprojects returns the names of direct children under root.
// Returns fully-qualified project names like "isv:percona:ppg".
func (c *Client) ListSubprojects(ctx context.Context, root string) ([]string, error) {
	resp, err := c.get(ctx, "/source/"+root)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var dir directoryListing
	if err := xml.NewDecoder(resp.Body).Decode(&dir); err != nil {
		return nil, fmt.Errorf("parse /source/%s: %w", root, err)
	}

	projects := make([]string, 0, len(dir.Entries))
	for _, e := range dir.Entries {
		projects = append(projects, root+":"+e.Name)
	}
	return projects, nil
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
