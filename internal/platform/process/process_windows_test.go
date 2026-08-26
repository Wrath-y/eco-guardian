//go:build windows

package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsOwnedProcessRedirectsOutputAndWaitsByLiveHandle(t *testing.T) {
	adapter := New()
	job, err := adapter.NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	output := make(chan Output, 4)
	process, err := adapter.CreateSuspended(t.Context(), windowsHelperCommand("simple"), 11, OutputSinkFunc(func(line Output) { output <- line }))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err = job.Assign(process); err != nil {
		t.Fatal(err)
	}
	if err = process.Resume(); err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait(t.Context())
	if err != nil || exit.Code != 7 || exit.Generation != 11 {
		t.Fatalf("exit=%#v err=%v", exit, err)
	}
	seen := map[Stream]string{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 2 {
		select {
		case line := <-output:
			seen[line.Stream] = line.Line
		case <-deadline:
			t.Fatalf("output=%v", seen)
		}
	}
	if seen[StreamStdout] != "owned stdout" || seen[StreamStderr] != "owned stderr" {
		t.Fatalf("output=%v", seen)
	}
}

func TestWindowsOwnedProcessHasNoConsoleWindowAndDrainsFlood(t *testing.T) {
	adapter := New()
	job, err := adapter.NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	var lines atomic.Uint64
	var console atomic.Uint64
	process, err := adapter.CreateSuspended(t.Context(), windowsHelperCommand("visibility-flood"), 14, OutputSinkFunc(func(output Output) {
		lines.Add(1)
		if strings.HasPrefix(output.Line, "CONSOLE_HANDLE=") {
			value, _ := strconv.ParseUint(strings.TrimPrefix(output.Line, "CONSOLE_HANDLE="), 10, 64)
			console.Store(value)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err = job.Assign(process); err != nil {
		t.Fatal(err)
	}
	if err = process.Resume(); err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait(t.Context())
	if err != nil || exit.Code != 0 || console.Load() != 0 || lines.Load() < 1000 {
		t.Fatalf("exit=%#v err=%v console=%d lines=%d", exit, err, console.Load(), lines.Load())
	}
}

func TestWindowsParentCrashClosesOwnedJobTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "owned-child.pid")
	owner, err := os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=^TestWindowsOwnedProcessHelper$"}, &os.ProcAttr{
		Dir: filepath.Dir(os.Args[0]), Env: append(os.Environ(), "ECO_PROCESS_HELPER=job-owner", "ECO_PROCESS_PID_FILE="+pidFile), Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Wait(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// The child may already be fully reaped, which also proves cleanup.
		return
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 10_000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned child survived parent crash: status=%d err=%v", status, err)
	}
}

func TestWindowsExternalProcessSurvivesOwnedJobClose(t *testing.T) {
	external, err := os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=^TestWindowsOwnedProcessHelper$"}, &os.ProcAttr{
		Dir: filepath.Dir(os.Args[0]), Env: append(requiredWindowsEnvironment(), "ECO_PROCESS_HELPER=block"), Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = external.Kill(); _, _ = external.Wait() }()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(external.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	job, err := New().NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	if err = job.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(handle, 100)
	if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("external process was affected: status=%d err=%v", status, err)
	}
}

func TestWindowsJobRequiresSameAdapterSuspendedHandle(t *testing.T) {
	owner := New()
	other := New()
	job, err := owner.NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	process, err := other.CreateSuspended(t.Context(), windowsHelperCommand("block"), 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	defer process.Terminate(1)
	if err = job.Assign(process); !errors.Is(err, ErrOwnership) {
		t.Fatalf("assign err=%v", err)
	}
	if err = process.Resume(); !errors.Is(err, ErrOwnership) {
		t.Fatalf("unassigned resume err=%v", err)
	}
}

func TestWindowsJobCloseReclaimsOwnedDescendant(t *testing.T) {
	adapter := New()
	job, err := adapter.NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan Output, 4)
	process, err := adapter.CreateSuspended(t.Context(), windowsHelperCommand("parent"), 13, OutputSinkFunc(func(line Output) { output <- line }))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err = job.Assign(process); err != nil {
		t.Fatal(err)
	}
	if err = process.Resume(); err != nil {
		t.Fatal(err)
	}
	var descendantPID uint64
	select {
	case line := <-output:
		value := strings.TrimPrefix(line.Line, "DESCENDANT_PID=")
		descendantPID, err = strconv.ParseUint(value, 10, 32)
		if err != nil {
			t.Fatalf("line=%q err=%v", line.Line, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("descendant was not reported")
	}
	descendant, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(descendantPID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(descendant)
	if err = job.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := windows.WaitForSingleObject(descendant, 10_000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant wait status=%d err=%v", status, err)
	}
}

func windowsHelperCommand(mode string) Command {
	return Command{
		Executable: os.Args[0], WorkingDirectory: filepath.Dir(os.Args[0]),
		Arguments:   []string{"-test.run=^TestWindowsOwnedProcessHelper$"},
		Environment: append(requiredWindowsEnvironment(), "ECO_PROCESS_HELPER="+mode),
	}
}

func requiredWindowsEnvironment() []string {
	result := []string{}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func TestWindowsOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv("ECO_PROCESS_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "simple":
		fmt.Println("owned stdout")
		fmt.Fprintln(os.Stderr, "owned stderr")
		os.Exit(7)
	case "visibility-flood":
		console, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
		fmt.Printf("CONSOLE_HANDLE=%d\n", console)
		for index := 0; index < 1000; index++ {
			fmt.Printf("stdout-%d\n", index)
			fmt.Fprintf(os.Stderr, "stderr-%d\n", index)
		}
		os.Exit(0)
	case "job-owner":
		adapter := New()
		job, err := adapter.NewKillOnCloseJob()
		if err != nil {
			os.Exit(30)
		}
		child, err := adapter.CreateSuspended(t.Context(), windowsHelperCommand("block"), 1, nil)
		if err != nil || job.Assign(child) != nil || child.Resume() != nil {
			os.Exit(31)
		}
		if err = os.WriteFile(os.Getenv("ECO_PROCESS_PID_FILE"), []byte(strconv.FormatUint(uint64(child.DiagnosticPID()), 10)), 0o600); err != nil {
			os.Exit(32)
		}
		// Deliberately bypass defers to model an abnormal Eco Guardian exit. The
		// operating system closes the Job Object handle and kills the child tree.
		os.Exit(0)
	case "parent":
		process, err := os.StartProcess(os.Args[0], []string{os.Args[0], "-test.run=^TestWindowsOwnedProcessHelper$"}, &os.ProcAttr{
			Dir: filepath.Dir(os.Args[0]), Env: append(requiredWindowsEnvironment(), "ECO_PROCESS_HELPER=block"), Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		})
		if err != nil {
			os.Exit(20)
		}
		fmt.Printf("DESCENDANT_PID=%d\n", process.Pid)
		select {}
	case "block":
		select {}
	default:
		os.Exit(21)
	}
}
