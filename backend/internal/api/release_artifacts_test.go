package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
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
		"openSUSE_Tumbleweed\x00x86_64\x00etcd.rpm": "3.5.30-2.1",
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

func TestBinaryBaseName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"postgresql16-16.4-2.3.x86_64.rpm", "postgresql16.rpm"},
		{"postgresql16-devel-16.4-2.3.x86_64.rpm", "postgresql16-devel.rpm"},
		{"perl-YAML-LibYAML-0.88-1.1.noarch.rpm", "perl-YAML-LibYAML.rpm"},
		{"etcd-3.5.30-2.1.x86_64.rpm", "etcd.rpm"},
		{"etcd_3.5.30-2ubuntu1_amd64.deb", "etcd.deb"},
		{"postgresql-16_16.4-2ubuntu1_amd64.deb", "postgresql-16.deb"},
		{"something.containerinfo", "something.containerinfo"},
	}
	for _, c := range cases {
		got := binaryBaseName(c.in)
		if got != c.want {
			t.Errorf("binaryBaseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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
