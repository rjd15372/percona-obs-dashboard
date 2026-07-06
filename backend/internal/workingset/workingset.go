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

type WorkingSet struct {
	mu       sync.Mutex
	packages map[string]*model.Package
	states   map[string]string
	inflight map[string]bool
	dispatch chan *model.Package
}

func New(queueSize int) *WorkingSet {
	return &WorkingSet{
		packages: make(map[string]*model.Package),
		states:   make(map[string]string),
		inflight: make(map[string]bool),
		dispatch: make(chan *model.Package, queueSize),
	}
}

func (ws *WorkingSet) Seed(pkgs []*model.Package) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, p := range pkgs {
		key := p.Project + "/" + p.Name
		ws.packages[key] = p
		ws.states[key] = string(p.RollupState)
	}
}

func (ws *WorkingSet) Add(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if _, exists := ws.packages[key]; exists {
		return
	}
	ws.packages[key] = pkg
	ws.states[key] = string(pkg.RollupState)
	ws.send(key, pkg)
}

func (ws *WorkingSet) Signal(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.packages[key] = pkg
	ws.states[key] = string(pkg.RollupState)
	ws.send(key, pkg)
}

func (ws *WorkingSet) Remove(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.packages, key)
	delete(ws.states, key)
	delete(ws.inflight, key)
}

// Done marks a package as no longer in-flight, allowing the scheduler to
// re-dispatch it on the next tick.
func (ws *WorkingSet) Done(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.inflight, key)
}

func (ws *WorkingSet) Dispatch() <-chan *model.Package {
	return ws.dispatch
}

// Stats returns a snapshot of the current working set under the lock.
func (ws *WorkingSet) Stats() Stats {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	s := Stats{Total: len(ws.packages), Inflight: len(ws.inflight), ByState: make(map[string]int)}
	for _, state := range ws.states {
		s.ByState[state]++
	}
	return s
}

func (ws *WorkingSet) StartScheduler(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws.mu.Lock()
				for key, p := range ws.packages {
					ws.send(key, p)
				}
				ws.mu.Unlock()
			}
		}
	}()
}

// send attempts a non-blocking enqueue. Drops the send if the package is
// already in-flight (being processed by a worker) or if the channel is full.
// Must be called with ws.mu held.
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
