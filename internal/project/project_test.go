package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(100 * time.Millisecond)
	if _, err := (FileLocker{}).Acquire(dir); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("second process lock=%v", err)
	}
}
