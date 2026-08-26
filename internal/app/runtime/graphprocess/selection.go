package graphprocess

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

var ErrSelectionUnavailable = errors.New("local-rag selection is unavailable")

type CompatibilityObservation struct {
	Compatible bool
	Reasons    []string
}

type CompatibilityProber interface {
	ProbeCompatibility(context.Context, string) (CompatibilityObservation, error)
}

type PortCandidates interface {
	NextLoopbackPort(context.Context) (uint16, error)
}

type BundledStarter interface {
	StartBundled(context.Context, uint16) (platformprocess.OwnedProcess, string, error)
}

type SelectionRequest struct {
	Mode                 runtimeconfig.GraphMode
	ExplicitEndpoint     string
	PackageAllowsBundled bool
	CandidateAttempts    int
}

type Selection struct {
	Ownership platformprocess.Ownership
	Endpoint  string
	Process   platformprocess.OwnedProcess
	Reasons   []string
}

type Selector struct {
	Prober  CompatibilityProber
	Ports   PortCandidates
	Starter BundledStarter
}

func (selector Selector) Select(ctx context.Context, request SelectionRequest) (Selection, error) {
	if request.Mode == runtimeconfig.GraphDisabled {
		return Selection{Ownership: platformprocess.OwnershipNone}, nil
	}
	result := Selection{Ownership: platformprocess.OwnershipNone}
	if strings.TrimSpace(request.ExplicitEndpoint) != "" {
		if selector.Prober == nil {
			return result, ErrSelectionUnavailable
		}
		observation, err := selector.Prober.ProbeCompatibility(ctx, request.ExplicitEndpoint)
		if err == nil && observation.Compatible {
			return Selection{Ownership: platformprocess.OwnershipExternal, Endpoint: request.ExplicitEndpoint}, nil
		}
		result.Reasons = append([]string(nil), observation.Reasons...)
		if err != nil {
			result.Reasons = []string{"EXTERNAL_PROBE_FAILED"}
		}
	}
	if !request.PackageAllowsBundled || selector.Ports == nil || selector.Starter == nil {
		if len(result.Reasons) == 0 {
			result.Reasons = []string{"BUNDLED_PROCESS_UNAVAILABLE"}
		}
		return result, ErrSelectionUnavailable
	}
	attempts := request.CandidateAttempts
	if attempts < 1 || attempts > 10 {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		port, err := selector.Ports.NextLoopbackPort(ctx)
		if err != nil || sameEndpointPort(request.ExplicitEndpoint, port) {
			continue
		}
		owned, endpoint, startErr := selector.Starter.StartBundled(ctx, port)
		if startErr != nil {
			continue
		}
		if owned == nil || owned.Generation() == 0 || endpoint != fmt.Sprintf("http://127.0.0.1:%d", port) {
			if owned != nil {
				_ = owned.Terminate(1)
				_ = owned.Close()
			}
			continue
		}
		return Selection{Ownership: platformprocess.OwnershipBundled, Endpoint: endpoint, Process: owned}, nil
	}
	result.Reasons = []string{"BUNDLED_START_FAILED"}
	return result, ErrSelectionUnavailable
}

func sameEndpointPort(endpoint string, candidate uint16) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Port() == "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	return err == nil && port == int(candidate)
}
