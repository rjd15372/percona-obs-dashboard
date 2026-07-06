package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The two cache fields are in-memory only: excluded from JSON, and therefore
// from both the targets_json DB column and API responses (both use json.Marshal).
func TestCacheFieldsExcludedFromJSON(t *testing.T) {
	tgt := Target{
		Repo: "UBI_9", Arch: "x86_64", State: "blocked",
		BlockedBy:          "not installable",
		BlockedByFetchedAt: time.Now(),
	}
	b, err := json.Marshal(tgt)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"BlockedByFetchedAt", "blocked_by_fetched_at"} {
		if strings.Contains(string(b), needle) {
			t.Fatalf("Target JSON must not contain %q: %s", needle, b)
		}
	}

	pkg := Package{
		Project: "p", Name: "n",
		TargetsStable: true,
		CacheWarm:     true,
		UpdatedAt:     time.Now(),
	}
	b, err = json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TargetsStable", "targets_stable", "CacheWarm", "cache_warm"} {
		if strings.Contains(string(b), needle) {
			t.Fatalf("Package JSON must not contain %q: %s", needle, b)
		}
	}
}
