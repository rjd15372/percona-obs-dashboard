package workingset

import (
	"context"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// Stats is a snapshot of working-set size.
type Stats struct {
	Total    int
	Inflight int
	ByState  map[string]int // rollup_state → count
}

// entry is a working-set member with its poll schedule. interval follows a
// backoff ladder: reset to base when a pass observes a change, doubled (up
// to max) when a pass observes nothing new.
type entry struct {
	pkg      *model.Package
	state    string
	interval time.Duration
	nextDue  time.Time
	wake     bool
}

// Job is a unit of work for the worker pool: one or more packages of the
// same project. ProjectFetch marks that the worker should make a single
// project-level _result call and process every package with the result.
type Job struct {
	Project      string
	ProjectFetch bool
	Pkgs         []*model.Package
}

// Gate lets the working set pause OBS-bound dispatching while no dashboard
// tab is watching. See internal/presence. A nil gate means "always
// dispatch".
type Gate interface {
	Active() bool
	Subscribe() <-chan struct{}
}

type WorkingSet struct {
	mu       sync.Mutex
	entries  map[string]*entry
	inflight map[string]bool
	dispatch chan Job
	gate     Gate

	base           time.Duration
	max            time.Duration
	batchThreshold int
	now            func() time.Time
}

// New creates a working set. base is the initial per-package poll interval
// (and the scheduler tick), max caps the backoff ladder. batchThreshold is
// the minimum number of due packages of one project that triggers a
// project-level batch fetch (used by the dispatcher; see Job).
func New(queueSize int, base, max time.Duration, batchThreshold int) *WorkingSet {
	return &WorkingSet{
		entries:        make(map[string]*entry),
		inflight:       make(map[string]bool),
		dispatch:       make(chan Job, queueSize),
		base:           base,
		max:            max,
		batchThreshold: batchThreshold,
		now:            time.Now,
	}
}

// NewWithClock is New with an injectable clock, for tests.
func NewWithClock(queueSize int, base, max time.Duration, batchThreshold int, clock func() time.Time) *WorkingSet {
	ws := New(queueSize, base, max, batchThreshold)
	ws.now = clock
	return ws
}

// SetGate installs the presence gate. Not safe to call concurrently with
// dispatching — wire it at startup.
func (ws *WorkingSet) SetGate(g Gate) {
	ws.gate = g
}

// Seed inserts packages without dispatching; they become due immediately and
// the first scheduler tick picks them up.
func (ws *WorkingSet) Seed(pkgs []*model.Package) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	for _, p := range pkgs {
		key := p.Project + "/" + p.Name
		ws.entries[key] = &entry{pkg: p, state: string(p.RollupState), interval: ws.base, nextDue: now}
	}
}

// Add inserts a package and dispatches it immediately. If the package is
// already present this is a wake: the entry is made due now WITHOUT
// resetting the backoff ladder (interval is left untouched), so blind
// periodic re-adds (e.g. the poller re-adding unpublished release packages
// every tick) don't pin a quiet package at base cadence forever. A pass that
// observes a real change still resets the interval via Done(changed=true).
func (ws *WorkingSet) Add(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if e, exists := ws.entries[key]; exists {
		// Wake: make the entry due now but keep the ladder position. A pass
		// that observes a real change resets the interval via Done; blind
		// re-adds (e.g. the poller's periodic release-package add) must not
		// pin a quiet package at base cadence forever.
		e.nextDue = ws.now()
		if ws.inflight[key] {
			e.wake = true
		}
		return
	}
	ws.entries[key] = &entry{pkg: pkg, state: string(pkg.RollupState), interval: ws.base, nextDue: ws.now()}
	if !ws.inflight[key] {
		ws.sendJob(Job{Pkgs: []*model.Package{pkg}})
	}
}

// Signal replaces the stored package, fully resets its schedule (interval
// back to base — MQ events are strong change signals), and dispatches
// immediately (unless in-flight, in which case it is marked to wake as soon
// as the in-flight pass completes). Used by the MQ consumer for real-time
// reactions.
func (ws *WorkingSet) Signal(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.entries[key] = &entry{pkg: pkg, state: string(pkg.RollupState), interval: ws.base, nextDue: ws.now(), wake: ws.inflight[key]}
	if !ws.inflight[key] {
		ws.sendJob(Job{Pkgs: []*model.Package{pkg}})
	}
}

func (ws *WorkingSet) Remove(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.entries, key)
	delete(ws.inflight, key)
}

// Done marks a package as no longer in-flight and advances its schedule:
// a changed pass resets the interval to base, an unchanged pass doubles it
// up to max. The next pass is due interval from now, unless a Signal/Add
// landed while this pass was in flight, in which case it stays due now.
func (ws *WorkingSet) Done(key string, changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.inflight, key)
	e, ok := ws.entries[key]
	if !ok {
		return // removed while in-flight
	}
	e.state = string(e.pkg.RollupState)
	if changed {
		e.interval = ws.base
	} else {
		e.interval *= 2
		if e.interval > ws.max {
			e.interval = ws.max
		}
	}
	if e.wake {
		// A Signal/Add landed while this pass was in flight: stay due now
		// instead of being pushed a full interval out.
		e.wake = false
		e.nextDue = ws.now()
		return
	}
	e.nextDue = ws.now().Add(e.interval)
}

func (ws *WorkingSet) Dispatch() <-chan Job {
	return ws.dispatch
}

// Stats returns a snapshot of the current working set under the lock.
func (ws *WorkingSet) Stats() Stats {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	s := Stats{Total: len(ws.entries), Inflight: len(ws.inflight), ByState: make(map[string]int)}
	for _, e := range ws.entries {
		s.ByState[e.state]++
	}
	return s
}

// DispatchDue enqueues every entry whose nextDue has passed and that is not
// already in-flight. Due packages of the same project are batched into one
// project-fetch job when the group reaches batchThreshold; below that,
// per-package fetches are cheaper than a project-level _result. Called by
// the scheduler tick; exported for tests.
func (ws *WorkingSet) DispatchDue() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	byProject := make(map[string][]*model.Package)
	for key, e := range ws.entries {
		if e.nextDue.After(now) || ws.inflight[key] {
			continue
		}
		byProject[e.pkg.Project] = append(byProject[e.pkg.Project], e.pkg)
	}
	for project, pkgs := range byProject {
		if ws.batchThreshold > 0 && len(pkgs) >= ws.batchThreshold {
			ws.sendJob(Job{Project: project, ProjectFetch: true, Pkgs: pkgs})
			continue
		}
		for _, p := range pkgs {
			ws.sendJob(Job{Pkgs: []*model.Package{p}})
		}
	}
}

// StartScheduler ticks at the base interval and dispatches due packages.
// While the gate is idle the tick is skipped; an idle→active wake signal
// dispatches immediately.
func (ws *WorkingSet) StartScheduler(ctx context.Context) {
	go func() {
		var wake <-chan struct{}
		if ws.gate != nil {
			wake = ws.gate.Subscribe()
		}
		ticker := time.NewTicker(ws.base)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ws.gate == nil || ws.gate.Active() {
					ws.DispatchDue()
				}
			case <-wake:
				ws.DispatchDue()
			}
		}
	}()
}

// sendJob attempts a non-blocking enqueue and marks every package in the job
// as in-flight on success. Drops the job if the channel is full — the
// packages stay due and are retried on the next tick. While the presence
// gate is idle, jobs are dropped the same way (drained on wake). Must be
// called with ws.mu held. Callers must ensure no package in the job is
// already in-flight.
func (ws *WorkingSet) sendJob(job Job) {
	if ws.gate != nil && !ws.gate.Active() {
		return
	}
	select {
	case ws.dispatch <- job:
		for _, p := range job.Pkgs {
			ws.inflight[p.Project+"/"+p.Name] = true
		}
	default:
	}
}
