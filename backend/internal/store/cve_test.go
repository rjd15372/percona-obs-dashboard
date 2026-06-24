package store_test

import (
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func TestCveUpsertAndQuery(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	scan := model.CveScan{
		Arch:          "x86_64",
		ImageRef:      "registry.opensuse.org/test/images/mypkg:1.0",
		ScannedAt:     time.Now().UTC().Truncate(time.Second),
		CriticalCount: 2,
		HighCount:     5,
		Findings: []model.CveFinding{
			{ID: "CVE-2024-1", PkgName: "libssl3", InstalledVersion: "3.1.0", FixedVersion: "3.1.1", Severity: "CRITICAL", Title: "heap overflow"},
		},
	}

	if err := store.UpsertCveScan(db, "proj", "mypkg", scan); err != nil {
		t.Fatal(err)
	}

	scans, err := store.QueryCveScans(db, "proj", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != 1 {
		t.Fatalf("expected 1 scan, got %d", len(scans))
	}
	if scans[0].CriticalCount != 2 || scans[0].HighCount != 5 {
		t.Errorf("unexpected counts: %+v", scans[0])
	}
	if len(scans[0].Findings) != 1 || scans[0].Findings[0].ID != "CVE-2024-1" {
		t.Errorf("unexpected findings: %+v", scans[0].Findings)
	}
}

func TestCveUpsertReplaces(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s1 := model.CveScan{Arch: "x86_64", ImageRef: "reg/img:1.0", ScannedAt: time.Now().UTC(), CriticalCount: 3}
	s2 := model.CveScan{Arch: "x86_64", ImageRef: "reg/img:1.1", ScannedAt: time.Now().UTC(), CriticalCount: 0}

	_ = store.UpsertCveScan(db, "proj", "mypkg", s1)
	_ = store.UpsertCveScan(db, "proj", "mypkg", s2)

	scans, _ := store.QueryCveScans(db, "proj", "mypkg")
	if len(scans) != 1 {
		t.Fatalf("upsert should replace, got %d rows", len(scans))
	}
	if scans[0].CriticalCount != 0 {
		t.Errorf("expected replaced row, got critical_count=%d", scans[0].CriticalCount)
	}
}

func TestAttachCveScans(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_ = store.UpsertCveScan(db, "proj", "pkgA", model.CveScan{Arch: "x86_64", ImageRef: "r/i:1", ScannedAt: time.Now().UTC(), HighCount: 1})
	_ = store.UpsertCveScan(db, "proj", "pkgA", model.CveScan{Arch: "aarch64", ImageRef: "r/i:1", ScannedAt: time.Now().UTC(), CriticalCount: 2})

	pkgA := &model.Package{Project: "proj", Name: "pkgA"}
	pkgB := &model.Package{Project: "proj", Name: "pkgB"}

	if err := store.AttachCveScans(db, []*model.Package{pkgA, pkgB}); err != nil {
		t.Fatal(err)
	}
	if len(pkgA.CveScans) != 2 {
		t.Errorf("expected 2 scans for pkgA, got %d", len(pkgA.CveScans))
	}
	if len(pkgB.CveScans) != 0 {
		t.Errorf("expected 0 scans for pkgB, got %d", len(pkgB.CveScans))
	}
}
