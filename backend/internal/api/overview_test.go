package api

import "testing"

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
