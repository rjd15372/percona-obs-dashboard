package model

import "time"

type RollupState string

const (
	RollupFailed       RollupState = "failed"
	RollupBroken       RollupState = "broken"
	RollupUnresolvable RollupState = "unresolvable"
	RollupBlocked      RollupState = "blocked"
	RollupBuilding     RollupState = "building"
	// RollupFinished means the build worker finished but the scheduler has not yet
	// accepted the result. It is a transient state between building and succeeded/failed.
	RollupFinished  RollupState = "finished"
	RollupScheduled RollupState = "scheduled"
	RollupSucceeded RollupState = "succeeded"
	RollupPublished RollupState = "published" // terminal: all targets built and repos published
)

// Severity returns a sortable integer: higher = worse (for failure-first ordering).
func (s RollupState) Severity() int {
	switch s {
	case RollupBroken:
		return 5
	case RollupFailed:
		return 4
	case RollupUnresolvable:
		return 3
	case RollupBlocked:
		return 2
	case RollupBuilding, RollupFinished, RollupScheduled:
		return 1
	default:
		return 0
	}
}

type Target struct {
	Repo                string   `json:"repo"`
	Arch                string   `json:"arch"`
	State               string   `json:"state"`
	Details             string   `json:"details,omitempty"`
	BlockedBy           string   `json:"blocked_by,omitempty"`
	BuildReason         string   `json:"build_reason,omitempty"`
	BuildReasonPackages []string `json:"build_reason_packages,omitempty"`
	Published           bool     `json:"published,omitempty"`
}

type Trigger struct {
	What string    `json:"what"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

type Package struct {
	Project        string      `json:"project"`
	Name           string      `json:"name"`
	Tags           []string    `json:"tags,omitempty"`
	IsRelease      bool        `json:"is_release,omitempty"`
	RollupState    RollupState `json:"rollup_state"`
	OKTargets      int         `json:"ok_targets"`
	TotalTargets   int         `json:"total_targets"`
	IsContainer    *bool       `json:"is_container,omitempty"`
	Version        string      `json:"version,omitempty"`
	ContainerTags  []string    `json:"container_tags,omitempty"`
	Trigger        *Trigger    `json:"trigger,omitempty"`
	Targets        []Target    `json:"targets"`
	UpdatedAt      time.Time   `json:"updated_at"`
	StateChangedAt *time.Time  `json:"state_changed_at,omitempty"`
}

type EventType string

const (
	EventTriggered     EventType = "triggered"
	EventStarted       EventType = "started"
	EventSucceeded     EventType = "succeeded"
	EventFailed        EventType = "failed"
	EventUnresolvable  EventType = "unresolvable"
	EventBroken        EventType = "broken"
	EventBlocked       EventType = "blocked"
	EventPublished     EventType = "published"
	EventCreated       EventType = "created"
	EventDeleted       EventType = "deleted"
	EventBuildStarted  EventType = "build_started"
	EventBuildFinished EventType = "build_finished"
	EventVersionChange EventType = "version_change"
	EventUpdated       EventType = "updated"
)

type Event struct {
	ID      string    `json:"id"`
	Type    EventType `json:"type"`
	Tags    []string  `json:"tags,omitempty"`
	Project string    `json:"project"`
	Package string    `json:"package"`
	Repo    string    `json:"repo,omitempty"`
	Arch    string    `json:"arch,omitempty"`
	What    string    `json:"what"`
	Why     string    `json:"why"`
	Version string    `json:"version,omitempty"`
	URL     string    `json:"url"`
	At      time.Time `json:"at"`
}
