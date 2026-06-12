package mq

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchange             = "pubsub"
	packageRouteKey      = "opensuse.obs.package.#"
	repoRouteKey         = "opensuse.obs.repo.published"
	repoBuildStartedKey  = "opensuse.obs.repo.build_started"
	repoBuildFinishedKey = "opensuse.obs.repo.build_finished"
	projectRouteKey      = "opensuse.obs.project.#"
)

// mqMessage is the JSON structure of OBS MQ events.
// Fields are a union of all event payloads; unused fields are zero for any given event type.
type mqMessage struct {
	Project    string `json:"project"`
	Package    string `json:"package"`
	Repo       string `json:"repository"`
	Arch       string `json:"arch"`
	Reason     string `json:"reason"`
	Sender     string `json:"sender"`
	BuildID    string `json:"buildid"`
	OldVersion string `json:"oldversion"`
	NewVersion string `json:"newversion"`
	Rev        string `json:"rev"`
	User       string `json:"user"`
	Comment    string `json:"comment"`
}

// Consumer subscribes to the OBS AMQP bus and updates the store on build events.
type Consumer struct {
	url string
	db  *sql.DB
}

func NewConsumer(url string, db *sql.DB) *Consumer {
	return &Consumer{url: url, db: db}
}

// Run blocks until ctx is cancelled, reconnecting on errors with exponential back-off.
func (c *Consumer) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for ctx.Err() == nil {
		if err := c.run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("mq: disconnected, reconnecting", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			next := backoff * 2
			if next > maxBackoff {
				next = maxBackoff
			}
			backoff = next
		} else {
			backoff = time.Second
		}
	}
}

func (c *Consumer) run(ctx context.Context) error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("channel: %w", err)
	}
	defer ch.Close()

	// Passive declare — exchange already exists on rabbit.opensuse.org
	if err := ch.ExchangeDeclarePassive(exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("exchange declare: %w", err)
	}

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return fmt.Errorf("queue declare: %w", err)
	}

	for _, key := range []string{
		packageRouteKey,
		repoRouteKey,
		repoBuildStartedKey,
		repoBuildFinishedKey,
		projectRouteKey,
	} {
		if err := ch.QueueBind(q.Name, key, exchange, false, nil); err != nil {
			return fmt.Errorf("queue bind %s: %w", key, err)
		}
	}

	msgs, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	connClose := conn.NotifyClose(make(chan *amqp.Error, 1))
	slog.Info("mq: connected", "exchange", exchange)

	for {
		select {
		case <-ctx.Done():
			return nil
		case mqErr := <-connClose:
			if mqErr != nil {
				return fmt.Errorf("connection closed: %w", mqErr)
			}
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return fmt.Errorf("channel closed")
			}
			c.handle(msg)
		}
	}
}

func (c *Consumer) handle(msg amqp.Delivery) {
	var m mqMessage
	if err := json.Unmarshal(msg.Body, &m); err != nil {
		slog.Debug("mq: unparseable message", "err", err)
		return
	}

	// Filter: only process isv:percona projects
	if !strings.HasPrefix(m.Project, "isv:percona") {
		return
	}

	var payload any
	if err := json.Unmarshal(msg.Body, &payload); err != nil {
		slog.Info("mq: received raw message", "key", msg.RoutingKey, "message", string(msg.Body), "err", err)
	} else {
		slog.Info("mq: received raw message", "key", msg.RoutingKey, "payload", payload)
	}

	key := msg.RoutingKey
	switch {
	case key == repoRouteKey:
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventPublished,
			Scope:   model.ScopeRelease,
			Project: m.Project,
			Package: m.Package,
			Repo:    m.Repo,
			What:    fmt.Sprintf("%s published", m.Repo),
			Why:     "repo published",
			URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case key == repoBuildStartedKey:
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventBuildStarted,
			Scope:   inferScopeFromProject(m.Project),
			Project: m.Project,
			Repo:    m.Repo,
			Arch:    m.Arch,
			What:    fmt.Sprintf("%s/%s build started", m.Repo, m.Arch),
			Why:     m.BuildID,
			URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case key == repoBuildFinishedKey:
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventBuildFinished,
			Scope:   inferScopeFromProject(m.Project),
			Project: m.Project,
			Repo:    m.Repo,
			Arch:    m.Arch,
			What:    fmt.Sprintf("%s/%s build finished", m.Repo, m.Arch),
			Why:     m.BuildID,
			URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case key == "opensuse.obs.project.create":
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventCreated,
			Scope:   inferScopeFromProject(m.Project),
			Project: m.Project,
			What:    fmt.Sprintf("project %s created", m.Project),
			Why:     m.Sender,
			URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case key == "opensuse.obs.project.delete":
		scope := inferScopeFromProject(m.Project)
		if err := store.DeletePackagesByProject(c.db, m.Project); err != nil {
			slog.Error("mq: delete packages for project", "project", m.Project, "err", err)
		}
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventDeleted,
			Scope:   scope,
			Project: m.Project,
			What:    fmt.Sprintf("project %s deleted", m.Project),
			Why:     m.Comment,
			URL:     fmt.Sprintf("https://build.opensuse.org/project/show/%s", m.Project),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case key == "opensuse.obs.package.version_change":
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    model.EventVersionChange,
			Scope:   inferScopeFromProject(m.Project),
			Project: m.Project,
			Package: m.Package,
			What:    fmt.Sprintf("%s version %s → %s", m.Package, m.OldVersion, m.NewVersion),
			Why:     m.Comment,
			URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", m.Project, m.Package),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}

	case isPackageBuildEvent(key):
		scope := inferScopeFromProject(m.Project)

		// build_unchanged: state is unknown — only append the event, skip upsert.
		if key == "opensuse.obs.package.build_unchanged" {
			evt := &model.Event{
				ID:      "evt_" + ulid.Make().String(),
				Type:    model.EventSucceeded,
				Scope:   scope,
				Project: m.Project,
				Package: m.Package,
				Repo:    m.Repo,
				Arch:    m.Arch,
				What:    fmt.Sprintf("%s build unchanged on %s/%s", m.Package, m.Repo, m.Arch),
				Why:     m.Reason,
				URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", m.Project, m.Package),
				At:      time.Now().UTC(),
			}
			if err := store.AppendEvent(c.db, evt); err != nil {
				slog.Error("mq: append event", "err", err)
			}
			return
		}

		rollup := mqStateToRollup(key)
		evtType := rollupToEventType(rollup)

		// Read current package from store and merge the updated target into the
		// full target list so we don't overwrite other (repo, arch) entries.
		pkg := c.mergePackageTarget(m, scope, rollup)

		if err := store.UpsertPackageState(c.db, pkg); err != nil {
			slog.Error("mq: upsert package", "err", err)
			return
		}
		evt := &model.Event{
			ID:      "evt_" + ulid.Make().String(),
			Type:    evtType,
			Scope:   scope,
			Project: m.Project,
			Package: m.Package,
			Repo:    m.Repo,
			Arch:    m.Arch,
			What:    fmt.Sprintf("%s %s on %s/%s", m.Package, string(rollup), m.Repo, m.Arch),
			Why:     m.Reason,
			URL:     fmt.Sprintf("https://build.opensuse.org/package/show/%s/%s", m.Project, m.Package),
			At:      time.Now().UTC(),
		}
		if err := store.AppendEvent(c.db, evt); err != nil {
			slog.Error("mq: append event", "err", err)
		}
	}
}

// mergePackageTarget reads the existing package from the store (if any), updates
// the (repo, arch) target with the new state, then recalculates OKTargets,
// TotalTargets, and RollupState from the full merged target list.
func (c *Consumer) mergePackageTarget(m mqMessage, scope model.Scope, newState model.RollupState) *model.Package {
	targets := []model.Target{{Repo: m.Repo, Arch: m.Arch, State: string(newState)}}

	existing, err := store.QueryPackages(c.db, m.Project)
	if err != nil {
		slog.Warn("mq: could not read existing package, creating fresh", "err", err)
	} else {
		for _, p := range existing {
			if p.Name == m.Package {
				// Found existing package — merge the updated target.
				merged := make([]model.Target, 0, len(p.Targets))
				found := false
				for _, t := range p.Targets {
					if t.Repo == m.Repo && t.Arch == m.Arch {
						merged = append(merged, model.Target{Repo: m.Repo, Arch: m.Arch, State: string(newState)})
						found = true
					} else {
						merged = append(merged, t)
					}
				}
				if !found {
					merged = append(merged, model.Target{Repo: m.Repo, Arch: m.Arch, State: string(newState)})
				}
				targets = merged
				break
			}
		}
	}

	// Recalculate rollup and counts from the full target list.
	worst := model.RollupSucceeded
	okCount := 0
	for _, t := range targets {
		s := model.RollupState(t.State)
		if s.Severity() > worst.Severity() {
			worst = s
		}
		if s == model.RollupSucceeded {
			okCount++
		}
	}

	var trigger *model.Trigger
	if m.Reason != "" {
		trigger = &model.Trigger{What: m.Reason, Kind: "obs", At: time.Now().UTC()}
	}

	return &model.Package{
		Project:      m.Project,
		Name:         m.Package,
		Scope:        scope,
		RollupState:  worst,
		OKTargets:    okCount,
		TotalTargets: len(targets),
		Trigger:      trigger,
		Targets:      targets,
		UpdatedAt:    time.Now().UTC(),
	}
}

func isPackageBuildEvent(key string) bool {
	return key == "opensuse.obs.package.build_success" ||
		key == "opensuse.obs.package.build_fail" ||
		key == "opensuse.obs.package.build_unchanged"
}

func mqStateToRollup(key string) model.RollupState {
	switch key {
	case "opensuse.obs.package.build_success":
		return model.RollupSucceeded
	case "opensuse.obs.package.build_fail":
		return model.RollupFailed
	default:
		return model.RollupSucceeded
	}
}

func rollupToEventType(state model.RollupState) model.EventType {
	switch state {
	case model.RollupSucceeded:
		return model.EventSucceeded
	case model.RollupFailed:
		return model.EventFailed
	default:
		return model.EventSucceeded
	}
}

func inferScopeFromProject(project string) model.Scope {
	lower := strings.ToLower(project)
	switch {
	case strings.HasPrefix(lower, "isv:percona:pr:"):
		return model.ScopePR
	case strings.Contains(lower, "container"):
		return model.ScopeContainer
	case strings.Contains(lower, "release"):
		return model.ScopeRelease
	case strings.Contains(lower, "ppgcommon"):
		return model.ScopePPGCommon
	case strings.Contains(lower, "common"):
		return model.ScopeCommon
	default:
		return model.ScopeVersion
	}
}
