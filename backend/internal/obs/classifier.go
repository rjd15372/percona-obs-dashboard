package obs

import (
	"strings"

	"github.com/percona/obs-dashboard/internal/model"
)

// ProjectKind categorises an OBS project relative to the configured root.
type ProjectKind int

const (
	KindUnknown   ProjectKind = iota
	// KindDev covers <root>:ppg:<version>[:<subproject>]. Container subprojects
	// (e.g. <root>:ppg:17:containers:ubi9) intentionally map here, not a separate
	// KindContainer. Container detection is per-package via is_container, not at the
	// project level — this was an explicit design decision. Events from container
	// subprojects therefore use scope ScopeVersion, not ScopeContainer.
	KindDev       // <root>:ppg:<version>[:<subproject>]
	KindPR        // <root>:PR:pr-<n>:ppg:<version>[:<subproject>]
	KindPPGCommon // <root>:ppg:common[:<subproject>]
	KindCommon    // <root>:common[:<subproject>]
	KindRelease   // <root>:ppg:releases:<version>[:<subproject>]
)

func (k ProjectKind) IsRealTime() bool {
	switch k {
	case KindDev, KindPR, KindPPGCommon, KindCommon:
		return true
	}
	return false
}

// EventScope returns the model.Scope to use for SSE events from this project kind.
func (k ProjectKind) EventScope() model.Scope {
	switch k {
	case KindDev:
		return model.ScopeVersion
	case KindPR:
		return model.ScopePR
	case KindPPGCommon:
		return model.ScopePPGCommon
	case KindCommon:
		return model.ScopeCommon
	case KindRelease:
		return model.ScopeRelease
	default:
		// KindUnknown returns ScopeCommon as a safe fallback. Callers that need to
		// filter unknowns should check kind == KindUnknown before calling EventScope.
		return model.ScopeCommon
	}
}

// Classify returns the ProjectKind for project relative to root.
// root is the top-level namespace, e.g. "isv:percona".
func Classify(root, project string) ProjectKind {
	prefix := root + ":"
	if !strings.HasPrefix(project, prefix) {
		return KindUnknown
	}
	rel := project[len(prefix):]
	parts := strings.Split(rel, ":")
	switch parts[0] {
	case "ppg":
		if len(parts) < 2 {
			return KindUnknown
		}
		switch parts[1] {
		case "common":
			return KindPPGCommon
		case "releases":
			if len(parts) >= 3 {
				return KindRelease
			}
			return KindUnknown
		default:
			return KindDev
		}
	case "ppgcommon":
		// Legacy flat-form project name for PPG common packages.
		return KindPPGCommon
	case "PR":
		return KindPR
	case "common":
		return KindCommon
	}
	return KindUnknown
}

// ProjectTags returns the tag slice to store on packages belonging to project.
func ProjectTags(root, project string) []string {
	switch Classify(root, project) {
	case KindDev:
		return []string{"ppg"}
	case KindPR:
		return []string{"ppg", "pr"}
	case KindPPGCommon:
		return []string{"ppg", "common"}
	case KindCommon:
		return []string{"common"}
	case KindRelease:
		return []string{"ppg", "release"}
	default:
		return []string{}
	}
}
