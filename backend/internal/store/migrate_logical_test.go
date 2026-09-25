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
