package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Runtime only accepts a loopback listener: the local UI must never expose a
// project-selection capability to the network.
type Runtime struct {
	mu               sync.RWMutex
	address          string
	fallbackToRandom bool
	engine           *gin.Engine
	server           *http.Server
	listener         net.Listener
	done             chan struct{}
	doneOnce         sync.Once
	stopOnce         sync.Once
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
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return nil, errors.New("HTTP runtime must bind to 127.0.0.1")
	}
	r := &Runtime{address: address, engine: gin.New(), done: make(chan struct{})}
	r.engine.Use(r.admitCanonicalHost)
	r.engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r, nil
}

// NewLoopbackRuntime selects a bounded preferred port when configured and
// falls back to an operating-system assigned loopback port if it is occupied.
func NewLoopbackRuntime(preferredPort int) (*Runtime, error) {
	if preferredPort < 0 || preferredPort > 65535 {
		return nil, errors.New("preferred HTTP port must be between 1 and 65535")
	}
	address := "127.0.0.1:0"
	if preferredPort != 0 {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(preferredPort))
	}
	runtime, err := NewRuntime(address)
	if err != nil {
		return nil, err
	}
	runtime.fallbackToRandom = preferredPort != 0
	return runtime, nil
}

// SetPreferredPort configures the listener before acquisition. Settings and
// bounded launch overrides are loaded by the phased coordinator before this
// method is used; a retained listener can never be reconfigured.
func (r *Runtime) SetPreferredPort(preferredPort int) error {
	if r == nil || preferredPort < 0 || preferredPort > 65535 {
		return errors.New("preferred HTTP port must be between 1 and 65535")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener != nil || r.server != nil {
		return errors.New("HTTP listener is already acquired")
	}
	r.address = "127.0.0.1:0"
	r.fallbackToRandom = false
	if preferredPort != 0 {
		r.address = net.JoinHostPort("127.0.0.1", strconv.Itoa(preferredPort))
		r.fallbackToRandom = true
	}
	return nil
}

// AcquireListener binds once and retains the TCP4 listener through host
// startup, eliminating probe-then-bind races.
func (r *Runtime) AcquireListener(ctx context.Context) (string, error) {
	if r == nil {
		return "", errors.New("HTTP runtime is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener != nil {
		return listenerURL(r.listener)
	}
	listen := net.ListenConfig{}
	listener, err := listen.Listen(ctx, "tcp4", r.address)
	if err != nil && r.fallbackToRandom {
		listener, err = listen.Listen(ctx, "tcp4", "127.0.0.1:0")
	}
	if err != nil {
		return "", err
	}
	if _, err = listenerURL(listener); err != nil {
		_ = listener.Close()
		return "", err
	}
	r.listener = listener
	return listenerURL(listener)
}

func listenerURL(listener net.Listener) (string, error) {
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.IP == nil || !address.IP.Equal(net.IPv4(127, 0, 0, 1)) || address.Port < 1 {
		return "", errors.New("HTTP listener must use 127.0.0.1 TCP")
	}
	return "http://" + net.JoinHostPort(address.IP.String(), strconv.Itoa(address.Port)), nil
}

// admitCanonicalHost prevents request Host values from becoming a DNS
// rebinding or URL-advertisement input. Public URLs are always derived from
// the retained listener instead.
func (r *Runtime) admitCanonicalHost(c *gin.Context) {
	expected := r.Address()
	if expected == "" || c.Request.Host != expected {
		c.AbortWithStatus(http.StatusMisdirectedRequest)
		return
	}
	c.Next()
}

func (r *Runtime) Start() error {
	if _, err := r.AcquireListener(context.Background()); err != nil {
		return err
	}
	return r.StartHost(context.Background())
}

func (r *Runtime) StartHost(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.listener == nil {
		r.mu.Unlock()
		return errors.New("HTTP listener has not been acquired")
	}
	if r.server != nil {
		r.mu.Unlock()
		return nil
	}
	server := &http.Server{Handler: r.engine, ReadHeaderTimeout: 5 * time.Second}
	listener := r.listener
	r.server = server
	r.mu.Unlock()
	go func() {
		_ = server.Serve(listener)
		r.doneOnce.Do(func() { close(r.done) })
	}()
	return nil
}

func (r *Runtime) WaitReachable(ctx context.Context) error {
	address := r.Address()
	if address == "" {
		return errors.New("HTTP listener is unavailable")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp4", address)
	if err != nil {
		return err
	}
	return connection.Close()
}

func (r *Runtime) Address() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.listener == nil {
		return ""
	}
	return r.listener.Addr().String()
}

func (r *Runtime) URL() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.listener == nil {
		return ""
	}
	value, _ := listenerURL(r.listener)
	return value
}

func (r *Runtime) Engine() *gin.Engine { return r.engine }

func (r *Runtime) StopHost(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var stopErr error
	r.stopOnce.Do(func() {
		r.mu.RLock()
		server := r.server
		listener := r.listener
		r.mu.RUnlock()
		if server != nil {
			stopErr = server.Shutdown(ctx)
			if stopErr != nil {
				_ = server.Close()
			}
			return
		}
		if listener != nil {
			stopErr = listener.Close()
		}
		r.doneOnce.Do(func() { close(r.done) })
	})
	if stopErr != nil && !errors.Is(stopErr, net.ErrClosed) {
		return fmt.Errorf("stop HTTP host: %w", stopErr)
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Close(ctx context.Context) error { return r.StopHost(ctx) }
