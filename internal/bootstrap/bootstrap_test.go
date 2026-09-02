package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	ecoguardian "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/app/runtime"
	"github.com/zouyi/eco-guardian/internal/app/runtime/config"
	runtimediagnostics "github.com/zouyi/eco-guardian/internal/app/runtime/diagnostics"
	runtimerecovery "github.com/zouyi/eco-guardian/internal/app/runtime/recovery"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/packageinfo"
	"github.com/zouyi/eco-guardian/internal/platform/appdir"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type pathSelector string

func (s pathSelector) SelectDirectory(context.Context) (string, error) { return string(s), nil }

type orderedWorker struct {
	host     *httpapi.Runtime
	lockPath string
	mu       sync.Mutex
	calls    []string
}

func (w *orderedWorker) Start(ctx context.Context) error {
	response, err := http.Get(w.host.URL() + "/health")
	if err != nil {
		return errors.New("HTTP host was not reachable before worker startup")
	}
	_ = response.Body.Close()
	w.mu.Lock()
	w.calls = append(w.calls, "start")
	w.mu.Unlock()
	return ctx.Err()
}

func (w *orderedWorker) Close(context.Context) error {
	client := http.Client{Timeout: 200 * time.Millisecond}
	if response, err := client.Get(w.host.URL() + "/health"); err == nil {
		_ = response.Body.Close()
		return errors.New("HTTP host still accepted work while worker stopped")
	}
	if _, err := os.Stat(w.lockPath); err != nil {
		return errors.New("project store closed before worker")
	}
	w.mu.Lock()
	w.calls = append(w.calls, "close")
	w.mu.Unlock()
	return nil
}

type snapshotCollector struct {
	mu     sync.Mutex
	phases []runtime.Phase
}

type failingBrowser struct{ unsafeError string }

func (b failingBrowser) OpenBrowser(_ context.Context, url string) error {
	response, err := http.Get(url + "/health")
	if err != nil {
		return errors.New("browser was invoked before HTTP became reachable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("browser readiness request failed")
	}
	return errors.New(b.unsafeError)
}

type shutdownBoundaryFunc func(context.Context) error

func (f shutdownBoundaryFunc) PrepareShutdown(ctx context.Context) error { return f(ctx) }

type blockingCloseWorker struct{ release <-chan struct{} }

func (blockingCloseWorker) Start(context.Context) error { return nil }
func (w blockingCloseWorker) Close(context.Context) error {
	<-w.release
	return nil
}

type degradedPackagePort struct{ err error }

func (p degradedPackagePort) VerifyPackage(context.Context) error { return p.err }
func (degradedPackagePort) AllowBundledExecution() bool           { return false }

type countingLifecycle struct{ starts, closes int }

func (l *countingLifecycle) Start(context.Context) error { l.starts++; return nil }
func (l *countingLifecycle) Close(context.Context) error { l.closes++; return nil }

func (c *snapshotCollector) Observe(snapshot runtime.StatusSnapshot) {
	c.mu.Lock()
	c.phases = append(c.phases, snapshot.Phase)
	c.mu.Unlock()
}

func TestCompositionStartsAppliedServicesAndClosesInOwnershipOrder(t *testing.T) {
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "project")
	if err = os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	worker := &orderedWorker{lockPath: filepath.Join(projectPath, ".eco-guardian.lock")}
	collector := &snapshotCollector{}
	var output bytes.Buffer
	process, err := Build(BuildOptions{
		Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{}, Paths: paths,
		Workers: []Worker{worker}, Observers: []StatusObserver{collector}, Stdout: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := process.Coordinator.Ports.Recovery.(stagedStartupRecovery); !ok {
		t.Fatalf("restore recovery is not owned by the single staged startup coordinator: %T", process.Coordinator.Ports.Recovery)
	}
	worker.host = process.Host
	token, _, err := process.Projects.IssueSelection(context.Background(), pathSelector(projectPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = process.Projects.Create(context.Background(), token); err != nil {
		t.Fatal(err)
	}

	snapshot, err := process.Start(context.Background())
	if err != nil || snapshot.Phase != runtime.PhaseDegraded || snapshot.ListenerURL == "" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if strings.TrimSpace(output.String()) != snapshot.ListenerURL {
		t.Fatalf("stdout=%q url=%q", output.String(), snapshot.ListenerURL)
	}
	assertEmbeddedSameOrigin(t, snapshot.ListenerURL)
	assertRuntimeCapabilitiesComposed(t, snapshot.ListenerURL)
	routes := map[string]bool{}
	for _, route := range process.Host.Engine().Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/settings", "POST /api/v1/projects", "POST /api/v1/revisions",
		"POST /api/v1/simulation-jobs", "POST /api/v1/ai-design-jobs", "GET /api/v1/runtime/status",
		"GET /api/v1/backups", "POST /api/v1/backups", "POST /api/v1/restore-preflights", "POST /api/v1/restores",
	} {
		if !routes[route] {
			t.Fatalf("composition omitted route %s", route)
		}
	}

	if err = process.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = process.Close(context.Background()); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	worker.mu.Lock()
	calls := append([]string(nil), worker.calls...)
	worker.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"start", "close"}) {
		t.Fatalf("worker calls=%v", calls)
	}
	if _, err = os.Stat(worker.lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project lock remains after close: %v", err)
	}
	collector.mu.Lock()
	phases := append([]runtime.Phase(nil), collector.phases...)
	collector.mu.Unlock()
	if len(phases) == 0 || phases[len(phases)-1] != runtime.PhaseStopped {
		t.Fatalf("observer phases=%v", phases)
	}
	logBody, err := os.ReadFile(filepath.Join(paths.Logs, runtimediagnostics.Filename))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBody)
	for _, eventName := range []string{"package_verification", "startup_phase", "listener_selected", "http_request", "process_lifecycle"} {
		if !strings.Contains(logText, `"event_name":"`+eventName+`"`) {
			t.Fatalf("runtime log omitted %s: %s", eventName, logText)
		}
	}
	if strings.Contains(logText, projectPath) {
		t.Fatalf("runtime log leaked project path: %s", logText)
	}
}

func assertRuntimeCapabilitiesComposed(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Get(baseURL + "/api/v1/runtime/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("runtime capabilities status=%d body=%q err=%v", response.StatusCode, body, readErr)
	}
	text := string(body)
	if !strings.Contains(text, `"release":{"disabled_reasons":[],"enabled":true}`) {
		t.Fatalf("release Gate registry was not composed: %s", text)
	}
	if !strings.Contains(text, `"reasons":["AI_DISABLED"]`) || strings.Contains(text, "AI_PROVIDER_UNCONFIGURED") {
		t.Fatalf("AI capability did not use live settings: %s", text)
	}

	statusResponse, err := http.Get(baseURL + "/api/v1/runtime/status")
	if err != nil {
		t.Fatal(err)
	}
	statusBody, readErr := io.ReadAll(statusResponse.Body)
	_ = statusResponse.Body.Close()
	if readErr != nil || statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("runtime status=%d body=%q err=%v", statusResponse.StatusCode, statusBody, readErr)
	}
	if bytes.Contains(statusBody, []byte("RELEASE_GATE_UNREGISTERED")) || bytes.Contains(statusBody, []byte("RELEASE_GATE_REGISTRY_UNAVAILABLE")) {
		t.Fatalf("runtime status retained synthetic release failure: %s", statusBody)
	}
}

func assertEmbeddedSameOrigin(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	index, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(index, []byte(`<div id="app"></div>`)) {
		t.Fatalf("index status=%d body=%q err=%v", response.StatusCode, index, err)
	}
	match := regexp.MustCompile(`src="([^"]+\.js)"`).FindSubmatch(index)
	if len(match) != 2 {
		t.Fatalf("index has no script asset: %s", index)
	}
	assetResponse, err := http.Get(baseURL + string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	assetPrefix := make([]byte, 32)
	read, readErr := assetResponse.Body.Read(assetPrefix)
	_ = assetResponse.Body.Close()
	if readErr != nil || assetResponse.StatusCode != http.StatusOK || bytes.Contains(assetPrefix[:read], []byte("<!doctype html>")) {
		t.Fatalf("asset status=%d prefix=%q err=%v", assetResponse.StatusCode, assetPrefix[:read], readErr)
	}
	settingsResponse, err := http.Get(baseURL + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	_ = settingsResponse.Body.Close()
	if settingsResponse.StatusCode != http.StatusOK {
		t.Fatalf("same-origin API status=%d", settingsResponse.StatusCode)
	}
	missingAPI, err := http.Get(baseURL + "/api/v1/not-found")
	if err != nil {
		t.Fatal(err)
	}
	_ = missingAPI.Body.Close()
	if missingAPI.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API fell through to UI: %d", missingAPI.StatusCode)
	}
}

func TestStartupRejectsMissingOrCorruptEmbeddedAssetsBeforeBinding(t *testing.T) {
	tests := map[string]func(fstest.MapFS){
		"missing": func(assets fstest.MapFS) { delete(assets, "web/dist/index.html") },
		"corrupt": func(assets fstest.MapFS) {
			assets["api/openapi.yaml"] = &fstest.MapFile{Data: []byte("not the compiled contract")}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			assets := cloneEmbeddedAssets(t)
			mutate(assets)
			paths, err := appdir.ResolveWindows(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			process, err := Build(BuildOptions{Paths: paths, Assets: assets, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = process.Close(context.Background()) })
			snapshot, startErr := process.Start(context.Background())
			if startErr == nil || snapshot.Phase != runtime.PhaseStopped || process.Host.Address() != "" {
				t.Fatalf("snapshot=%#v address=%q err=%v", snapshot, process.Host.Address(), startErr)
			}
			if len(snapshot.Reasons) != 1 || snapshot.Reasons[0].Code != "PACKAGE_VERIFICATION_FAILED" {
				t.Fatalf("reasons=%v", snapshot.Reasons)
			}
		})
	}
}

func TestBrowserFailureKeepsExactConsoleURLAndEmitsOnlySafeDiagnostic(t *testing.T) {
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const unsafeCanary = "browser failure at /Users/private/canary"
	var console, diagnostics bytes.Buffer
	process, err := Build(BuildOptions{
		Paths: paths, Environment: config.EnvironmentMap{}, Browser: failingBrowser{unsafeError: unsafeCanary},
		Stdout: &console, Diagnostics: &diagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close(context.Background()) })
	snapshot, err := process.Start(context.Background())
	if err != nil || snapshot.Phase != runtime.PhaseDegraded || snapshot.ListenerURL == "" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if strings.TrimSpace(console.String()) != snapshot.ListenerURL {
		t.Fatalf("console=%q url=%q", console.String(), snapshot.ListenerURL)
	}
	if diagnostics.String() != "{\"event\":\"browser_launch_failed\",\"code\":\"BROWSER_LAUNCH_FAILED\"}\n" || strings.Contains(diagnostics.String(), unsafeCanary) {
		t.Fatalf("diagnostics=%q", diagnostics.String())
	}
	response, err := http.Get(snapshot.ListenerURL + "/health")
	if err != nil {
		t.Fatalf("browser failure stopped host: %v", err)
	}
	_ = response.Body.Close()
}

func TestRecentProjectReopenUsesManagerAndPreservesUnsafeProjects(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		version    int
		lock       bool
		wantPhase  runtime.Phase
		wantReason string
	}{
		{name: "healthy", version: store.DBSchemaVersion(), wantPhase: runtime.PhaseDegraded},
		{name: "locked", version: store.DBSchemaVersion(), lock: true, wantPhase: runtime.PhaseDegraded, wantReason: "RECENT_PROJECT_LOCKED"},
		{name: "newer schema", version: store.DBSchemaVersion() + 1, wantPhase: runtime.PhaseDegraded, wantReason: "RECENT_PROJECT_SCHEMA_NEWER"},
		{name: "migration recovery required", version: store.DBSchemaVersion() - 1, wantPhase: runtime.PhaseDegraded, wantReason: "RECENT_PROJECT_RECOVERY_REQUIRED"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			paths, err := appdir.ResolveWindows(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projectPath := filepath.Join(t.TempDir(), "recent-project")
			registry, err := domain.NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			projectStore, projectID, err := store.Create(context.Background(), projectPath, registry)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = projectStore.Create(context.Background(), domain.KindTag, domain.EntityDraft{
				Key: "recent", Name: "recent", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = projectStore.Close(); err != nil {
				t.Fatal(err)
			}
			if testCase.version != store.DBSchemaVersion() {
				database, openErr := sql.Open("sqlite", filepath.Join(projectPath, "project.db"))
				if openErr != nil {
					t.Fatal(openErr)
				}
				if _, openErr = database.Exec("UPDATE project_meta SET db_schema_version=?", testCase.version); openErr != nil {
					_ = database.Close()
					t.Fatal(openErr)
				}
				if openErr = database.Close(); openErr != nil {
					t.Fatal(openErr)
				}
			}
			if err = project.NewFileRecentProjects(filepath.Dir(paths.Root)).Record(project.ProjectInfo{ID: projectID, Name: "recent-project", Path: projectPath}); err != nil {
				t.Fatal(err)
			}
			var held project.Lock
			if testCase.lock {
				held, err = (project.FileLocker{}).Acquire(projectPath)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Release()
			}
			before := directoryDigests(t, projectPath)

			process, err := Build(BuildOptions{Paths: paths, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = process.Close(context.Background()) })
			snapshot, err := process.Start(context.Background())
			if err != nil || snapshot.Phase != testCase.wantPhase {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			_, active := process.Projects.Current()
			if testCase.wantReason == "" {
				if !active {
					t.Fatal("healthy recent project was not opened through the manager")
				}
				return
			}
			if active || !hasRuntimeReason(snapshot.Reasons, testCase.wantReason) {
				t.Fatalf("active=%v reasons=%v", active, snapshot.Reasons)
			}
			after := directoryDigests(t, projectPath)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("unsafe recent project changed: before=%v after=%v", before, after)
			}
			response, requestErr := http.Get(snapshot.ListenerURL + "/")
			if requestErr != nil {
				t.Fatalf("selection UI unavailable: %v", requestErr)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("selection UI status=%d", response.StatusCode)
			}
		})
	}
}

func TestShutdownRunsSafePointsWorkersDependenciesAndStoresInOrder(t *testing.T) {
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "ordered-shutdown")
	if err = os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(projectPath, ".eco-guardian.lock")
	var mu sync.Mutex
	var calls []string
	appendCall := func(value string) {
		mu.Lock()
		calls = append(calls, value)
		mu.Unlock()
	}
	assertProjectOpen := func() error {
		if _, statErr := os.Stat(lockPath); statErr != nil {
			return errors.New("project store closed too early")
		}
		return nil
	}
	worker := lifecycleFunc{
		start: func(context.Context) error { appendCall("start:worker"); return nil },
		close: func(context.Context) error {
			if err := assertProjectOpen(); err != nil {
				return err
			}
			appendCall("close:worker")
			return nil
		},
	}
	dependency := lifecycleFunc{
		start: func(context.Context) error { appendCall("start:dependency"); return nil },
		close: func(context.Context) error {
			if err := assertProjectOpen(); err != nil {
				return err
			}
			appendCall("close:dependency")
			return nil
		},
	}
	var process *Process
	boundary := shutdownBoundaryFunc(func(context.Context) error {
		client := http.Client{Timeout: 200 * time.Millisecond}
		if response, requestErr := client.Get(process.Host.URL() + "/health"); requestErr == nil {
			_ = response.Body.Close()
			return errors.New("HTTP admission remained open at Job safe point")
		}
		if err := assertProjectOpen(); err != nil {
			return err
		}
		appendCall("safe-point")
		return nil
	})
	shutdownOperation := func(name string) runtimerecovery.ShutdownOperation {
		return func(context.Context) error {
			if err := assertProjectOpen(); err != nil {
				return err
			}
			appendCall(name)
			return nil
		}
	}
	policy := runtimerecovery.ShutdownPolicy{Kind: "test", StopClaims: shutdownOperation("stop-claims"), PersistIntent: shutdownOperation("persist-intent"), WaitSafeBoundary: shutdownOperation("wait-safe"), ProveRecoverable: shutdownOperation("prove-recoverable")}
	process, err = Build(BuildOptions{
		Paths: paths, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{},
		Workers: []Worker{worker}, Dependencies: []Worker{dependency}, ShutdownBoundaries: []ShutdownBoundary{boundary}, ShutdownPolicies: []runtimerecovery.ShutdownPolicy{policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := process.Projects.IssueSelection(context.Background(), pathSelector(projectPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = process.Projects.Create(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err = process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = process.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{"start:dependency", "start:worker", "stop-claims", "persist-intent", "wait-safe", "prove-recoverable", "safe-point", "close:worker", "close:dependency"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown calls=%v want=%v", got, want)
	}
	if _, err = os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project store was not closed last: %v", err)
	}
}

func TestShutdownIsBoundedAndRepeatedCloseReturnsSameOutcome(t *testing.T) {
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	process, err := Build(BuildOptions{
		Paths: paths, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{},
		Workers: []Worker{blockingCloseWorker{release: release}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	process.shutdownWindow = 50 * time.Millisecond
	started := time.Now()
	first := process.Close(context.Background())
	if !errors.Is(first, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("first close duration=%v err=%v", time.Since(started), first)
	}
	started = time.Now()
	second := process.Close(context.Background())
	if second == nil || second.Error() != first.Error() || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("second close duration=%v first=%v second=%v", time.Since(started), first, second)
	}
	close(release)
}

func TestOptionalGraphStartupFailureKeepsOfflineHostServing(t *testing.T) {
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	graph := lifecycleFunc{
		start: func(context.Context) error { return errors.New("graph unavailable") },
		close: func(context.Context) error { return nil },
	}
	process, err := Build(BuildOptions{
		Paths: paths, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{},
		Dependencies: []Worker{graph},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Close(context.Background()) })
	snapshot, err := process.Start(context.Background())
	if err != nil || snapshot.Phase != runtime.PhaseDegraded || !hasRuntimeReason(snapshot.Reasons, "DEPENDENCY_START_FAILED") {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	assertEmbeddedSameOrigin(t, snapshot.ListenerURL)
}

func TestMissingCompleteComponentsDegradePreciselyWithoutExecutingThem(t *testing.T) {
	for _, testCase := range []struct {
		kind     packageinfo.ComponentKind
		code     string
		affected string
	}{
		{packageinfo.ComponentLocalRAG, "PACKAGE_LOCAL_RAG_MISSING", "GRAPH_CORE_UNAVAILABLE"},
		{packageinfo.ComponentPythonRuntime, "PACKAGE_PYTHON_MISSING", "BUNDLED_PROCESS_UNAVAILABLE"},
		{packageinfo.ComponentEmbeddingModel, "PACKAGE_EMBEDDING_MODEL_MISSING", "VECTOR_RETRIEVAL_UNAVAILABLE"},
		{packageinfo.ComponentRerankModel, "PACKAGE_RERANK_MODEL_MISSING", "RERANK_RETRIEVAL_UNAVAILABLE"},
	} {
		t.Run(string(testCase.kind), func(t *testing.T) {
			paths, err := appdir.ResolveWindows(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			degradation := classifyPackageVerification(packageinfo.DiagnosticError{Code: packageinfo.CodeComponentMissing, Component: testCase.kind})
			owned, worker := &countingLifecycle{}, &countingLifecycle{}
			process, err := Build(BuildOptions{
				Paths: paths, Args: []string{"--browser-auto-open", "false"}, Environment: config.EnvironmentMap{},
				PackageVerifier: degradedPackagePort{err: degradation}, Dependencies: []Worker{owned}, Workers: []Worker{worker},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = process.Close(context.Background()) })
			snapshot, err := process.Start(context.Background())
			if err != nil || snapshot.Phase != runtime.PhaseDegraded || len(snapshot.Reasons) < 2 || snapshot.Reasons[0].Code != testCase.code || !hasRuntimeReason(snapshot.Reasons, testCase.affected) {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			if owned.starts != 0 || worker.starts != 1 {
				t.Fatalf("unverified owned component starts=%d offline worker starts=%d", owned.starts, worker.starts)
			}
			assertEmbeddedSameOrigin(t, snapshot.ListenerURL)
		})
	}
}

func TestPackageFailureClassifierPreservesHardErrorsAndCorruptCategory(t *testing.T) {
	hard := packageinfo.DiagnosticError{Code: packageinfo.CodePathEscape, Component: packageinfo.ComponentLocalRAG}
	if got := classifyPackageVerification(hard); got.Error() != hard.Error() {
		t.Fatalf("hard error changed: %v", got)
	}
	classified := classifyPackageVerification(packageinfo.DiagnosticError{Code: packageinfo.CodeComponentCorrupt, Component: packageinfo.ComponentEmbeddingModel})
	var degradation runtime.StartupDegradations
	if !errors.As(classified, &degradation) || len(degradation.Reasons) != 2 || degradation.Reasons[0].Code != "PACKAGE_EMBEDDING_MODEL_CORRUPT" || degradation.Reasons[0].Message == "" || degradation.Reasons[1].Code != "VECTOR_RETRIEVAL_UNAVAILABLE" {
		t.Fatalf("classified=%#v err=%v", degradation, classified)
	}
}

func hasRuntimeReason(reasons []runtime.RuntimeReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func directoryDigests(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[relative] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func cloneEmbeddedAssets(t *testing.T) fstest.MapFS {
	t.Helper()
	result := fstest.MapFS{}
	err := fs.WalkDir(ecoguardian.Assets, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, err := fs.ReadFile(ecoguardian.Assets, name)
		if err != nil {
			return err
		}
		result[name] = &fstest.MapFile{Data: contents, Mode: 0o444}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWorkerGroupRollsBackAndStopsInReverseOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	first := lifecycleFunc{
		start: func(context.Context) error { mu.Lock(); calls = append(calls, "start:first"); mu.Unlock(); return nil },
		close: func(context.Context) error { mu.Lock(); calls = append(calls, "close:first"); mu.Unlock(); return nil },
	}
	second := lifecycleFunc{
		start: func(context.Context) error {
			mu.Lock()
			calls = append(calls, "start:second")
			mu.Unlock()
			return errors.New("failed")
		},
		close: func(context.Context) error { mu.Lock(); calls = append(calls, "close:second"); mu.Unlock(); return nil },
	}
	group := &workerGroup{values: []Worker{first, second}}
	if err := group.StartDependencies(context.Background()); err == nil {
		t.Fatal("expected worker start failure")
	}
	if !reflect.DeepEqual(calls, []string{"start:first", "start:second", "close:first"}) {
		t.Fatalf("rollback calls=%v", calls)
	}
}

type lifecycleFunc struct {
	start func(context.Context) error
	close func(context.Context) error
}

func (f lifecycleFunc) Start(ctx context.Context) error { return f.start(ctx) }
func (f lifecycleFunc) Close(ctx context.Context) error { return f.close(ctx) }

func TestMainRemainsAThinBootstrapEntrypoint(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "eco-guardian", "main.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		value := strings.Trim(imported.Path.Value, `"`)
		if strings.Contains(value, "/internal/") && value != "github.com/zouyi/eco-guardian/internal/bootstrap" {
			t.Fatalf("main imports concrete application module %q", value)
		}
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name != "main" {
			t.Fatalf("main package contains composition helper %q", function.Name.Name)
		}
	}
}

func TestRunHelpDoesNotResolvePlatformOrStartRuntime(t *testing.T) {
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--preferred-port") || !strings.Contains(output.String(), "command line > environment") {
		t.Fatalf("help=%q", output.String())
	}
}

func TestBackupRuntimeCloseGuardBlocksOnlyNonterminalBackupRestoreJobs(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	opened, projectID, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	runtime := &backupRuntime{activeStore: opened}
	hash := strings.Repeat("a", 64)
	job, _, err := opened.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: projectID, Kind: "backup", InputHash: hash, IdempotencyKey: "close-guard", RequestHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Preflight(context.Background()); !errors.Is(err, project.ErrCloseBlocked) {
		t.Fatalf("queued backup did not block close: %v", err)
	}
	job, _, err = opened.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Failed, nil, 0)
	if err != nil || job.Status != sharedjob.Failed {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	if err = runtime.Preflight(context.Background()); err != nil {
		t.Fatalf("terminal backup blocked close: %v", err)
	}
	restoreJob, _, err := opened.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: projectID, Kind: application.RestoreJobKind, InputHash: hash, IdempotencyKey: "restore-close-guard", RequestHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Preflight(context.Background()); !errors.Is(err, project.ErrCloseBlocked) {
		t.Fatalf("uncoordinated restore did not block close: %v", err)
	}
	coordinated := project.WithMaintenanceCoordinator(context.Background(), restoreJob.ID)
	if err = runtime.Preflight(coordinated); err != nil {
		t.Fatalf("restore coordinator blocked its own maintenance: %v", err)
	}
}

func TestBackupRuntimeRollbackDisablesNewCommandsAndPreservesPublishedArtifacts(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	opened, projectID, err := store.Create(ctx, t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	artifacts, err := backupfs.NewStore(filepath.Clean(t.TempDir()), store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store.BackupSource{Store: opened, AppVersion: "test"}, artifacts, store.BackupVerifier{}, opened, opened, backupfs.Probe{})
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: projectID, Purpose: backupdomain.Manual, ManualReason: "rollback evidence", Source: backupdomain.SourceIdentity{}}
	job, _, err := service.Submit(ctx, command, "rollback-evidence")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteStored(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &backupRuntime{activeStore: opened}
	if err = runtime.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	service.CommandGate = runtime.commandGate
	if _, _, err = service.Submit(ctx, command, "rollback-new-command"); !errors.Is(err, application.ErrFeatureDisabled) {
		t.Fatalf("new command err=%v", err)
	}
	page, err := artifacts.List(ctx, ports.InventoryQuery{ProjectID: projectID, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].BackupID != result.BackupID {
		t.Fatalf("published artifacts=%#v err=%v", page.Items, err)
	}
}
