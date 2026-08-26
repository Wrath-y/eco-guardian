package runtime

import (
	"context"
	"errors"
	"time"
)

const defaultStartupDeadline = 30 * time.Second

var ErrRuntimeAlreadyStarted = errors.New("runtime coordinator already started")

// StartupDegradation lets package verification report a bounded component
// degradation while the offline host continues. All other errors from the
// hard settings/package/listener/HTTP stages stop startup.
type StartupDegradation struct{ Reason RuntimeReason }

func (e StartupDegradation) Error() string {
	if e.Reason.Code == "" {
		return "runtime startup degraded"
	}
	return e.Reason.Code
}

// StartupDegradations lets package verification classify one unavailable
// component into its exact process/Graph/retrieval effects without failing the
// offline host.
type StartupDegradations struct{ Reasons []RuntimeReason }

func (e StartupDegradations) Error() string {
	return "runtime startup has unavailable packaged capabilities"
}

// RecentProjectDegradation carries only a safe project-open classification.
// Concrete lock, schema, migration, and recovery errors stay in composition.
type RecentProjectDegradation struct{ Reason RuntimeReason }

func (e RecentProjectDegradation) Error() string {
	if e.Reason.Code == "" {
		return "recent project startup degraded"
	}
	return e.Reason.Code
}

func (c *Coordinator) Start(ctx context.Context, deadline time.Duration) (StatusSnapshot, error) {
	if c == nil || c.Status == nil {
		return StatusSnapshot{}, ErrRuntimePhaseInvalid
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return c.Status.Snapshot(), ErrRuntimeAlreadyStarted
	}
	c.started = true
	c.mu.Unlock()
	if deadline <= 0 {
		deadline = defaultStartupDeadline
	}
	startupContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	reasons := []RuntimeReason{}
	observations := []RuntimeObservation{}
	listenerURL := ""
	hostAcquired, dependenciesStarted := false, false

	if err := c.Ports.Settings.LoadSettings(startupContext); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "SETTINGS_LOAD_FAILED", Component: "settings", Message: "Runtime settings could not be loaded"}, hostAcquired, dependenciesStarted, err)
	}
	if _, err := c.Status.Transition(PhaseVerifyingPackage, nil); err != nil {
		return c.Status.Snapshot(), err
	}
	if err := c.Ports.Package.VerifyPackage(startupContext); err != nil {
		var degradation StartupDegradation
		var degradations StartupDegradations
		if errors.As(err, &degradations) && len(degradations.Reasons) > 0 {
			reasons = append(reasons, degradations.Reasons...)
		} else if errors.As(err, &degradation) && degradation.Reason.Code != "" {
			reasons = append(reasons, degradation.Reason)
		} else {
			return c.abortStartup(startupContext, RuntimeReason{Code: "PACKAGE_VERIFICATION_FAILED", Component: "package", Message: "Runtime package verification failed"}, hostAcquired, dependenciesStarted, err)
		}
	}
	if _, err := c.Status.Transition(PhaseBindingHTTP, func(snapshot *StatusSnapshot) { snapshot.Reasons = append([]RuntimeReason(nil), reasons...) }); err != nil {
		return c.Status.Snapshot(), err
	}
	var err error
	listenerURL, err = c.Ports.Host.AcquireListener(startupContext)
	if err != nil || listenerURL == "" {
		return c.abortStartup(startupContext, RuntimeReason{Code: "LISTENER_ACQUISITION_FAILED", Component: "http", Message: "Loopback listener could not be acquired"}, hostAcquired, dependenciesStarted, firstError(err, ErrRuntimePhaseInvalid))
	}
	hostAcquired = true
	if err = c.Ports.Host.StartHost(startupContext); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "HTTP_START_FAILED", Component: "http", Message: "Local HTTP host could not start"}, hostAcquired, dependenciesStarted, err)
	}
	if err = c.Ports.Host.WaitReachable(startupContext); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "HTTP_NOT_REACHABLE", Component: "http", Message: "Local HTTP host did not become reachable"}, hostAcquired, dependenciesStarted, err)
	}
	if _, err = c.Status.Transition(PhaseStartingDependencies, func(snapshot *StatusSnapshot) {
		snapshot.ListenerURL = listenerURL
		snapshot.Reasons = append([]RuntimeReason(nil), reasons...)
	}); err != nil {
		return c.Status.Snapshot(), err
	}
	if c.Ports.Dependencies != nil {
		if err = c.Ports.Dependencies.StartDependencies(startupContext); err != nil {
			reasons = append(reasons, RuntimeReason{Code: "DEPENDENCY_START_FAILED", Component: "dependencies", Message: "Optional dependencies are unavailable"})
		} else {
			dependenciesStarted = true
		}
	}
	if err = startupContext.Err(); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "STARTUP_CANCELED", Component: "runtime", Message: "Runtime startup was canceled"}, hostAcquired, dependenciesStarted, err)
	}
	if _, err = c.Status.Transition(PhaseRecovering, func(snapshot *StatusSnapshot) { snapshot.Reasons = append([]RuntimeReason(nil), reasons...) }); err != nil {
		return c.Status.Snapshot(), err
	}
	if c.Ports.Recovery != nil {
		if err = c.Ports.Recovery.Recover(startupContext); err != nil {
			reasons = append(reasons, RuntimeReason{Code: "RECOVERY_REQUIRED", Component: "recovery", Message: "Runtime recovery requires attention"})
		}
	}
	if err = startupContext.Err(); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "STARTUP_CANCELED", Component: "runtime", Message: "Runtime startup was canceled"}, hostAcquired, dependenciesStarted, err)
	}
	if _, err = c.Status.Transition(PhaseOpeningRecentProject, func(snapshot *StatusSnapshot) { snapshot.Reasons = append([]RuntimeReason(nil), reasons...) }); err != nil {
		return c.Status.Snapshot(), err
	}
	if c.Ports.Projects != nil {
		if err = c.Ports.Projects.OpenRecentProject(startupContext); err != nil {
			var degradation RecentProjectDegradation
			if errors.As(err, &degradation) && degradation.Reason.Code != "" {
				reasons = append(reasons, degradation.Reason)
			} else {
				reasons = append(reasons, RuntimeReason{Code: "RECENT_PROJECT_UNAVAILABLE", Component: "project", Message: "The recent project was not opened"})
			}
		}
	}
	degraded := len(reasons) > 0
	if c.Ports.Capabilities != nil {
		convergence, convergenceErr := c.Ports.Capabilities.ConvergeCapabilities(startupContext)
		if convergenceErr != nil {
			reasons = append(reasons, RuntimeReason{Code: "CAPABILITY_CONVERGENCE_FAILED", Component: "capabilities", Message: "Capability state could not fully converge"})
			degraded = true
		} else {
			reasons = append(reasons, convergence.Reasons...)
			observations = append(observations, convergence.Observations...)
			degraded = degraded || convergence.Degraded
		}
	}
	if err = startupContext.Err(); err != nil {
		return c.abortStartup(startupContext, RuntimeReason{Code: "STARTUP_CANCELED", Component: "runtime", Message: "Runtime startup was canceled"}, hostAcquired, dependenciesStarted, err)
	}
	if c.Ports.Browser != nil {
		if err = c.Ports.Browser.OpenBrowser(startupContext, listenerURL); err != nil {
			reasons = append(reasons, RuntimeReason{Code: "BROWSER_LAUNCH_FAILED", Component: "browser", Message: "The browser could not be opened; use the local URL"})
			degraded = true
		}
	}
	finalPhase := PhaseReady
	if degraded {
		finalPhase = PhaseDegraded
	}
	return c.Status.Transition(finalPhase, func(snapshot *StatusSnapshot) {
		snapshot.ListenerURL = listenerURL
		snapshot.Reasons = append([]RuntimeReason(nil), reasons...)
		snapshot.Observations = append([]RuntimeObservation(nil), observations...)
	})
}

func (c *Coordinator) abortStartup(ctx context.Context, reason RuntimeReason, hostStarted, dependenciesStarted bool, cause error) (StatusSnapshot, error) {
	_, _ = c.Status.Transition(PhaseStopping, func(snapshot *StatusSnapshot) { snapshot.Reasons = append(snapshot.Reasons, reason) })
	cleanupContext := context.WithoutCancel(ctx)
	if dependenciesStarted && c.Ports.Dependencies != nil {
		_ = c.Ports.Dependencies.StopDependencies(cleanupContext)
	}
	if hostStarted {
		_ = c.Ports.Host.StopHost(cleanupContext)
	}
	final, _ := c.Status.Transition(PhaseStopped, nil)
	return final, cause
}

func firstError(value, fallback error) error {
	if value != nil {
		return value
	}
	return fallback
}
