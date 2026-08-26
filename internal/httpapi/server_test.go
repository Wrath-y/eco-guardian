package httpapi

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeRejectsNonLoopbackBind(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", "[::1]:8080", "127.0.0.2:8080", "localhost:8080"} {
		if _, err := NewRuntime(address); err == nil {
			t.Fatalf("accepted non-canonical bind %s", address)
		}
	}
}

func TestRuntimeStopIsConcurrentAndIdempotent(t *testing.T) {
	runtime, err := NewLoopbackRuntime(0)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if stopErr := runtime.StopHost(context.Background()); stopErr != nil {
				errorsFound <- stopErr
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for stopErr := range errorsFound {
		t.Fatal(stopErr)
	}
	if connection, dialErr := net.Dial("tcp4", runtime.Address()); dialErr == nil {
		_ = connection.Close()
		t.Fatal("stopped runtime still accepted connections")
	}
}
func TestRuntimeStartsAndStops(t *testing.T) {
	r, err := NewRuntime("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Start(); err != nil {
		t.Fatal(err)
	}
	if r.Address() == "" {
		t.Fatal("no listener")
	}
	if err = r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRetainsTCP4ListenerAndDerivesCanonicalURL(t *testing.T) {
	runtime, err := NewLoopbackRuntime(0)
	if err != nil {
		t.Fatal(err)
	}
	url, err := runtime.AcquireListener(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	address := runtime.Address()
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || port == "" || url != "http://"+address || runtime.URL() != url {
		t.Fatalf("address=%q url=%q err=%v", address, url, err)
	}
	if competing, listenErr := net.Listen("tcp4", address); listenErr == nil {
		_ = competing.Close()
		t.Fatal("listener was not retained before host startup")
	}
	if err = runtime.StartHost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = runtime.WaitReachable(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(url + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	if err = runtime.StopHost(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPreferredPortFallsBackWithoutTouchingOccupant(t *testing.T) {
	occupant, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupant.Close()
	_, portValue, err := net.SplitHostPort(occupant.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewLoopbackRuntime(port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.AcquireListener(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.Address() == occupant.Addr().String() {
		t.Fatal("runtime did not fall back from occupied preferred port")
	}
	if err = runtime.StopHost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connection, dialErr := net.Dial("tcp4", occupant.Addr().String()); dialErr != nil {
		t.Fatalf("occupant was disturbed: %v", dialErr)
	} else {
		_ = connection.Close()
	}
}

func TestRuntimeRejectsUnboundOrForeignHostHeader(t *testing.T) {
	runtime, err := NewLoopbackRuntime(0)
	if err != nil {
		t.Fatal(err)
	}
	url, err := runtime.AcquireListener(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.StartHost(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer runtime.StopHost(context.Background())
	request, err := http.NewRequest(http.MethodGet, url+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "attacker.example"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host status=%d", response.StatusCode)
	}
	if strings.Contains(runtime.URL(), request.Host) {
		t.Fatalf("canonical URL used request Host: %q", runtime.URL())
	}
}
