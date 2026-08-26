//go:build windows

package graphprocess

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

type windowsHelperCommands struct{ mode string }

func (commands windowsHelperCommands) BuildBundled(port uint16) (BundledCommand, error) {
	environment := []string{"ECO_GRAPH_HELPER=" + commands.mode, "ECO_GRAPH_PORT=" + strconv.Itoa(int(port))}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return BundledCommand{
		Process:  platformprocess.Command{Executable: os.Args[0], WorkingDirectory: filepath.Dir(os.Args[0]), Arguments: []string{"-test.run=^TestWindowsGraphSupervisorHelper$"}, Environment: environment},
		Endpoint: fmt.Sprintf("http://127.0.0.1:%d", port),
	}, nil
}

func windowsSupervisor(t *testing.T, commands CommandFactory, ports []uint16, attempts int) *Supervisor {
	t.Helper()
	client := &http.Client{Timeout: 50 * time.Millisecond}
	supervisor, err := NewSupervisor(SupervisorOptions{
		Adapter: platformprocess.New(), Commands: commands, Ports: &portFake{ports: ports},
		Readiness: ReadinessFunc(func(ctx context.Context, endpoint string, _ platformprocess.LaunchGeneration) error {
			for {
				request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
				response, requestErr := client.Do(request)
				if requestErr == nil {
					body, _ := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK && string(body) == "compatible" {
						return nil
					}
				}
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}),
		Policy: SupervisorPolicy{MaximumAttempts: attempts, AttemptWindow: time.Minute, ReadinessTimeout: 2 * time.Second, StableReady: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	return supervisor
}

func TestWindowsSupervisorCrashLoopExhaustion(t *testing.T) {
	supervisor := windowsSupervisor(t, windowsHelperCommands{mode: "crash"}, []uint16{43002}, 2)
	_, _, err := supervisor.StartBundled(t.Context(), 43001)
	if !errors.Is(err, ErrRestartExhausted) || supervisor.Snapshot().State != StateRestartExhausted {
		t.Fatalf("snapshot=%#v err=%v", supervisor.Snapshot(), err)
	}
	_ = supervisor.Stop(t.Context())
}

func TestWindowsSupervisorBindCollisionRetriesFreshPortAndGracefulTimeoutKillsOwnedOnly(t *testing.T) {
	occupant, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupant.Close()
	occupiedPort := uint16(occupant.Addr().(*net.TCPAddr).Port)
	candidate, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freshPort := uint16(candidate.Addr().(*net.TCPAddr).Port)
	_ = candidate.Close()
	supervisor := windowsSupervisor(t, windowsHelperCommands{mode: "serve"}, []uint16{freshPort}, 2)
	owned, endpoint, err := supervisor.StartBundled(t.Context(), occupiedPort)
	if err != nil || endpoint != fmt.Sprintf("http://127.0.0.1:%d", freshPort) || owned.Generation() != 2 {
		t.Fatalf("endpoint=%s generation=%d err=%v", endpoint, owned.Generation(), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err = supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	waitContext, waitCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer waitCancel()
	if _, err = owned.Wait(waitContext); err != nil {
		t.Fatalf("owned child survived Job fallback: %v", err)
	}
	if probe, probeErr := net.DialTimeout("tcp4", occupant.Addr().String(), time.Second); probeErr != nil {
		t.Fatalf("incompatible occupant was affected: %v", probeErr)
	} else {
		_ = probe.Close()
	}
}

func TestWindowsSupervisorRejectsStaleGenerationRegardlessOfDiagnosticPID(t *testing.T) {
	firstListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstPort := uint16(firstListener.Addr().(*net.TCPAddr).Port)
	_ = firstListener.Close()
	secondListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondPort := uint16(secondListener.Addr().(*net.TCPAddr).Port)
	_ = secondListener.Close()
	supervisor := windowsSupervisor(t, windowsHelperCommands{mode: "serve"}, []uint16{secondPort}, 3)
	if _, _, err = supervisor.StartBundled(t.Context(), firstPort); err != nil {
		t.Fatal(err)
	}
	if err = supervisor.TerminateOwned(1, 23); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot := supervisor.Snapshot(); snapshot.State == StateReady && snapshot.Generation == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot := supervisor.Snapshot(); snapshot.State != StateReady || snapshot.Generation != 2 {
		t.Fatalf("restart snapshot=%#v", snapshot)
	}
	if err = supervisor.TerminateOwned(1, 1); !errors.Is(err, platformprocess.ErrOwnership) {
		t.Fatalf("stale generation err=%v", err)
	}
	if response, probeErr := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", secondPort)); probeErr != nil {
		t.Fatalf("stale generation affected current child: %v", probeErr)
	} else {
		_ = response.Body.Close()
	}
	_ = supervisor.Stop(t.Context())
}

func TestWindowsGraphSupervisorHelper(t *testing.T) {
	mode := os.Getenv("ECO_GRAPH_HELPER")
	if mode == "" {
		return
	}
	if mode == "crash" {
		os.Exit(7)
	}
	if mode != "serve" {
		os.Exit(8)
	}
	port, err := strconv.Atoi(os.Getenv("ECO_GRAPH_PORT"))
	if err != nil {
		os.Exit(9)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("compatible")) })
	if err = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux); err != nil {
		os.Exit(10)
	}
}
