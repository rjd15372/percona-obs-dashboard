package obs

import (
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		project string
		want    ProjectKind
	}{
		{"ppg:17", KindDev},
		{"ppg:17:containers:ubi9", KindDev},
		{"ppg:releases:17", KindRelease},
		{"ppg:releases:17:containers:ubi9", KindRelease},
		{"PR:pr-42:ppg:17", KindPR},
		{"PR:pr-42:ppg:17:containers:ubi9", KindPR},
		{"ppg:common", KindPPGCommon},
		{"ppg:common:deps", KindPPGCommon},
		{"ppgcommon", KindPPGCommon},
		{"common", KindCommon},
		{"common:containers:ubi9", KindCommon},
		{"isv:other:project", KindUnknown},
		{"isv:percona", KindUnknown},
		{"isv:percona:ppg:17", KindUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.project); got != c.want {
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

func TestProjectTags(t *testing.T) {
	cases := []struct {
		project string
		want    []string
	}{
		{"ppg:17", []string{"ppg"}},
		{"ppg:17:containers:ubi9", []string{"ppg"}},
		{"ppg:releases:17", []string{"ppg", "release"}},
		{"PR:pr-42:ppg:17", []string{"ppg", "pr"}},
		{"ppg:common", []string{"ppg", "common"}},
		{"ppgcommon", []string{"ppg", "common"}},
		{"common", []string{"common"}},
		{"isv:other", []string{}},
	}
	for _, c := range cases {
		got := ProjectTags(c.project)
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
