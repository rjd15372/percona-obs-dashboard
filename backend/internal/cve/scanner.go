package cve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

const obsBase = "https://build.opensuse.org"

var obsToDockerPlatform = map[string]string{
	"x86_64":  "amd64",
	"aarch64": "arm64",
	"i586":    "386",
	"armv7l":  "arm/v7",
	"ppc64le": "ppc64le",
	"s390x":   "s390x",
}

// ScanRequest carries the parameters for a single container image CVE scan.
type ScanRequest struct {
	Project    string
	Package    string
	Tags       []string
	ImageBase  string
	PrimaryTag string
	Targets    []model.Target
}

// ExecFn is the signature for running an external command and capturing output.
type ExecFn func(ctx context.Context, name string, args ...string) ([]byte, error)

// Scanner queues and executes CVE scans via trivy.
type Scanner struct {
	queue     chan ScanRequest
	db        *sql.DB
	hub       *hubpkg.Hub
	workers   int
	execFn    ExecFn
	enqueueFn func(ScanRequest)
}

// Option is a functional option for Scanner.
type Option func(*Scanner)

// WithExecFn replaces the default os/exec runner (useful in tests).
func WithExecFn(fn ExecFn) Option { return func(s *Scanner) { s.execFn = fn } }

// WithEnqueueFn replaces the internal queue send (useful in tests).
func WithEnqueueFn(fn func(ScanRequest)) Option { return func(s *Scanner) { s.enqueueFn = fn } }

// NewScanner creates a Scanner with the given options.
func NewScanner(db *sql.DB, h *hubpkg.Hub, workers int, opts ...Option) *Scanner {
	s := &Scanner{
		queue:   make(chan ScanRequest, 100),
		db:      db,
		hub:     h,
		workers: workers,
		execFn:  defaultExec,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Start launches worker goroutines. Call once; they run until ctx is cancelled.
func (s *Scanner) Start(ctx context.Context) {
	for i := 0; i < s.workers; i++ {
		go s.run(ctx)
	}
}

// Enqueue adds a scan request to the queue. Non-blocking: drops and logs a
// warning if the queue is full.
func (s *Scanner) Enqueue(req ScanRequest) {
	if s.enqueueFn != nil {
		s.enqueueFn(req)
		return
	}
	select {
	case s.queue <- req:
	default:
		slog.Warn("cve: scan queue full, dropping", "pkg", req.Project+"/"+req.Package)
	}
}

func (s *Scanner) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-s.queue:
			if !ok {
				return
			}
			s.scanPackage(ctx, req)
		}
	}
}

func (s *Scanner) scanPackage(ctx context.Context, req ScanRequest) {
	if req.PrimaryTag == "" || len(req.Targets) == 0 {
		return
	}
	imageRef := req.ImageBase + ":" + req.PrimaryTag

	for _, target := range req.Targets {
		if ctx.Err() != nil {
			return
		}
		platform, ok := obsToDockerPlatform[target.Arch]
		if !ok {
			slog.Warn("cve: unknown OBS arch, skipping", "arch", target.Arch)
			continue
		}

		obsURL := fmt.Sprintf("%s/package/show/%s/%s", obsBase, req.Project, req.Package)
		s.appendEvent(&model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventCVEScanStarted,
			Tags:    req.Tags,
			Project: req.Project,
			Package: req.Package,
			Repo:    target.Repo,
			Arch:    target.Arch,
			What:    "CVE scan started",
			Why:     "",
			Version: req.PrimaryTag,
			URL:     obsURL,
			At:      time.Now().UTC(),
		})

		slog.Info("cve: scanning", "pkg", req.Package, "arch", target.Arch, "image", imageRef)
		scan, err := s.runTrivy(ctx, imageRef, platform, target.Arch)
		if err != nil {
			slog.Warn("cve: trivy failed", "pkg", req.Package, "arch", target.Arch, "err", err)
			s.appendEvent(&model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventCVEScanFailed,
				Tags:    req.Tags,
				Project: req.Project,
				Package: req.Package,
				Repo:    target.Repo,
				Arch:    target.Arch,
				What:    "CVE scan failed",
				Why:     err.Error(),
				Version: req.PrimaryTag,
				URL:     obsURL,
				At:      time.Now().UTC(),
			})
			continue
		}

		if err := store.UpsertCveScan(s.db, req.Project, req.Package, scan); err != nil {
			slog.Error("cve: upsert scan", "pkg", req.Package, "err", err)
			continue
		}

		why := "No CVEs found"
		if scan.CriticalCount > 0 || scan.HighCount > 0 {
			why = fmt.Sprintf("CRITICAL: %d, HIGH: %d", scan.CriticalCount, scan.HighCount)
		}
		slog.Info("cve: scan complete", "pkg", req.Package, "arch", target.Arch, "critical", scan.CriticalCount, "high", scan.HighCount)
		s.appendEvent(&model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventCVEScanFinished,
			Tags:    req.Tags,
			Project: req.Project,
			Package: req.Package,
			Repo:    target.Repo,
			Arch:    target.Arch,
			What:    "CVE scan finished",
			Why:     why,
			Version: req.PrimaryTag,
			URL:     obsURL,
			At:      time.Now().UTC(),
		})
	}

	pkg, err := store.GetPackage(s.db, req.Project, req.Package)
	if err != nil || pkg == nil {
		return
	}
	_ = store.AttachCveScans(s.db, []*model.Package{pkg})
	s.hub.Notify(hubpkg.PackageUpdate(pkg))
}

func (s *Scanner) runTrivy(ctx context.Context, imageRef, platform, arch string) (model.CveScan, error) {
	out, err := s.execFn(ctx, "trivy",
		"image",
		"--platform", "linux/"+platform,
		"--severity", "HIGH,CRITICAL",
		"--ignore-unfixed",
		"-f", "json",
		"-q",
		imageRef,
	)
	if err != nil {
		// Exit code 2 means trivy found vulnerabilities — treat as success.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return parseTrivyOutput(out, imageRef, arch)
		}
		// Include stderr in the error so failures show the actual trivy message.
		stderr := ""
		if exitErr != nil && len(exitErr.Stderr) > 0 {
			stderr = ": " + strings.TrimSpace(string(exitErr.Stderr))
		}
		return model.CveScan{}, fmt.Errorf("trivy: %w%s", err, stderr)
	}
	return parseTrivyOutput(out, imageRef, arch)
}

type trivyOutput struct {
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

func parseTrivyOutput(data []byte, imageRef, arch string) (model.CveScan, error) {
	var raw trivyOutput
	if err := json.Unmarshal(data, &raw); err != nil {
		return model.CveScan{}, fmt.Errorf("parse trivy JSON: %w", err)
	}
	scan := model.CveScan{
		Arch:      arch,
		ImageRef:  imageRef,
		ScannedAt: time.Now().UTC(),
	}
	for _, result := range raw.Results {
		for _, v := range result.Vulnerabilities {
			f := model.CveFinding{
				ID:               v.VulnerabilityID,
				PkgName:          v.PkgName,
				InstalledVersion: v.InstalledVersion,
				FixedVersion:     v.FixedVersion,
				Severity:         v.Severity,
				Title:            v.Title,
			}
			scan.Findings = append(scan.Findings, f)
			switch v.Severity {
			case "CRITICAL":
				scan.CriticalCount++
			case "HIGH":
				scan.HighCount++
			}
		}
	}
	return scan, nil
}

func (s *Scanner) appendEvent(evt *model.Event) {
	if err := store.AppendEvent(s.db, evt); err != nil {
		slog.Error("cve: append event", "err", err)
		return
	}
	s.hub.Notify(hubpkg.NewEvent(evt))
}

// ImageBase constructs the OBS container registry path for a package.
// Example: "isv:percona:ppg:17" → "registry.opensuse.org/isv/percona/ppg/17/images/<name>"
func ImageBase(project, name string) string {
	return "registry.opensuse.org/" + strings.ReplaceAll(project, ":", "/") + "/images/" + name
}

// SucceededTargets filters targets to those with state "succeeded".
func SucceededTargets(targets []model.Target) []model.Target {
	var out []model.Target
	for _, t := range targets {
		if t.State == "succeeded" {
			out = append(out, t)
		}
	}
	return out
}
