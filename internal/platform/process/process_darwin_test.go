//go:build darwin

package process

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinAdapterStartsSuspendedProcessAndCapturesOutput(t *testing.T) {
	adapter := New()
	command := Command{
		Executable:       "/bin/sh",
		Arguments:        []string{"-c", "printf 'darwin-child\\n'; exit 7"},
		Environment:      []string{"PATH=/bin:/usr/bin"},
		WorkingDirectory: filepath.Clean(t.TempDir()),
	}
	lines := make(chan string, 2)
	process, err := adapter.CreateSuspended(context.Background(), command, 1, OutputSinkFunc(func(output Output) {
		if output.Stream == StreamStdout {
			lines <- output.Line
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	job, err := adapter.NewKillOnCloseJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	if err := job.Assign(process); err != nil {
		t.Fatal(err)
	}
	if err := process.Resume(); err != nil {
		t.Fatal(err)
	}
	exit, err := process.Wait(context.Background())
	if err != nil || exit.Code != 7 || exit.Generation != 1 {
		t.Fatalf("exit=%#v err=%v", exit, err)
	}
	select {
	case line := <-lines:
		if line != "darwin-child" {
			t.Fatalf("output=%q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for child output")
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
}
