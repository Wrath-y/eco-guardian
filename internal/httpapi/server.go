package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Runtime only accepts a loopback listener: the local UI must never expose a
// project-selection capability to the network.
type Runtime struct {
	address  string
	engine   *gin.Engine
	server   *http.Server
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

func NewRuntime(address string) (*Runtime, error) {
	if address == "" {
		address = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("HTTP runtime must bind to a loopback address")
	}
	r := &Runtime{address: address, engine: gin.New(), done: make(chan struct{})}
	r.engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r, nil
}
func (r *Runtime) Start() error {
	l, err := net.Listen("tcp", r.address)
	if err != nil {
		return err
	}
	r.listener = l
	r.server = &http.Server{Handler: r.engine, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.server.Serve(l); close(r.done) }()
	return nil
}
func (r *Runtime) Address() string {
	if r.listener == nil {
		return ""
	}
	return r.listener.Addr().String()
}
func (r *Runtime) Engine() *gin.Engine { return r.engine }
func (r *Runtime) Close(ctx context.Context) error {
	r.once.Do(func() {
		if r.server == nil {
			close(r.done)
			return
		}
		_ = r.server.Shutdown(ctx)
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
