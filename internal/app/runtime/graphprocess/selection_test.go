package graphprocess

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

type probeFake struct {
	observation CompatibilityObservation
	err         error
	calls       []string
}

func (probe *probeFake) ProbeCompatibility(_ context.Context, endpoint string) (CompatibilityObservation, error) {
	probe.calls = append(probe.calls, endpoint)
	return probe.observation, probe.err
}

type portFake struct{ ports []uint16 }

func (ports *portFake) NextLoopbackPort(context.Context) (uint16, error) {
	if len(ports.ports) == 0 {
		return 0, errors.New("exhausted")
	}
	value := ports.ports[0]
	ports.ports = ports.ports[1:]
	return value, nil
}

type ownedProcessFake struct {
	generation platformprocess.LaunchGeneration
	terminated bool
	closed     bool
}

func (*ownedProcessFake) DiagnosticPID() uint32 { return 99 }
func (process *ownedProcessFake) Generation() platformprocess.LaunchGeneration {
	return process.generation
}
func (*ownedProcessFake) Resume() error { return nil }
func (*ownedProcessFake) Wait(context.Context) (platformprocess.Exit, error) {
	return platformprocess.Exit{}, nil
}
func (*ownedProcessFake) RequestStop(context.Context) error { return nil }
func (process *ownedProcessFake) Terminate(uint32) error    { process.terminated = true; return nil }
func (process *ownedProcessFake) Close() error              { process.closed = true; return nil }

type starterFake struct {
	starts []uint16
	owned  *ownedProcessFake
	err    error
}

func (starter *starterFake) StartBundled(_ context.Context, port uint16) (platformprocess.OwnedProcess, string, error) {
	starter.starts = append(starter.starts, port)
	if starter.err != nil {
		return nil, "", starter.err
	}
	return starter.owned, "http://127.0.0.1:" + strconv.Itoa(int(port)), nil
}

func TestSelectorClassifiesOnlyCompatibleEndpointAsExternal(t *testing.T) {
	probe := &probeFake{observation: CompatibilityObservation{Compatible: true}}
	starter := &starterFake{}
	selection, err := (Selector{Prober: probe, Starter: starter}).Select(t.Context(), SelectionRequest{
		Mode: runtimeconfig.GraphExternal, ExplicitEndpoint: "http://127.0.0.1:9300",
	})
	if err != nil || selection.Ownership != platformprocess.OwnershipExternal || selection.Process != nil || len(starter.starts) != 0 {
		t.Fatalf("selection=%#v starts=%v err=%v", selection, starter.starts, err)
	}
}

func TestSelectorNeverKillsIncompatibleExternalAndUsesSeparateBundledPort(t *testing.T) {
	probe := &probeFake{observation: CompatibilityObservation{Reasons: []string{"API_V1_UNSUPPORTED"}}}
	ports := &portFake{ports: []uint16{9300, 9400}}
	owned := &ownedProcessFake{generation: 7}
	starter := &starterFake{owned: owned}
	selection, err := (Selector{Prober: probe, Ports: ports, Starter: starter}).Select(t.Context(), SelectionRequest{
		Mode: runtimeconfig.GraphExternal, ExplicitEndpoint: "http://127.0.0.1:9300", PackageAllowsBundled: true, CandidateAttempts: 2,
	})
	if err != nil || selection.Ownership != platformprocess.OwnershipBundled || selection.Endpoint != "http://127.0.0.1:9400" {
		t.Fatalf("selection=%#v err=%v", selection, err)
	}
	if !reflect.DeepEqual(starter.starts, []uint16{9400}) || owned.terminated || owned.closed {
		t.Fatalf("starts=%v owned=%#v", starter.starts, owned)
	}
}

func TestSelectorLightweightKeepsIncompatibleOccupantUntouched(t *testing.T) {
	probe := &probeFake{observation: CompatibilityObservation{Reasons: []string{"SNAPSHOT_SCHEMA_UNSUPPORTED"}}}
	starter := &starterFake{}
	selection, err := (Selector{Prober: probe, Starter: starter}).Select(t.Context(), SelectionRequest{
		Mode: runtimeconfig.GraphExternal, ExplicitEndpoint: "http://127.0.0.1:9300", PackageAllowsBundled: false,
	})
	if !errors.Is(err, ErrSelectionUnavailable) || selection.Ownership != platformprocess.OwnershipNone || !reflect.DeepEqual(selection.Reasons, []string{"SNAPSHOT_SCHEMA_UNSUPPORTED"}) || len(starter.starts) != 0 {
		t.Fatalf("selection=%#v starts=%v err=%v", selection, starter.starts, err)
	}
}

func TestSelectorRejectsInvalidOwnedStartResult(t *testing.T) {
	owned := &ownedProcessFake{}
	starter := &starterFake{owned: owned}
	selection, err := (Selector{Ports: &portFake{ports: []uint16{9400}}, Starter: starter}).Select(t.Context(), SelectionRequest{
		Mode: runtimeconfig.GraphBundled, PackageAllowsBundled: true, CandidateAttempts: 1,
	})
	if !errors.Is(err, ErrSelectionUnavailable) || selection.Ownership != platformprocess.OwnershipNone || !owned.terminated || !owned.closed {
		t.Fatalf("selection=%#v owned=%#v err=%v", selection, owned, err)
	}
}
