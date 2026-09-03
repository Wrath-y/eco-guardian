package project

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeLock struct{ released bool }

func (l *fakeLock) Release() error { l.released = true; return nil }

type fakeLocker struct {
	lock *fakeLock
	err  error
}

func (l fakeLocker) Acquire(string) (Lock, error) { return l.lock, l.err }

type fakeHandle struct {
	id     domain.ID
	closed bool
}

func (h *fakeHandle) Close() error  { h.closed = true; return nil }
func (h *fakeHandle) ID() domain.ID { return h.id }

type fakeFactory struct {
	handle *fakeHandle
	err    error
}
type blockingGuard struct{}

func (blockingGuard) Preflight(context.Context) error { return ErrCloseBlocked }

type fakeJob struct {
	mode                   CloseJobMode
	cancelled, interrupted bool
}

func (j *fakeJob) Mode() CloseJobMode                    { return j.mode }
func (j *fakeJob) Name() string                          { return "job" }
func (j *fakeJob) CancelAndWait(context.Context) error   { j.cancelled = true; return nil }
func (j *fakeJob) MarkInterrupted(context.Context) error { j.interrupted = true; return nil }

func (f fakeFactory) Create(context.Context, string) (ProjectHandle, error) { return f.handle, f.err }
func (f fakeFactory) Open(context.Context, string) (ProjectHandle, error)   { return f.handle, f.err }
func TestTokenIsOpaqueOneTimeAndExpires(t *testing.T) {
	clock := fakeClock{time.Now()}
	s := NewTokenStore(time.Minute, clock)
	token, _, err := s.Issue("relative")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(token); !errors.Is(err, ErrInvalidSelection) {
		t.Fatal(err)
	}
	expired := NewTokenStore(0, clock)
	token, _, _ = expired.Issue("x")
	if _, err := expired.Consume(token); !errors.Is(err, ErrInvalidSelection) {
		t.Fatal(err)
	}
}
func TestManagerAcquiresCreatesAndClosesInOrder(t *testing.T) {
	id, _ := domain.NewID()
	lock := &fakeLock{}
	handle := &fakeHandle{id: id}
	tokens := NewTokenStore(time.Hour, nil)
	token, _, _ := tokens.Issue(t.TempDir())
	m := NewManager(tokens, fakeLocker{lock: lock}, fakeFactory{handle: handle}, NoJobs{}, nil)
	info, err := m.Create(context.Background(), token)
	if err != nil || info.ID != id {
		t.Fatalf("create=%#v %v", info, err)
	}
	if _, err := m.Open(context.Background(), token); !errors.Is(err, ErrActiveProject) {
		t.Fatal(err)
	}
	if err := m.Close(context.Background()); err != nil || !handle.closed || !lock.released {
		t.Fatalf("close=%v handle=%v lock=%v", err, handle.closed, lock.released)
	}
}

func TestMaintenanceKeepsOSLockBlocksOrdinaryAccessAndReopensSameIdentity(t *testing.T) {
	id, _ := domain.NewID()
	lock := &fakeLock{}
	handle := &fakeHandle{id: id}
	tokens := NewTokenStore(time.Hour, nil)
	directory := t.TempDir()
	token, _, _ := tokens.Issue(directory)
	manager := NewManager(tokens, fakeLocker{lock: lock}, fakeFactory{handle: handle}, NoJobs{}, nil)
	if _, err := manager.Create(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	maintenance, err := manager.AcquireMaintenance(context.Background(), id)
	if err != nil || maintenance.Path() != directory {
		t.Fatalf("maintenance=%#v err=%v", maintenance, err)
	}
	if _, active := manager.ActiveHandle(); active {
		t.Fatal("maintenance exposed active handle")
	}
	if active, nonInterruptible := manager.MaintenanceState(); !active || nonInterruptible {
		t.Fatalf("maintenance projection=%v/%v", active, nonInterruptible)
	}
	if err = manager.Close(context.Background()); !errors.Is(err, ErrMaintenance) || lock.released {
		t.Fatalf("close=%v lock=%#v", err, lock)
	}
	if err = maintenance.MarkNonInterruptible(); err != nil || !maintenance.NonInterruptible() {
		t.Fatalf("non-interruptible=%v err=%v", maintenance.NonInterruptible(), err)
	}
	if active, nonInterruptible := manager.MaintenanceState(); !active || !nonInterruptible {
		t.Fatalf("non-interruptible projection=%v/%v", active, nonInterruptible)
	}
	if err = maintenance.CloseConnections(context.Background()); err != nil || !handle.closed || lock.released {
		t.Fatalf("close connections=%v handle=%#v lock=%#v", err, handle, lock)
	}
	if _, err = maintenance.Reopen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.Release(); err != nil {
		t.Fatal(err)
	}
	if _, active := manager.ActiveHandle(); !active || lock.released {
		t.Fatalf("active=%v lock=%#v", active, lock)
	}
	if active, nonInterruptible := manager.MaintenanceState(); active || nonInterruptible {
		t.Fatalf("terminal maintenance projection=%v/%v", active, nonInterruptible)
	}
}

func TestManagerFailureCleansLockAndBlockedCloseKeepsActive(t *testing.T) {
	id, _ := domain.NewID()
	lock := &fakeLock{}
	tokens := NewTokenStore(time.Hour, nil)
	token, _, _ := tokens.Issue(t.TempDir())
	m := NewManager(tokens, fakeLocker{lock: lock}, fakeFactory{err: errors.New("open failed")}, NoJobs{}, nil)
	if _, err := m.Open(context.Background(), token); err == nil || !lock.released {
		t.Fatalf("failure did not clean lock: %v %v", err, lock.released)
	}
	lock = &fakeLock{}
	handle := &fakeHandle{id: id}
	tokens = NewTokenStore(time.Hour, nil)
	token, _, _ = tokens.Issue(t.TempDir())
	m = NewManager(tokens, fakeLocker{lock: lock}, fakeFactory{handle: handle}, blockingGuard{}, nil)
	if _, err := m.Create(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(context.Background()); !errors.Is(err, ErrCloseBlocked) {
		t.Fatal(err)
	}
	if _, ok := m.Current(); !ok || handle.closed || lock.released {
		t.Fatal("blocked close changed active project")
	}
}

func TestJobGuardAndRecentProjects(t *testing.T) {
	cancel, external := &fakeJob{mode: CancelAndWait}, &fakeJob{mode: InterruptExternal}
	if err := (JobGuard{Jobs: []CloseJob{cancel, external}}).Preflight(context.Background()); err != nil || !cancel.cancelled || !external.interrupted {
		t.Fatalf("guard=%v", err)
	}
	if err := (JobGuard{Jobs: []CloseJob{&fakeJob{mode: BlockClose}}}).Preflight(context.Background()); !errors.Is(err, ErrCloseBlocked) {
		t.Fatal(err)
	}
	recent := NewFileRecentProjects(t.TempDir())
	id, _ := domain.NewID()
	if err := recent.Record(ProjectInfo{ID: id, Name: "old", Path: "/old"}); err != nil {
		t.Fatal(err)
	}
	if err := recent.Record(ProjectInfo{ID: id, Name: "moved", Path: "/moved"}); err != nil {
		t.Fatal(err)
	}
	values, err := recent.List()
	if err != nil || len(values) != 1 || values[0].Path != "/moved" {
		t.Fatalf("recent=%#v %v", values, err)
	}
}

func TestFileLockerRejectsSecondWriter(t *testing.T) {
	dir := t.TempDir()
	first, err := (FileLocker{}).Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := (FileLocker{}).Acquire(dir); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("second lock=%v", err)
	}
}

func TestFileLockerSecondProcess(t *testing.T) {
	if os.Getenv("ECO_LOCK_HELPER") == "1" {
		lock, err := FileLocker{}.Acquire(os.Args[len(os.Args)-1])
		if err != nil {
			os.Exit(2)
		}
		fmt.Println("locked")
		time.Sleep(2 * time.Second)
		_ = lock.Release()
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestFileLockerSecondProcess", dir)
	cmd.Env = append(os.Environ(), "ECO_LOCK_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatalf("lock helper did not become ready: %q", scanner.Text())
	}
	if _, err := (FileLocker{}).Acquire(dir); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("second process lock=%v", err)
	}
}

func TestFileLockerPublishesVerifiedOwnerMetadata(t *testing.T) {
	dir := t.TempDir()
	secret, err := NewInstanceSecret()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := (FileLocker{Owner: func() InstanceOwner {
		return InstanceOwner{Version: 1, PID: 123, URL: "http://127.0.0.1:43123", Secret: secret}
	}}).Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ReadInstanceOwner(dir)
	if err != nil || owner.PID != 123 || owner.URL != "http://127.0.0.1:43123" || owner.Secret != secret || owner.Acquired == "" {
		t.Fatalf("owner=%#v err=%v", owner, err)
	}
	if err = lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadInstanceOwner(dir); !errors.Is(err, ErrLockOwnerUnknown) {
		t.Fatalf("released owner=%v", err)
	}
}

func TestFileLockerRejectsUnsafeOwnerAndLegacyLockCannotBeRemotelyClosed(t *testing.T) {
	dir := t.TempDir()
	legacy, err := (FileLocker{}).Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadInstanceOwner(dir); !errors.Is(err, ErrLockOwnerUnknown) {
		t.Fatalf("legacy owner=%v", err)
	}
	if err = legacy.Release(); err != nil {
		t.Fatal(err)
	}
	secret, _ := NewInstanceSecret()
	if _, err = (FileLocker{Owner: func() InstanceOwner {
		return InstanceOwner{Version: 1, PID: 123, URL: "http://example.com:43123", Secret: secret}
	}}).Acquire(dir); !errors.Is(err, ErrLockOwnerUnknown) {
		t.Fatalf("unsafe owner=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".eco-guardian.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsafe lock was retained: %v", statErr)
	}
}

func TestPeerInstanceClientUsesLockSecretAndWaitsForRelease(t *testing.T) {
	dir := t.TempDir()
	id, _ := domain.NewID()
	secret, _ := NewInstanceSecret()
	var lock Lock
	var gotToken, gotProject string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotToken = request.Header.Get(InstanceTokenHeader)
		var body map[string]string
		_ = json.NewDecoder(request.Body).Decode(&body)
		gotProject = body["project_id"]
		_ = lock.Release()
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	var err error
	lock, err = (FileLocker{Owner: func() InstanceOwner {
		return InstanceOwner{Version: 1, PID: os.Getpid(), URL: server.URL, Secret: secret}
	}}).Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = (PeerInstanceClient{Client: server.Client()}).Close(context.Background(), ProjectInfo{ID: id, Name: "fixture", Path: dir}); err != nil {
		t.Fatal(err)
	}
	if gotToken != secret || gotProject != string(id) {
		t.Fatalf("token=%q project=%q", gotToken, gotProject)
	}
}

func TestPeerInstanceClientArchivesDeadOwnerLock(t *testing.T) {
	dir := t.TempDir()
	id, _ := domain.NewID()
	secret, _ := NewInstanceSecret()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	owner := InstanceOwner{Version: 1, PID: 424242, URL: server.URL, Secret: secret, Acquired: time.Now().UTC().Format(time.RFC3339Nano)}
	writeInstanceOwner(t, dir, owner)
	client := server.Client()
	server.Close()
	err := (PeerInstanceClient{Client: client, IsProcessAlive: func(pid int) (bool, error) {
		if pid != owner.PID {
			t.Fatalf("probed pid=%d want=%d", pid, owner.PID)
		}
		return false, nil
	}}).Close(context.Background(), ProjectInfo{ID: id, Name: "fixture", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".eco-guardian.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale lock still present: %v", statErr)
	}
	archives, err := filepath.Glob(filepath.Join(dir, ".eco-guardian.lock.stale-pid-424242-*"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives=%v err=%v", archives, err)
	}
	lock, err := (FileLocker{}).Acquire(dir)
	if err != nil {
		t.Fatalf("reacquire after stale lock archive: %v", err)
	}
	if err = lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerInstanceClientRetainsUnreachableLiveOwnerLock(t *testing.T) {
	dir := t.TempDir()
	id, _ := domain.NewID()
	secret, _ := NewInstanceSecret()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	owner := InstanceOwner{Version: 1, PID: 424243, URL: server.URL, Secret: secret, Acquired: time.Now().UTC().Format(time.RFC3339Nano)}
	writeInstanceOwner(t, dir, owner)
	client := server.Client()
	server.Close()
	err := (PeerInstanceClient{Client: client, IsProcessAlive: func(int) (bool, error) { return true, nil }}).Close(context.Background(), ProjectInfo{ID: id, Name: "fixture", Path: dir})
	if !errors.Is(err, ErrOtherUnavailable) {
		t.Fatalf("close=%v", err)
	}
	current, readErr := ReadInstanceOwner(dir)
	if readErr != nil || current != owner {
		t.Fatalf("owner=%#v err=%v", current, readErr)
	}
}

func TestPeerInstanceClientDoesNotArchiveChangedOwner(t *testing.T) {
	dir := t.TempDir()
	id, _ := domain.NewID()
	secret, _ := NewInstanceSecret()
	replacementSecret, _ := NewInstanceSecret()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	owner := InstanceOwner{Version: 1, PID: 424244, URL: server.URL, Secret: secret, Acquired: time.Now().UTC().Format(time.RFC3339Nano)}
	replacement := InstanceOwner{Version: 1, PID: 424245, URL: server.URL, Secret: replacementSecret, Acquired: time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)}
	writeInstanceOwner(t, dir, owner)
	client := server.Client()
	server.Close()
	err := (PeerInstanceClient{Client: client, IsProcessAlive: func(int) (bool, error) {
		writeInstanceOwner(t, dir, replacement)
		return false, nil
	}}).Close(context.Background(), ProjectInfo{ID: id, Name: "fixture", Path: dir})
	if !errors.Is(err, ErrOtherUnavailable) {
		t.Fatalf("close=%v", err)
	}
	current, readErr := ReadInstanceOwner(dir)
	if readErr != nil || current != replacement {
		t.Fatalf("owner=%#v err=%v", current, readErr)
	}
}

func writeInstanceOwner(t *testing.T, directory string, owner InstanceOwner) {
	t.Helper()
	file, err := os.Create(filepath.Join(directory, ".eco-guardian.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(file).Encode(owner); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

type fakeOtherCloser struct{ info ProjectInfo }

func (closer *fakeOtherCloser) Close(_ context.Context, info ProjectInfo) error {
	closer.info = info
	return nil
}

func TestManagerCloseOtherResolvesOnlyServerOwnedRecentProject(t *testing.T) {
	recent := NewFileRecentProjects(t.TempDir())
	id, _ := domain.NewID()
	if err := recent.Record(ProjectInfo{ID: id, Name: "fixture", Path: "/server-owned/path"}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(NewTokenStore(time.Minute, nil), fakeLocker{}, fakeFactory{}, NoJobs{}, recent)
	closer := &fakeOtherCloser{}
	if err := manager.CloseOther(context.Background(), id, closer); err != nil || closer.info.ID != id || closer.info.Path != "/server-owned/path" {
		t.Fatalf("close=%v info=%#v", err, closer.info)
	}
	missing, _ := domain.NewID()
	if err := manager.CloseOther(context.Background(), missing, closer); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("missing close=%v", err)
	}
}
