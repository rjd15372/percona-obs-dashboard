package store

import (
	"testing"
	"time"
)

func TestOverviewBuildCompletions(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ins := func(project, pkg, repo, state, enteredAt string) {
		if _, err := db.Exec(`INSERT INTO target_state_durations
			(project, package, repo, arch, state, entered_at) VALUES (?,?,?,?,?,?)`,
			project, pkg, repo, "x86_64", state, enteredAt); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	fmtT := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "finished", fmtT(-1*time.Hour))  // in window: counted
	ins("isv:percona:ppg:17", "pkg-b", "UBI_9", "failed", fmtT(-2*time.Hour))    // in window: counted
	ins("isv:percona:ppg:17", "pkg-c", "UBI_9", "building", fmtT(-1*time.Hour))  // build start: must NOT count
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "finished", fmtT(-30*time.Hour)) // before window
	ins("isv:percona:ppg:17", "pkg-a", "UBI_9", "scheduled", fmtT(-1*time.Hour)) // wrong state

	got, err := QueryBuildCompletions(db, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("QueryBuildCompletions = %+v, want the 2 in-window completions", got)
	}
	pkgs := map[string]bool{}
	for _, e := range got {
		pkgs[e.Package] = true
	}
	if !pkgs["pkg-a"] || !pkgs["pkg-b"] {
		t.Fatalf("QueryBuildCompletions = %+v, want pkg-a (finished) and pkg-b (failed)", got)
	}
	prev, err := QueryBuildCompletions(db, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(prev) != 1 {
		t.Fatalf("previous window = %+v, want 1", prev)
	}
}

func TestOverviewCveQueries(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO cve_scans
		(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "x86_64", "ref", now.Format(time.RFC3339),
		2, 6, "[]", now.Add(-34*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cve_scans
		(project, package, arch, image_ref, scanned_at, critical_count, high_count, findings_json)
		VALUES (?,?,?,?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "aarch64", "ref", now.Format(time.RFC3339),
		1, 6, "[]"); err != nil { // NULL cve_since (pre-age-tracking row)
		t.Fatal(err)
	}
	scans, err := QueryAllCveScans(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(scans) != 2 {
		t.Fatalf("scans = %d, want 2", len(scans))
	}
	var withSince, withoutSince int
	for _, s := range scans {
		if s.CveSince != nil {
			withSince++
		} else {
			withoutSince++
		}
	}
	if withSince != 1 || withoutSince != 1 {
		t.Fatalf("cve_since nullability mishandled: %+v", scans)
	}

	if _, err := db.Exec(`INSERT INTO cve_periods (project, package, arch, cve_since, clean_since)
		VALUES (?,?,?,?,?)`,
		"isv:percona:ppg:17:containers:ubi9", "pdp", "x86_64",
		now.Add(-20*24*time.Hour).Format(time.RFC3339Nano), now.Add(-11*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	periods, err := QueryAllCvePeriods(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 1 || int(periods[0].CleanSince.Sub(periods[0].CveSince).Hours()/24) != 9 {
		t.Fatalf("periods = %+v, want one 9-day episode", periods)
	}
}
