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

	if err := store.UpsertCveScan(db, "proj", "mypkg", s1); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertCveScan(db, "proj", "mypkg", s2); err != nil {
		t.Fatal(err)
	}

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

func TestUpsertCveScanStateMachine(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	withVulns := func(arch string) model.CveScan {
		return model.CveScan{Arch: arch, ImageRef: "r/img:1.0", ScannedAt: now, CriticalCount: 1}
	}
	clean := func(arch string) model.CveScan {
		return model.CveScan{Arch: arch, ImageRef: "r/img:1.0", ScannedAt: now}
	}

	t.Run("no_row+vulns sets cve_since", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		scans, _ := store.QueryCveScans(db, "p", "pkg")
		if scans[0].CveSince == nil {
			t.Fatal("expected cve_since to be set")
		}
		if scans[0].CleanSince != nil {
			t.Fatal("expected clean_since to be nil")
		}
	})

	t.Run("no_row+clean sets clean_since", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))
		scans, _ := store.QueryCveScans(db, "p", "pkg")
		if scans[0].CleanSince == nil {
			t.Fatal("expected clean_since to be set")
		}
		if scans[0].CveSince != nil {
			t.Fatal("expected cve_since to be nil")
		}
	})

	t.Run("cve_since+vulns carries cve_since", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		scans1, _ := store.QueryCveScans(db, "p", "pkg")
		original := *scans1[0].CveSince

		time.Sleep(10 * time.Millisecond)
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		scans2, _ := store.QueryCveScans(db, "p", "pkg")
		if scans2[0].CveSince == nil || !scans2[0].CveSince.Equal(original) {
			t.Fatalf("expected cve_since %v to be carried, got %v", original, scans2[0].CveSince)
		}
	})

	t.Run("cve_since+clean flips to clean_since and inserts cve_periods", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))

		scans, _ := store.QueryCveScans(db, "p", "pkg")
		if scans[0].CveSince != nil {
			t.Fatal("expected cve_since to be nil after clean")
		}
		if scans[0].CleanSince == nil {
			t.Fatal("expected clean_since to be set")
		}

		periods, _ := store.QueryCvePeriods(db, "p", "pkg")
		if len(periods) != 1 {
			t.Fatalf("expected 1 cve_period, got %d", len(periods))
		}
		if periods[0].Arch != "x86_64" {
			t.Errorf("unexpected arch %q", periods[0].Arch)
		}
	})

	t.Run("clean_since+vulns flips to cve_since", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		scans, _ := store.QueryCveScans(db, "p", "pkg")
		if scans[0].CveSince == nil {
			t.Fatal("expected cve_since to be set")
		}
		if scans[0].CleanSince != nil {
			t.Fatal("expected clean_since to be nil")
		}
	})

	t.Run("clean_since+clean carries clean_since", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))
		scans1, _ := store.QueryCveScans(db, "p", "pkg")
		original := *scans1[0].CleanSince

		time.Sleep(10 * time.Millisecond)
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))
		scans2, _ := store.QueryCveScans(db, "p", "pkg")
		if scans2[0].CleanSince == nil || !scans2[0].CleanSince.Equal(original) {
			t.Fatalf("expected clean_since %v to be carried, got %v", original, scans2[0].CleanSince)
		}
	})

	t.Run("QueryCvePeriods returns newest first", func(t *testing.T) {
		db, _ := store.Open(":memory:")
		defer db.Close()
		// Episode 1: vuln → clean
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))
		time.Sleep(time.Millisecond)
		// Episode 2: vuln → clean
		_ = store.UpsertCveScan(db, "p", "pkg", withVulns("x86_64"))
		_ = store.UpsertCveScan(db, "p", "pkg", clean("x86_64"))

		periods, _ := store.QueryCvePeriods(db, "p", "pkg")
		if len(periods) != 2 {
			t.Fatalf("expected 2 periods, got %d", len(periods))
		}
		if !periods[0].CveSince.After(periods[1].CveSince) {
			t.Errorf("periods not ordered newest-first: %v >= %v", periods[0].CveSince, periods[1].CveSince)
		}
	})
}

func TestOpenSchemaHasCvePeriods(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// cve_periods table exists
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cve_periods'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("cve_periods table missing")
	}
	// cve_scans has cve_since
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cve_scans') WHERE name='cve_since'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("cve_scans.cve_since column missing")
	}
	// cve_scans has clean_since
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('cve_scans') WHERE name='clean_since'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("cve_scans.clean_since column missing")
	}
}
