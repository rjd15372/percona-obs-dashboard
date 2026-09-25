package obs

import (
	"strings"
)

// ProjectKind categorises a logical (root-free) OBS project name.
type ProjectKind int

const (
	KindUnknown ProjectKind = iota
	// KindDev covers ppg:<version>[:<subproject>]. Container subprojects
	// (e.g. ppg:17:containers:ubi9) intentionally map here, not a separate
	// KindContainer. Container detection is per-package via is_container, not at the
	// project level — this was an explicit design decision. Events from container
	// subprojects therefore use the ppg tag, not the container tag.
	KindDev       // ppg:<version>[:<subproject>]
	KindPR        // PR:pr-<n>:ppg:<version>[:<subproject>]
	KindPPGCommon // ppg:common[:<subproject>]
	KindCommon    // common[:<subproject>]
	KindRelease   // ppg:releases:<version>[:<subproject>]
)

func (k ProjectKind) IsRealTime() bool {
	switch k {
	case KindDev, KindPR, KindPPGCommon, KindCommon:
		return true
	}
	return false
}

// Classify returns the ProjectKind of a logical (root-free) project name,
// e.g. "ppg:17", "PR:pr-42:ppg:17", "ppg:releases:17", "common".
func Classify(project string) ProjectKind {
	parts := strings.Split(project, ":")
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
func ProjectTags(project string) []string {
	switch Classify(project) {
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
