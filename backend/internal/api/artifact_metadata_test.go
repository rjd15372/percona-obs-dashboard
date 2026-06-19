package api

import (
	"context"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/obs"
)

func TestArtifactMetadataPackageFiltersDistributableAndPicksMaxMtime(t *testing.T) {
	item := ArtifactMetadataItem{
		Project: "isv:percona:ppg:17",
		Name:    "etcd",
		Repo:    "openSUSE_Leap_15.6",
		Arch:    "x86_64",
		Kind:    "package",
	}
	binaries := []obs.BinaryArtifact{
		{
			Project: "isv:percona:ppg:17", Repo: "openSUSE_Leap_15.6", Arch: "x86_64",
			Package: "etcd", Filename: "etcd-3.5.30-1.x86_64.rpm",
			MTime: 1000, BuiltAt: time.Unix(1000, 0).UTC(),
		},
		{
			Project: "isv:percona:ppg:17", Repo: "openSUSE_Leap_15.6", Arch: "x86_64",
			Package: "etcd", Filename: "etcd-debugsource-3.5.30-1.x86_64.rpm",
			MTime: 2000, BuiltAt: time.Unix(2000, 0).UTC(),
		},
	}
	result := resolveMetadataItem(item, binaries)
	if len(result.Binaries) != 1 {
		t.Fatalf("expected 1 distributable binary, got %d", len(result.Binaries))
	}
	if result.Binaries[0].Filename != "etcd-3.5.30-1.x86_64.rpm" {
		t.Errorf("unexpected filename: %s", result.Binaries[0].Filename)
	}
	want := time.Unix(1000, 0).UTC().Format(time.RFC3339)
	if result.BuiltAt != want {
		t.Errorf("BuiltAt = %q, want %q", result.BuiltAt, want)
	}
}

func TestArtifactMetadataContainerUsesContainerinfoMaxMtime(t *testing.T) {
	item := ArtifactMetadataItem{
		Project: "isv:percona:ppg:17:containers:ubi9",
		Name:    "postgresql-17",
		Kind:    "container",
	}
	binaries := []obs.BinaryArtifact{
		{
			Project: "isv:percona:ppg:17:containers:ubi9", Repo: "standard", Arch: "x86_64",
			Package: "postgresql-17", Filename: "postgresql-17.containerinfo",
			MTime: 5000, BuiltAt: time.Unix(5000, 0).UTC(),
		},
		{
			Project: "isv:percona:ppg:17:containers:ubi9", Repo: "standard", Arch: "aarch64",
			Package: "postgresql-17", Filename: "postgresql-17.containerinfo",
			MTime: 6000, BuiltAt: time.Unix(6000, 0).UTC(),
		},
	}
	result := resolveMetadataItem(item, binaries)
	if result.MTime != 6000 {
		t.Errorf("MTime = %d, want 6000 (highest mtime)", result.MTime)
	}
	want := time.Unix(6000, 0).UTC().Format(time.RFC3339)
	if result.BuiltAt != want {
		t.Errorf("BuiltAt = %q, want %q", result.BuiltAt, want)
	}
	if result.Binaries != nil {
		t.Errorf("container result must not include Binaries")
	}
}

func TestArtifactMetadataContainerNoMatchReturnsEmptyBuiltAt(t *testing.T) {
	item := ArtifactMetadataItem{
		Project: "isv:percona:ppg:17:containers:ubi9",
		Name:    "postgresql-17",
		Kind:    "container",
	}
	result := resolveMetadataItem(item, nil)
	if result.BuiltAt != "" {
		t.Errorf("expected empty BuiltAt when no binaries, got %q", result.BuiltAt)
	}
}

func TestBinaryListCacheReturnsCachedResult(t *testing.T) {
	calls := 0
	cache := newBinaryListCache(5 * time.Minute)
	fetch := func(ctx context.Context) ([]obs.BinaryArtifact, error) {
		calls++
		return []obs.BinaryArtifact{}, nil
	}
	if _, err := cache.Get(context.Background(), "proj", fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(context.Background(), "proj", fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 fetch call, got %d (cache miss on second call)", calls)
	}
}
