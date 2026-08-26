// Package process defines the platform boundary for owned child processes.
// Process handles are intentionally opaque: PIDs, names, ports, and persisted
// summaries never grant termination authority.
package process

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	ErrUnsupportedPlatform     = errors.New("owned process adapter is unsupported on this platform")
	ErrInvalidCommand          = errors.New("owned process command is invalid")
	ErrOwnership               = errors.New("owned process authority is invalid")
	ErrGracefulStopUnsupported = errors.New("owned process has no platform graceful-stop signal")
	ErrClosed                  = errors.New("owned process handle is closed")
)

type Ownership string

const (
	OwnershipNone     Ownership = "not_selected"
	OwnershipExternal Ownership = "external"
	OwnershipBundled  Ownership = "bundled"
)

type LaunchGeneration uint64

type Command struct {
	Executable       string
	Arguments        []string
	Environment      []string
	WorkingDirectory string
}

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Output struct {
	Stream     Stream
	Generation LaunchGeneration
	Line       string
	Truncated  bool
}

type OutputSink interface {
	PublishOutput(Output)
}

type OutputSinkFunc func(Output)

func (f OutputSinkFunc) PublishOutput(output Output) { f(output) }

type Exit struct {
	Generation LaunchGeneration
	Code       int
	At         time.Time
}

type OwnedProcess interface {
	// DiagnosticPID is never ownership authority and must not be used to reopen
	// or terminate a process.
	DiagnosticPID() uint32
	Generation() LaunchGeneration
	Resume() error
	Wait(context.Context) (Exit, error)
	RequestStop(context.Context) error
	Terminate(uint32) error
	Close() error
}

type JobObject interface {
	Assign(OwnedProcess) error
	Close() error
}

type Adapter interface {
	CreateSuspended(context.Context, Command, LaunchGeneration, OutputSink) (OwnedProcess, error)
	NewKillOnCloseJob() (JobObject, error)
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Backoff interface {
	Delay(attempt int) time.Duration
}

type ExponentialBackoff struct {
	Initial time.Duration
	Maximum time.Duration
}

func (policy ExponentialBackoff) Delay(attempt int) time.Duration {
	if attempt <= 0 || policy.Initial <= 0 {
		return 0
	}
	delay := policy.Initial
	for current := 1; current < attempt && delay < policy.Maximum; current++ {
		if delay > policy.Maximum/2 {
			return policy.Maximum
		}
		delay *= 2
	}
	if policy.Maximum > 0 && delay > policy.Maximum {
		return policy.Maximum
	}
	return delay
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
func (SystemClock) NewTimer(delay time.Duration) Timer {
	return systemTimer{Timer: time.NewTimer(delay)}
}

type systemTimer struct{ *time.Timer }

func (timer systemTimer) C() <-chan time.Time { return timer.Timer.C }

// New returns the host implementation. Unsupported hosts return an adapter
// whose operations fail closed with ErrUnsupportedPlatform.
func New() Adapter { return newHostAdapter() }

func ValidateCommand(command Command) error {
	if command.Executable == "" || command.WorkingDirectory == "" {
		return ErrInvalidCommand
	}
	for _, value := range command.Arguments {
		if value == "" || containsNUL(value) {
			return ErrInvalidCommand
		}
	}
	seenEnvironment := map[string]bool{}
	for _, value := range command.Environment {
		separator := strings.IndexByte(value, '=')
		if separator <= 0 || containsNUL(value) {
			return ErrInvalidCommand
		}
		name := strings.ToUpper(value[:separator])
		if seenEnvironment[name] {
			return ErrInvalidCommand
		}
		seenEnvironment[name] = true
	}
	return nil
}

func containsNUL(value string) bool {
	for _, character := range value {
		if character == 0 {
			return true
		}
	}
	return false
}

var _ io.Closer = (OwnedProcess)(nil)
