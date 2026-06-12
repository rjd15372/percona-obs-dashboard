package mq

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/oklog/ulid/v2"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

const (
	exchange        = "pubsub"
	packageRouteKey = "opensuse.obs.package.#"
	repoRouteKey    = "opensuse.obs.repo.published"
)

// mqMessage is the JSON structure of OBS MQ events.
type mqMessage struct {
	Project string `json:"project"`
	Package string `json:"package"`
	Repo    string `json:"repository"`
	Arch    string `json:"arch"`
	Reason  string `json:"reason"`
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
			next := time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
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

	for _, key := range []string{packageRouteKey, repoRouteKey} {
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

	case isPackageBuildEvent(key):
		rollup := mqStateToRollup(key)
		evtType := rollupToEventType(rollup)
		scope := inferScopeFromProject(m.Project)
		pkg := &model.Package{
			Project:      m.Project,
			Name:         m.Package,
			Scope:        scope,
			RollupState:  rollup,
			OKTargets:    0,
			TotalTargets: 1,
			Targets: []model.Target{
				{Repo: m.Repo, Arch: m.Arch, State: string(rollup)},
			},
			UpdatedAt: time.Now().UTC(),
		}
		if rollup == model.RollupSucceeded {
			pkg.OKTargets = 1
		}
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
