package obs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StatusError is a non-2xx OBS HTTP response.
type StatusError struct {
	Path   string
	Code   int
	Status string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("OBS %s: %s — %s", e.Path, e.Status, e.Body)
}

// IsNotFound reports whether err is an OBS 404 — for a Fleet, "this
// instance does not host that project/package".
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// InstanceInfo is the static description of one OBS instance.
type InstanceInfo struct {
	Name        string
	Slug        string
	Root        string
	WebURL      string
	DownloadURL string
	Registry    string

	MQURL           string
	MQExchange      string
	MQRoutingPrefix string

	// Identity makes logical names equal instance names (root kept). Used by
	// tests and by the pre-migration single-instance wiring; ToLogical still
	// rejects projects outside Root.
	Identity bool
}

const (
	healthFailureThreshold = 3
	mqDownGrace            = 2 * time.Minute
)

// Health is an instance's reachability as shown in the UI.
type Health struct {
	OK                  bool       `json:"ok"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	MQConnected         bool       `json:"mq_connected"`
}

type healthState struct {
	mu          sync.Mutex
	now         func() time.Time
	lastSuccess time.Time
	lastErrorAt time.Time
	lastError   string
	consecutive int
	mqConnected bool
	mqDownSince time.Time
}

// Instance is one OBS instance: its client, name translation, URL builders
// and health.
type Instance struct {
	InstanceInfo
	Client *Client
	health healthState
}

func NewInstance(info InstanceInfo, client *Client) *Instance {
	in := &Instance{InstanceInfo: info, Client: client}
	in.health.now = time.Now
	in.health.mqDownSince = time.Now()
	return in
}

// LegacyInstance is the openSUSE instance in identity mode (logical names
// keep the root).
func LegacyInstance(c *Client, root string) *Instance {
	return NewInstance(InstanceInfo{
		Name:            "openSUSE",
		Slug:            "opensuse",
		Root:            root,
		WebURL:          "https://build.opensuse.org",
		DownloadURL:     "https://download.opensuse.org/repositories",
		Registry:        "registry.opensuse.org",
		MQExchange:      "pubsub",
		MQRoutingPrefix: "opensuse.obs",
		Identity:        true,
	}, c)
}

// ToLogical maps an instance project name to its logical name. ok is false
// for projects outside this instance's root.
func (i *Instance) ToLogical(project string) (string, bool) {
	prefix := i.Root + ":"
	if !strings.HasPrefix(project, prefix) || len(project) == len(prefix) {
		return "", false
	}
	if i.Identity {
		return project, true
	}
	return project[len(prefix):], true
}

// ToInstance maps a logical project name to this instance's project name.
func (i *Instance) ToInstance(logical string) string {
	if i.Identity || i.Root == "" {
		return logical
	}
	return i.Root + ":" + logical
}

// PathRoot is the prefix a client must add to a logical name to get the
// instance project name ("" for identity instances).
func (i *Instance) PathRoot() string {
	if i.Identity {
		return ""
	}
	return i.Root
}

func (i *Instance) ProjectURL(project string) string {
	return i.WebURL + "/project/show/" + i.ToInstance(project)
}

func (i *Instance) PackageURL(project, pkg string) string {
	return i.WebURL + "/package/show/" + i.ToInstance(project) + "/" + pkg
}

func (i *Instance) LiveLogURL(project, pkg, repo, arch string) string {
	return i.WebURL + "/package/live_build_log/" + i.ToInstance(project) + "/" + pkg + "/" + repo + "/" + arch
}

func (i *Instance) DownloadURL(project, repo string) string {
	return i.InstanceInfo.DownloadURL + "/" + strings.ReplaceAll(i.ToInstance(project), ":", ":/") + "/" + repo + "/"
}

func (i *Instance) ImageBase(project, repo, name string) string {
	return i.Registry + "/" + strings.ToLower(strings.ReplaceAll(i.ToInstance(project), ":", "/")) + "/" + repo + "/" + name
}

// observe records the outcome of one API call for health tracking. 404s
// ("not hosted here") and caller cancellations say nothing about health.
func (i *Instance) observe(err error) {
	if err != nil && (IsNotFound(err) || errors.Is(err, context.Canceled)) {
		return
	}
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.lastSuccess = h.now()
		h.consecutive = 0
		return
	}
	h.consecutive++
	h.lastError = err.Error()
	h.lastErrorAt = h.now()
}

// SetMQConnected records the instance's AMQP connection state.
func (i *Instance) SetMQConnected(connected bool) {
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mqConnected && !connected {
		h.mqDownSince = h.now()
	}
	h.mqConnected = connected
}

// Health returns the current health snapshot.
func (i *Instance) Health() Health {
	h := &i.health
	h.mu.Lock()
	defer h.mu.Unlock()
	out := Health{
		ConsecutiveFailures: h.consecutive,
		LastError:           h.lastError,
		MQConnected:         h.mqConnected,
	}
	if !h.lastSuccess.IsZero() {
		t := h.lastSuccess
		out.LastSuccess = &t
	}
	if !h.lastErrorAt.IsZero() {
		t := h.lastErrorAt
		out.LastErrorAt = &t
	}
	mqOK := h.mqConnected || h.now().Sub(h.mqDownSince) < mqDownGrace
	out.OK = h.consecutive < healthFailureThreshold && mqOK
	return out
}
