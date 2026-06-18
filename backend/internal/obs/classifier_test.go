package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

const root = "isv:percona"

func TestClassify(t *testing.T) {
	cases := []struct {
		project string
		want    ProjectKind
	}{
		{"isv:percona:ppg:17", KindDev},
		{"isv:percona:ppg:17:containers:ubi9", KindDev},
		{"isv:percona:ppg:releases:17", KindRelease},
		{"isv:percona:ppg:releases:17:containers:ubi9", KindRelease},
		{"isv:percona:PR:pr-42:ppg:17", KindPR},
		{"isv:percona:PR:pr-42:ppg:17:containers:ubi9", KindPR},
		{"isv:percona:ppg:common", KindPPGCommon},
		{"isv:percona:ppg:common:deps", KindPPGCommon},
		{"isv:percona:common", KindCommon},
		{"isv:percona:common:containers:ubi9", KindCommon},
		{"isv:other:project", KindUnknown},
		{"isv:percona", KindUnknown},
	}
	for _, c := range cases {
		if got := Classify(root, c.project); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.project, got, c.want)
		}
	}
}

func TestIsRealTime(t *testing.T) {
	if !KindDev.IsRealTime() {
		t.Error("KindDev.IsRealTime() should be true")
	}
	if !KindPR.IsRealTime() {
		t.Error("KindPR.IsRealTime() should be true")
	}
	if !KindPPGCommon.IsRealTime() {
		t.Error("KindPPGCommon.IsRealTime() should be true")
	}
	if !KindCommon.IsRealTime() {
		t.Error("KindCommon.IsRealTime() should be true")
	}
	if KindRelease.IsRealTime() {
		t.Error("KindRelease.IsRealTime() should be false")
	}
	if KindUnknown.IsRealTime() {
		t.Error("KindUnknown.IsRealTime() should be false")
	}
}

func TestEventScope(t *testing.T) {
	cases := []struct {
		kind ProjectKind
		want model.Scope
	}{
		{KindDev, model.ScopeVersion},
		{KindPR, model.ScopePR},
		{KindPPGCommon, model.ScopePPGCommon},
		{KindCommon, model.ScopeCommon},
		{KindRelease, model.ScopeRelease},
		{KindUnknown, model.ScopeCommon},
	}
	for _, c := range cases {
		if got := c.kind.EventScope(); got != c.want {
			t.Errorf("%v.EventScope() = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestProjectTags(t *testing.T) {
	cases := []struct {
		project string
		want    []string
	}{
		{"isv:percona:ppg:17", []string{"ppg"}},
		{"isv:percona:ppg:17:containers:ubi9", []string{"ppg"}},
		{"isv:percona:ppg:releases:17", []string{"ppg", "release"}},
		{"isv:percona:PR:pr-42:ppg:17", []string{"ppg", "pr"}},
		{"isv:percona:ppg:common", []string{"ppg", "common"}},
		{"isv:percona:common", []string{"common"}},
		{"isv:other", []string{}},
	}
	for _, c := range cases {
		got := ProjectTags(root, c.project)
		if len(got) != len(c.want) {
			t.Errorf("ProjectTags(%q) = %v, want %v", c.project, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ProjectTags(%q)[%d] = %q, want %q", c.project, i, got[i], c.want[i])
			}
		}
	}
}
