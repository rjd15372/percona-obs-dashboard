package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

func TestLogicalProject(t *testing.T) {
	const root = "isv:percona"
	cases := []struct{ project, want string }{
		{"isv:percona:ppg:17", "isv:percona:ppg:17"},
		{"isv:percona:ppg:17:containers:ubi9", "isv:percona:ppg:17"},
		{"isv:percona:ppg:16:extras", "isv:percona:ppg:16:extras"},
		{"isv:percona:ppg:16:extras:containers:ubi9", "isv:percona:ppg:16:extras"},
		{"isv:percona:ppg:common", "isv:percona:ppg:common"},
		{"isv:percona:ppg:common:deps", "isv:percona:ppg:common"},
		{"isv:percona:common:containers:ubi8", "isv:percona:common"},
		{"isv:percona:ppg:releases:17:containers:ubi9", "isv:percona:ppg:releases"},
		{"isv:percona:PR:pr-124:ppg:16:extras", "isv:percona:PR:pr-124"},
		{"isv:percona:PR:pr-33:ppg:18:containers:ubi9", "isv:percona:PR:pr-33"},
		{"isv:other:ppg:17", ""},
		{"isv:percona:ppg", ""},
	}
	for _, c := range cases {
		if got := logicalProject(root, c.project); got != c.want {
			t.Errorf("logicalProject(%q) = %q, want %q", c.project, got, c.want)
		}
	}
}

func TestOverviewSnapshotBuilder(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }

	cur := []store.BuildingEntry{
		{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "UBI_9"},
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Repo: "images"},
		{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "Debian_12"},
		{Project: "isv:percona:PR:pr-9:ppg:18", Package: "pkg-b", Repo: "UBI_9"},
	}
	prev := []store.BuildingEntry{{Project: "isv:percona:ppg:17", Package: "pkg-a", Repo: "UBI_9"}}
	scans := []store.OverviewCveScan{
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Arch: "x86_64", Critical: 2, High: 6, CveSince: ptrTime(day(34))},
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", Arch: "aarch64", Critical: 1, High: 7, CveSince: ptrTime(day(10))},
		{Project: "isv:percona:ppg:releases:17:containers:ubi9", Package: "img-r", Arch: "x86_64", Critical: 0, High: 48, CveSince: nil},
		{Project: "isv:percona:common:containers:ubi9", Package: "img-clean", Arch: "x86_64", Critical: 0, High: 0, CveSince: nil},
	}
	periods := []store.OverviewCvePeriod{
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", CveSince: day(30), CleanSince: day(21)}, // 9d
		{Project: "isv:percona:ppg:17:containers:ubi9", Package: "img-x", CveSince: day(60), CleanSince: day(49)}, // 11d
	}

	s := buildOverviewSnapshot("isv:percona", "24h", now, cur, prev, scans, periods)

	if s.PreviousWindowRebuildTotal != 1 {
		t.Fatalf("prev total = %d", s.PreviousWindowRebuildTotal)
	}
	if s.TopRepo == nil || s.TopRepo.Name != "UBI_9" || s.TopRepo.Count != 2 {
		t.Fatalf("top_repo = %+v", s.TopRepo)
	}
	p17 := findProject(t, s, "isv:percona:ppg:17")
	if p17.Rebuilds != 3 || p17.TopPackage.Name != "pkg-a" || p17.TopPackage.Count != 2 {
		t.Fatalf("ppg:17 = %+v", p17)
	}
	if len(p17.Images) != 1 || p17.Images[0].Critical != 2 || p17.Images[0].High != 7 {
		t.Fatalf("img-x max-across-archs failed: %+v", p17.Images)
	}
	if p17.Images[0].OldestOpenDays != 34 || p17.Images[0].AvgFixDays != 10 { // mean(9,11)=10
		t.Fatalf("img-x ages = %+v", p17.Images[0])
	}
	rel := findProject(t, s, "isv:percona:ppg:releases")
	if rel.Rebuilds != 0 || rel.Images[0].OldestOpenDays != 0 || rel.Images[0].AvgFixDays != 0 {
		t.Fatalf("releases = %+v", rel)
	}
	findProject(t, s, "isv:percona:common")
	pr := findProject(t, s, "isv:percona:PR:pr-9")
	if pr.Rebuilds != 1 {
		t.Fatalf("pr = %+v", pr)
	}
	if s.Projects[0].Project != "isv:percona:ppg:17" {
		t.Fatalf("sort order: %v", s.Projects[0].Project)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func findProject(t *testing.T, s OverviewSnapshot, name string) OverviewProject {
	t.Helper()
	for _, p := range s.Projects {
		if p.Project == name {
			return p
		}
	}
	t.Fatalf("project %s missing from snapshot: %+v", name, s.Projects)
	return OverviewProject{}
}

func TestOverviewHandlerWindowValidation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "isv:percona", newOverviewCache(time.Minute))

	for _, tc := range []struct {
		q    string
		code int
	}{{"", 200}, {"?window=24h", 200}, {"?window=48h", 200}, {"?window=7d", 200}, {"?window=1h", 400}} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, "/api/overview"+tc.q, nil))
		if w.Code != tc.code {
			t.Fatalf("window %q → %d, want %d", tc.q, w.Code, tc.code)
		}
	}
}

func TestOverviewHandlerCaches(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "isv:percona", newOverviewCache(time.Minute))

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s1 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES ('isv:percona:ppg:17','p','r','x86_64','building',?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s2 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s2); err != nil {
		t.Fatal(err)
	}
	if len(s2.Projects) != len(s1.Projects) {
		t.Fatalf("second request bypassed the cache: %d vs %d projects", len(s2.Projects), len(s1.Projects))
	}
}
