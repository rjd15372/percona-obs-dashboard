package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateCveRepoColumnBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Old schema: no repo column, PK (project, package, arch).
	_, err = db.Exec(`
		CREATE TABLE cve_scans (
			project TEXT NOT NULL, package TEXT NOT NULL, arch TEXT NOT NULL,
			image_ref TEXT NOT NULL, scanned_at DATETIME NOT NULL,
			critical_count INTEGER NOT NULL DEFAULT 0, high_count INTEGER NOT NULL DEFAULT 0,
			findings_json TEXT NOT NULL DEFAULT '[]', cve_since DATETIME, clean_since DATETIME,
			PRIMARY KEY (project, package, arch));
		CREATE TABLE cve_periods (
			project TEXT NOT NULL, package TEXT NOT NULL, arch TEXT NOT NULL,
			cve_since DATETIME NOT NULL, clean_since DATETIME NOT NULL,
			PRIMARY KEY (project, package, arch, cve_since));
		INSERT INTO cve_scans (project,package,arch,image_ref,scanned_at) VALUES ('p','k','x86_64','ref','2026-07-24T00:00:00Z');
		INSERT INTO cve_periods (project,package,arch,cve_since,clean_since) VALUES ('p','k','x86_64','2026-07-01T00:00:00Z','2026-07-10T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}

	if columnExists(db, "cve_scans", "repo") {
		t.Fatal("precondition: repo column should be absent")
	}
	if err := migrateCveRepoColumn(db); err != nil {
		t.Fatal(err)
	}
	if !columnExists(db, "cve_scans", "repo") || !columnExists(db, "cve_periods", "repo") {
		t.Fatal("repo column missing after migration")
	}

	var repo string
	if err := db.QueryRow(`SELECT repo FROM cve_scans WHERE project='p' AND package='k'`).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	if repo != "images" {
		t.Fatalf("backfill repo = %q, want images", repo)
	}
	var pRepo string
	if err := db.QueryRow(`SELECT repo FROM cve_periods WHERE project='p' AND package='k'`).Scan(&pRepo); err != nil {
		t.Fatal(err)
	}
	if pRepo != "images" {
		t.Fatalf("period backfill repo = %q, want images", pRepo)
	}

	// New PK includes repo: a duplicate (p,k,images,x86_64) row is rejected.
	_, err = db.Exec(`INSERT INTO cve_scans (project,package,repo,arch,image_ref,scanned_at) VALUES ('p','k','images','x86_64','ref2','2026-07-24T01:00:00Z')`)
	if err == nil {
		t.Fatal("expected PK conflict on duplicate (project,package,repo,arch)")
	}
}
