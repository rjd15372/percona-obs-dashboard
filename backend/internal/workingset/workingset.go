package workingset

import (
	"context"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

type WorkingSet struct {
	mu       sync.RWMutex
	packages map[string]*model.Package
	dispatch chan *model.Package
}

func New(queueSize int) *WorkingSet {
	return &WorkingSet{
		packages: make(map[string]*model.Package),
		dispatch: make(chan *model.Package, queueSize),
	}
}

func (ws *WorkingSet) Seed(pkgs []*model.Package) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, p := range pkgs {
		ws.packages[p.Project+"/"+p.Name] = p
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
	ws.send(pkg)
}

func (ws *WorkingSet) Signal(pkg *model.Package) {
	key := pkg.Project + "/" + pkg.Name
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.packages[key] = pkg
	ws.send(pkg)
}

func (ws *WorkingSet) Remove(key string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	delete(ws.packages, key)
}

func (ws *WorkingSet) Dispatch() <-chan *model.Package {
	return ws.dispatch
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
				ws.mu.RLock()
				for _, p := range ws.packages {
					ws.send(p)
				}
				ws.mu.RUnlock()
			}
		}
	}()
}

// send attempts a non-blocking send. Must be called with ws.mu held (read or write).
func (ws *WorkingSet) send(pkg *model.Package) {
	select {
	case ws.dispatch <- pkg:
	default:
	}
}
