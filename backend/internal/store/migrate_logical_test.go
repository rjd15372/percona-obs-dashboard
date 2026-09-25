package store

import (
	"testing"
	"time"
)

func TestMigrateLogicalNames(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'failed', '[{"repo":"R","arch":"x","state":"failed"}]', ?)`, now)
	mustExec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('ppg:18', 'already', 'succeeded', '[{"repo":"R","arch":"x","state":"succeeded","instance":"percona"}]', ?)`, now)
	mustExec(`INSERT INTO events (id, type, project, package, repo, arch, what, why, url, at)
		VALUES ('e1', 'failed', 'isv:percona:ppg:17', 'pg', 'R', 'x', 'w', '', 'https://build.opensuse.org/x', ?)`, now)
	mustExec(`INSERT INTO events (id, type, project, package, what, why, url, at)
		VALUES ('e2', 'created', 'isv:percona:ppg:17', '', 'w', '', 'u', ?)`, now)
	mustExec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', 'failed', ?)`, now)
	mustExec(`INSERT INTO cve_scans (project, package, repo, arch, image_ref, scanned_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', 'ref', ?)`, now)
	mustExec(`INSERT INTO cve_periods (project, package, repo, arch, cve_since, clean_since)
		VALUES ('isv:percona:ppg:17', 'pg', 'R', 'x', ?, ?)`, now, now)

	for run := 1; run <= 2; run++ {
		if err := MigrateLogicalNames(db, "isv:percona", "opensuse"); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, table := range []string{"packages", "events", "target_state_durations", "cve_scans", "cve_periods"} {
			var n int
			db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE project LIKE 'isv:percona:%'`).Scan(&n)
			if n != 0 {
				t.Errorf("run %d: %s still has %d prefixed rows", run, table, n)
			}
		}
		var targets string
		db.QueryRow(`SELECT targets_json FROM packages WHERE project = 'ppg:17'`).Scan(&targets)
		if targets != `[{"repo":"R","arch":"x","state":"failed","instance":"opensuse"}]` {
			t.Errorf("run %d: targets_json = %s", run, targets)
		}
		db.QueryRow(`SELECT targets_json FROM packages WHERE project = 'ppg:18'`).Scan(&targets)
		if targets != `[{"repo":"R","arch":"x","state":"succeeded","instance":"percona"}]` {
			t.Errorf("run %d: stamped targets must be untouched: %s", run, targets)
		}
		var inst1, inst2 string
		db.QueryRow(`SELECT COALESCE(instance,'') FROM events WHERE id = 'e1'`).Scan(&inst1)
		db.QueryRow(`SELECT COALESCE(instance,'') FROM events WHERE id = 'e2'`).Scan(&inst2)
		if inst1 != "opensuse" || inst2 != "" {
			t.Errorf("run %d: event instances = %q, %q", run, inst1, inst2)
		}
	}
}

func TestStampMissingInstanceEmptyObject(t *testing.T) {
	out, changed, err := stampMissingInstance(`[{}]`, "opensuse")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed = true")
	}
	if out != `[{"instance":"opensuse"}]` {
		t.Errorf("out = %s, want %s", out, `[{"instance":"opensuse"}]`)
	}
}

func TestStampMissingInstanceMixedRow(t *testing.T) {
	raw := `[{"repo":"R","arch":"x","state":"failed"},{"repo":"R2","arch":"y","state":"succeeded","instance":"percona"}]`
	out, changed, err := stampMissingInstance(raw, "opensuse")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed = true")
	}
	want := `[{"repo":"R","arch":"x","state":"failed","instance":"opensuse"},{"repo":"R2","arch":"y","state":"succeeded","instance":"percona"}]`
	if out != want {
		t.Errorf("out = %s, want %s", out, want)
	}
}

// Once the migration has run, later boots must not touch rows again: in a
// multi-instance deployment new instance-less target events and unstamped
// targets are legitimate and must not get the legacy slug.
func TestMigrateLogicalNamesRunsOnce(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := MigrateLogicalNames(db, "isv:percona", "opensuse"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('isv:percona:ppg:17', 'pg', 'failed', '[{"repo":"R","arch":"x","state":"failed"}]', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events (id, type, project, package, repo, arch, what, why, url, at)
		VALUES ('e1', 'failed', 'ppg:17', 'pg', 'R', 'x', 'w', '', 'u', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLogicalNames(db, "isv:percona", "opensuse"); err != nil {
		t.Fatal(err)
	}
	var project, targets string
	db.QueryRow(`SELECT project, targets_json FROM packages WHERE name = 'pg'`).Scan(&project, &targets)
	if project != "isv:percona:ppg:17" || targets != `[{"repo":"R","arch":"x","state":"failed"}]` {
		t.Errorf("second run touched packages: project=%s targets=%s", project, targets)
	}
	var inst string
	db.QueryRow(`SELECT COALESCE(instance,'') FROM events WHERE id = 'e1'`).Scan(&inst)
	if inst != "" {
		t.Errorf("second run stamped event instance %q", inst)
	}
}

func TestMigrateLogicalNamesEmptyRoot(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range []string{"ppg:17", ":odd"} {
		if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
			VALUES (?, 'pg', 'failed', '[{"repo":"R","arch":"x","state":"failed"}]', ?)`, p, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateLogicalNames(db, "", "opensuse"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT project FROM packages ORDER BY project`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		got = append(got, p)
	}
	if len(got) != 2 || got[0] != ":odd" || got[1] != "ppg:17" {
		t.Errorf("empty root must strip nothing, projects = %v", got)
	}
	var targets string
	db.QueryRow(`SELECT targets_json FROM packages WHERE project = 'ppg:17'`).Scan(&targets)
	if targets != `[{"repo":"R","arch":"x","state":"failed","instance":"opensuse"}]` {
		t.Errorf("targets not stamped: %s", targets)
	}
}

func TestMigrateLogicalNamesSkipsMalformedTargets(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO packages (project, name, rollup_state, targets_json, updated_at)
		VALUES ('isv:percona:ppg:17', 'bad', 'failed', 'not json', ?),
		       ('isv:percona:ppg:17', 'good', 'failed', '[{"repo":"R","arch":"x","state":"failed"}]', ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLogicalNames(db, "isv:percona", "opensuse"); err != nil {
		t.Fatalf("malformed row must not fail the migration: %v", err)
	}
	var project, bad, good string
	db.QueryRow(`SELECT project, targets_json FROM packages WHERE name = 'bad'`).Scan(&project, &bad)
	if project != "ppg:17" || bad != "not json" {
		t.Errorf("malformed row: project=%s targets=%s (want stripped, targets untouched)", project, bad)
	}
	db.QueryRow(`SELECT targets_json FROM packages WHERE name = 'good'`).Scan(&good)
	if good != `[{"repo":"R","arch":"x","state":"failed","instance":"opensuse"}]` {
		t.Errorf("good row not migrated: %s", good)
	}
}
