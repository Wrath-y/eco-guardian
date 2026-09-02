package process

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestExponentialBackoffIsBounded(t *testing.T) {
	policy := ExponentialBackoff{Initial: 100 * time.Millisecond, Maximum: 450 * time.Millisecond}
	want := []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 450 * time.Millisecond, 450 * time.Millisecond}
	for attempt, expected := range want {
		if got := policy.Delay(attempt); got != expected {
			t.Fatalf("attempt %d delay=%s want=%s", attempt, got, expected)
		}
	}
}

func TestCommandValidationRejectsEmptyAndNULValues(t *testing.T) {
	valid := Command{Executable: "component.exe", WorkingDirectory: "component", Arguments: []string{"serve"}, Environment: []string{"LANG=C"}}
	if err := ValidateCommand(valid); err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		{},
		{Executable: "component.exe", WorkingDirectory: "component", Arguments: []string{""}},
		{Executable: "component.exe", WorkingDirectory: "component", Environment: []string{"TOKEN=x\x00y"}},
	} {
		if !errors.Is(ValidateCommand(command), ErrInvalidCommand) {
			t.Fatalf("command unexpectedly valid: %#v", command)
		}
	}
}

func TestUnsupportedAdapterFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("unsupported adapter is selected only on hosts without a native process implementation")
	}
	adapter := New()
	if _, err := adapter.CreateSuspended(context.Background(), Command{}, 1, nil); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("create err=%v", err)
	}
	if _, err := adapter.NewKillOnCloseJob(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("job err=%v", err)
	}
}
