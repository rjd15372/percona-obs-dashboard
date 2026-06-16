package obs

import (
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

func TestInferScope(t *testing.T) {
	cases := []struct {
		project string
		want    model.Scope
	}{
		{"isv:percona:PR:pr-42:ppg17", model.ScopePR},
		{"isv:percona:ppg:releases:17:containers:ubi9", model.ScopeRelease},
		{"isv:percona:ppg:releases:17", model.ScopeRelease},
		{"isv:percona:ppg:common:deps", model.ScopePPGCommon},
		{"isv:percona:ppg:common", model.ScopePPGCommon},
		{"isv:percona:ppgcommon", model.ScopePPGCommon},
		{"isv:percona:ppg:17:containers:ubi9", model.ScopeContainer},
		{"isv:percona:ppg:17", model.ScopeVersion},
		// isv:percona:common:* subprojects are all ScopeCommon, even container ones
		{"isv:percona:common:deps:build", model.ScopeCommon},
		{"isv:percona:common:deps:runtime", model.ScopeCommon},
		{"isv:percona:common:containers:ubi8", model.ScopeCommon},
		{"isv:percona:common:containers:ubi9", model.ScopeCommon},
		{"isv:common:pg:deps", model.ScopeCommon},
	}
	for _, c := range cases {
		got := InferScope(c.project)
		if got != c.want {
			t.Errorf("InferScope(%q) = %q, want %q", c.project, got, c.want)
		}
	}
}
