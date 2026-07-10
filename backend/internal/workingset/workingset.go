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
	interval time.Duration
	nextDue  time.Time
}

type WorkingSet struct {
	mu       sync.Mutex
	entries  map[string]*entry
	inflight map[string]bool
	dispatch chan *model.Package

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
		dispatch:       make(chan *model.Package, queueSize),
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

// Seed inserts packages without dispatching; they become due immediately and
// the first scheduler tick picks them up.
func (ws *WorkingSet) Seed(pkgs []*model.Package) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	for _, p := range pkgs {
		key := p.Project + "/" + p.Name
		ws.entries[key] = &entry{pkg: p, interval: ws.base, nextDue: now}
	}
}

// Add inserts a package and dispatches it immediately. If the package is
// already present this is a wake signal: its schedule resets to due-now at
// the base interval (the stored, possibly enriched, package object is kept)
// but nothing is dispatched — the next scheduler tick handles it.
func (ws *WorkingSet) Add(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if e, exists := ws.entries[key]; exists {
		e.interval = ws.base
		e.nextDue = ws.now()
		return
	}
	ws.entries[key] = &entry{pkg: pkg, interval: ws.base, nextDue: ws.now()}
	ws.send(key, pkg)
}

// Signal replaces the stored package, resets its schedule, and dispatches
// immediately (unless in-flight). Used by the MQ consumer for real-time
// reactions.
func (ws *WorkingSet) Signal(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.entries[key] = &entry{pkg: pkg, interval: ws.base, nextDue: ws.now()}
	ws.send(key, pkg)
}

func (ws *WorkingSet) Remove(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.entries, key)
	delete(ws.inflight, key)
}

// Done marks a package as no longer in-flight and advances its schedule:
// a changed pass resets the interval to base, an unchanged pass doubles it
// up to max. The next pass is due interval from now.
func (ws *WorkingSet) Done(key string, changed bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.inflight, key)
	e, ok := ws.entries[key]
	if !ok {
		return // removed while in-flight
	}
	if changed {
		e.interval = ws.base
	} else {
		e.interval *= 2
		if e.interval > ws.max {
			e.interval = ws.max
		}
	}
	e.nextDue = ws.now().Add(e.interval)
}

func (ws *WorkingSet) Dispatch() <-chan *model.Package {
	return ws.dispatch
}

// Stats returns a snapshot of the current working set under the lock.
func (ws *WorkingSet) Stats() Stats {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	s := Stats{Total: len(ws.entries), Inflight: len(ws.inflight), ByState: make(map[string]int)}
	for _, e := range ws.entries {
		s.ByState[string(e.pkg.RollupState)]++
	}
	return s
}

// DispatchDue enqueues every entry whose nextDue has passed and that is not
// already in-flight. Called by the scheduler tick; exported for tests.
func (ws *WorkingSet) DispatchDue() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	now := ws.now()
	for key, e := range ws.entries {
		if e.nextDue.After(now) {
			continue
		}
		ws.send(key, e.pkg)
	}
}

// StartScheduler ticks at the base interval and dispatches due packages.
func (ws *WorkingSet) StartScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(ws.base)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws.DispatchDue()
			}
		}
	}()
}

// send attempts a non-blocking enqueue. Drops the send if the package is
// already in-flight (being processed by a worker) or if the channel is full
// (retried on the next tick). Must be called with ws.mu held.
func (ws *WorkingSet) send(key string, pkg *model.Package) {
	if ws.inflight[key] {
		return
	}
	select {
	case ws.dispatch <- pkg:
		ws.inflight[key] = true
	default:
	}
}
