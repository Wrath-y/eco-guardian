// Package app composes process-owned services. It deliberately contains no
// domain logic, so every dependency can be replaced in focused tests.
package app

import (
	"context"
	"sync"
)

// Config contains process-level configuration. A zero value is safe for
// command and unit-test startup; HTTP is attached by a later composition step.
type Config struct{}

// Application owns service lifetime in one deterministic place.
type Application struct {
	mu      sync.Mutex
	started bool
}

func New(Config) (*Application, error) { return &Application{}, nil }

func (a *Application) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = true
	return nil
}

func (a *Application) Close(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = false
	return nil
}

func (a *Application) Started() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.started
}
