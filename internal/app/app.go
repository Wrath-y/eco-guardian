// Package app composes process-owned services. It deliberately contains no
// domain logic, so every dependency can be replaced in focused tests.
package app

import (
	"context"
	"sync"
)

// Runtime is the loopback HTTP service owned by the process composition root.
type Runtime interface {
	Start() error
	Close(context.Context) error
}

// ProjectManager is intentionally a narrow lifecycle port. The concrete
// single-project manager lives in internal/project and is closed only after
// the HTTP listener has stopped accepting requests.
type ProjectManager interface {
	Close(context.Context) error
}

// Application owns service lifetime in one deterministic place.
type Application struct {
	mu       sync.Mutex
	started  bool
	runtime  Runtime
	projects ProjectManager
}

// Config contains process-level configuration. Its zero value is safe for
// command and unit-test startup without an active project.
type Config struct {
	Runtime  Runtime
	Projects ProjectManager
}

func New(config Config) (*Application, error) {
	return &Application{runtime: config.Runtime, projects: config.Projects}, nil
}

func (a *Application) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	if a.runtime != nil {
		if err := a.runtime.Start(); err != nil {
			return err
		}
	}
	a.started = true
	return nil
}

func (a *Application) Close(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return nil
	}
	var first error
	if a.runtime != nil {
		if err := a.runtime.Close(ctx); err != nil {
			first = err
		}
	}
	if a.projects != nil {
		if err := a.projects.Close(ctx); err != nil && first == nil {
			first = err
		}
	}
	a.started = false
	return first
}

func (a *Application) Started() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}
