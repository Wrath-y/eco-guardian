package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type startupFake struct {
	calls       []string
	fail        map[string]error
	convergence CapabilityConvergence
}

func (f *startupFake) call(ctx context.Context, name string) error {
	f.calls = append(f.calls, name)
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.fail[name]
}

func (f *startupFake) LoadSettings(ctx context.Context) error  { return f.call(ctx, "settings") }
func (f *startupFake) VerifyPackage(ctx context.Context) error { return f.call(ctx, "package") }
func (f *startupFake) AcquireListener(ctx context.Context) (string, error) {
	return "http://127.0.0.1:31888", f.call(ctx, "acquire_listener")
}
func (f *startupFake) StartHost(ctx context.Context) error     { return f.call(ctx, "start_http") }
func (f *startupFake) WaitReachable(ctx context.Context) error { return f.call(ctx, "wait_reachable") }
func (f *startupFake) StopHost(ctx context.Context) error      { return f.call(ctx, "stop_http") }
func (f *startupFake) StartDependencies(ctx context.Context) error {
	return f.call(ctx, "start_dependencies")
}
func (f *startupFake) StopDependencies(ctx context.Context) error {
	return f.call(ctx, "stop_dependencies")
}
func (f *startupFake) Recover(ctx context.Context) error { return f.call(ctx, "recover") }
func (f *startupFake) OpenRecentProject(ctx context.Context) error {
	return f.call(ctx, "open_recent_project")
}
func (f *startupFake) ConvergeCapabilities(ctx context.Context) (CapabilityConvergence, error) {
	return f.convergence, f.call(ctx, "converge_capabilities")
}
func (f *startupFake) OpenBrowser(ctx context.Context, url string) error {
	if url != "http://127.0.0.1:31888" {
		return errors.New("wrong URL")
	}
	return f.call(ctx, "open_browser")
}

func testCoordinator(t *testing.T, fake *startupFake) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(NewStatusStore(nil), LifecyclePorts{
		Settings: fake, Package: fake, Host: fake, Dependencies: fake,
		Recovery: fake, Projects: fake, Capabilities: fake, Browser: fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func TestCoordinatorRunsStartupInRequiredOrderAndPublishesReady(t *testing.T) {
	fake := &startupFake{fail: map[string]error{}, convergence: CapabilityConvergence{Observations: []RuntimeObservation{{ID: "graph", State: "available", Generation: 1, ObservedAt: time.Now().UTC()}}}}
	coordinator := testCoordinator(t, fake)
	result, err := coordinator.Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseReady || result.Generation != 6 || result.ListenerURL != "http://127.0.0.1:31888" || len(result.Observations) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	want := []string{"settings", "package", "acquire_listener", "start_http", "wait_reachable", "start_dependencies", "recover", "open_recent_project", "converge_capabilities", "open_browser"}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("calls=%v want=%v", fake.calls, want)
	}
	if _, err := coordinator.Start(context.Background(), time.Second); !errors.Is(err, ErrRuntimeAlreadyStarted) {
		t.Fatalf("second start err=%v", err)
	}
}

func TestCoordinatorKeepsOfflineHostWhenOptionalDependencyFails(t *testing.T) {
	fake := &startupFake{fail: map[string]error{"start_dependencies": errors.New("graph unavailable")}}
	result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseDegraded || result.ListenerURL == "" || len(result.Reasons) != 1 || result.Reasons[0].Code != "DEPENDENCY_START_FAILED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !containsCall(fake.calls, "open_recent_project") || !containsCall(fake.calls, "open_browser") || containsCall(fake.calls, "stop_http") {
		t.Fatalf("optional failure stopped offline host: %v", fake.calls)
	}
}

func TestCoordinatorContinuesAfterClassifiedPackageDegradation(t *testing.T) {
	fake := &startupFake{fail: map[string]error{"package": StartupDegradation{Reason: RuntimeReason{Code: "MODEL_MISSING", Component: "package", Message: "Embedding model is missing"}}}}
	result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseDegraded || result.Reasons[0].Code != "MODEL_MISSING" || !containsCall(fake.calls, "start_http") {
		t.Fatalf("result=%#v calls=%v err=%v", result, fake.calls, err)
	}
}

func TestCoordinatorKeepsReachableHostWhenBrowserLaunchFails(t *testing.T) {
	fake := &startupFake{fail: map[string]error{"open_browser": errors.New("browser unavailable")}}
	result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseDegraded || len(result.Reasons) != 1 || result.Reasons[0].Code != "BROWSER_LAUNCH_FAILED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !containsCall(fake.calls, "wait_reachable") || containsCall(fake.calls, "stop_http") {
		t.Fatalf("browser failure affected host: %v", fake.calls)
	}
}

func TestCoordinatorPublishesSafeRecentProjectClassification(t *testing.T) {
	fake := &startupFake{fail: map[string]error{
		"open_recent_project": RecentProjectDegradation{Reason: RuntimeReason{Code: "RECENT_PROJECT_LOCKED", Component: "project", Message: "The recent project was not opened"}},
	}}
	result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseDegraded || len(result.Reasons) != 1 || result.Reasons[0].Code != "RECENT_PROJECT_LOCKED" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCoordinatorPropagatesDeadlineAndCleansStartedHost(t *testing.T) {
	fake := &startupFake{fail: map[string]error{"wait_reachable": context.DeadlineExceeded}}
	coordinator := testCoordinator(t, fake)
	result, err := coordinator.Start(context.Background(), time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || result.Phase != PhaseStopped || !containsCall(fake.calls, "stop_http") {
		t.Fatalf("result=%#v calls=%v err=%v", result, fake.calls, err)
	}
}

func TestCoordinatorClassifiesEveryStartupPhaseFailure(t *testing.T) {
	for _, testCase := range []struct {
		stage              string
		wantPhase          Phase
		wantCode           string
		wantError          bool
		wantHostStop       bool
		wantDependencyStop bool
	}{
		{stage: "settings", wantPhase: PhaseStopped, wantCode: "SETTINGS_LOAD_FAILED", wantError: true},
		{stage: "package", wantPhase: PhaseStopped, wantCode: "PACKAGE_VERIFICATION_FAILED", wantError: true},
		{stage: "acquire_listener", wantPhase: PhaseStopped, wantCode: "LISTENER_ACQUISITION_FAILED", wantError: true},
		{stage: "start_http", wantPhase: PhaseStopped, wantCode: "HTTP_START_FAILED", wantError: true, wantHostStop: true},
		{stage: "wait_reachable", wantPhase: PhaseStopped, wantCode: "HTTP_NOT_REACHABLE", wantError: true, wantHostStop: true},
		{stage: "start_dependencies", wantPhase: PhaseDegraded, wantCode: "DEPENDENCY_START_FAILED"},
		{stage: "recover", wantPhase: PhaseDegraded, wantCode: "RECOVERY_REQUIRED"},
		{stage: "open_recent_project", wantPhase: PhaseDegraded, wantCode: "RECENT_PROJECT_UNAVAILABLE"},
		{stage: "converge_capabilities", wantPhase: PhaseDegraded, wantCode: "CAPABILITY_CONVERGENCE_FAILED"},
		{stage: "open_browser", wantPhase: PhaseDegraded, wantCode: "BROWSER_LAUNCH_FAILED"},
	} {
		t.Run(testCase.stage, func(t *testing.T) {
			fake := &startupFake{fail: map[string]error{testCase.stage: errors.New("injected failure")}}
			result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
			if (err != nil) != testCase.wantError || result.Phase != testCase.wantPhase || len(result.Reasons) != 1 || result.Reasons[0].Code != testCase.wantCode {
				t.Fatalf("result=%#v calls=%v err=%v", result, fake.calls, err)
			}
			if containsCall(fake.calls, "stop_http") != testCase.wantHostStop {
				t.Fatalf("stop host mismatch calls=%v", fake.calls)
			}
			if containsCall(fake.calls, "stop_dependencies") != testCase.wantDependencyStop {
				t.Fatalf("stop dependencies mismatch calls=%v", fake.calls)
			}
		})
	}
}

func TestCoordinatorPublishesPartialDependencyReadiness(t *testing.T) {
	now := time.Now().UTC()
	fake := &startupFake{fail: map[string]error{}, convergence: CapabilityConvergence{
		Degraded: true,
		Reasons:  []RuntimeReason{{Code: "VECTOR_UNAVAILABLE", Component: "graph", Message: "Vector retrieval is unavailable"}},
		Observations: []RuntimeObservation{
			{ID: "graph-core", State: "available", Generation: 3, ObservedAt: now},
			{ID: "vector", State: "unavailable", Generation: 3, ObservedAt: now},
		},
	}}
	result, err := testCoordinator(t, fake).Start(context.Background(), time.Second)
	if err != nil || result.Phase != PhaseDegraded || len(result.Reasons) != 1 || len(result.Observations) != 2 || result.Observations[0].State != "available" || containsCall(fake.calls, "stop_http") {
		t.Fatalf("result=%#v calls=%v err=%v", result, fake.calls, err)
	}
}

func containsCall(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
