package api

import "strings"

// logicalProject maps a raw OBS project to the Overview row it belongs to:
// dev version roots absorb their :containers:* subprojects; :extras is its own
// row (absorbing its subtree); the common trees, the releases tree, and each PR
// collapse to one row each. Unknown shapes return "" (excluded).
func logicalProject(root, project string) string {
	prefix := root + ":"
	if !strings.HasPrefix(project, prefix) {
		return ""
	}
	rel := strings.Split(project[len(prefix):], ":")
	switch rel[0] {
	case "PR":
		if len(rel) >= 2 {
			return root + ":PR:" + rel[1]
		}
		return ""
	case "common":
		return root + ":common"
	case "ppg":
		if len(rel) < 2 {
			return ""
		}
		switch rel[1] {
		case "common":
			return root + ":ppg:common"
		case "releases":
			return root + ":ppg:releases"
		default:
			if len(rel) >= 3 && rel[2] == "extras" {
				return root + ":ppg:" + rel[1] + ":extras"
			}
			return root + ":ppg:" + rel[1]
		}
	}
	return ""
}
