//go:build darwin || linux

package process

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const maxChildOutputLineBytes = 1 << 20

// adapterIdentity is intentionally private and shared only by processes and
// jobs created by one adapter instance. A PID or persisted summary cannot be
// used as ownership authority.
type adapterIdentity struct{}

type darwinAdapter struct{ identity *adapterIdentity }

func newHostAdapter() Adapter { return &darwinAdapter{identity: &adapterIdentity{}} }

func (adapter *darwinAdapter) CreateSuspended(ctx context.Context, command Command, generation LaunchGeneration, sink OutputSink) (OwnedProcess, error) {
	if err := ValidateCommand(command); err != nil || generation == 0 {
		return nil, ErrInvalidCommand
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, err
	}
	commandProcess := exec.Command(command.Executable, command.Arguments...)
	commandProcess.Dir = command.WorkingDirectory
	commandProcess.Env = append([]string(nil), command.Environment...)
	commandProcess.Stdout = stdoutWrite
	commandProcess.Stderr = stderrWrite
	commandProcess.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = commandProcess.Start(); err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		return nil, err
	}
	// POSIX hosts have no Windows-style CREATE_SUSPENDED. Stop the newly-created
	// process group immediately so the supervisor can establish ownership
	// before allowing it to serve. The tiny interval before SIGSTOP is the
	// unavoidable limitation of the POSIX adapter.
	if err = syscall.Kill(-commandProcess.Process.Pid, syscall.SIGSTOP); err != nil {
		_ = syscall.Kill(-commandProcess.Process.Pid, syscall.SIGKILL)
		_ = commandProcess.Process.Release()
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		return nil, err
	}
	process := &darwinProcess{
		command: commandProcess, pid: uint32(commandProcess.Process.Pid), pgid: commandProcess.Process.Pid,
		generation: generation, stdout: stdoutRead, stderr: stderrRead, sink: sink,
		exit: make(chan exitResult, 1), owner: adapter.identity,
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	if err = ctx.Err(); err != nil {
		_ = process.terminateGroup()
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func (adapter *darwinAdapter) NewKillOnCloseJob() (JobObject, error) {
	return &darwinJob{owner: adapter.identity, assigned: map[LaunchGeneration]*darwinProcess{}}, nil
}

type exitResult struct {
	exit Exit
	err  error
}

type darwinProcess struct {
	mu         sync.Mutex
	command    *exec.Cmd
	pid        uint32
	pgid       int
	generation LaunchGeneration
	stdout     *os.File
	stderr     *os.File
	sink       OutputSink
	exit       chan exitResult
	owner      *adapterIdentity
	assigned   bool
	resumed    bool
	resumeOnce sync.Once
	closeOnce  sync.Once
}

func (process *darwinProcess) DiagnosticPID() uint32        { return process.pid }
func (process *darwinProcess) Generation() LaunchGeneration { return process.generation }
func (process *darwinProcess) RequestStop(context.Context) error {
	return ErrGracefulStopUnsupported
}

func (process *darwinProcess) Resume() error {
	var resumeErr error
	process.resumeOnce.Do(func() {
		process.mu.Lock()
		pid, assigned, resumed := process.pid, process.assigned, process.resumed
		process.mu.Unlock()
		if pid == 0 || resumed {
			resumeErr = ErrClosed
			return
		}
		if !assigned {
			resumeErr = ErrOwnership
			return
		}
		if err := syscall.Kill(-int(pid), syscall.SIGCONT); err != nil {
			resumeErr = err
			return
		}
		process.mu.Lock()
		process.resumed = true
		stdout, stderr := process.stdout, process.stderr
		process.mu.Unlock()
		go process.copyOutput(StreamStdout, stdout)
		go process.copyOutput(StreamStderr, stderr)
		go process.wait()
	})
	return resumeErr
}

func (process *darwinProcess) Wait(ctx context.Context) (Exit, error) {
	select {
	case result := <-process.exit:
		process.exit <- result
		return result.exit, result.err
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	}
}

func (process *darwinProcess) Terminate(uint32) error { return process.terminateGroup() }

func (process *darwinProcess) terminateGroup() error {
	process.mu.Lock()
	pgid := process.pgid
	pid := process.pid
	process.mu.Unlock()
	if pgid == 0 || pid == 0 {
		return ErrClosed
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (process *darwinProcess) Close() error {
	var closeErr error
	process.closeOnce.Do(func() {
		process.mu.Lock()
		defer process.mu.Unlock()
		for _, file := range []*os.File{process.stdout, process.stderr} {
			if file != nil {
				closeErr = errors.Join(closeErr, file.Close())
			}
		}
		process.pid = 0
		process.pgid = 0
	})
	return closeErr
}

func (process *darwinProcess) wait() {
	result := exitResult{exit: Exit{Generation: process.generation}}
	err := process.command.Wait()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.exit.Code = exitError.ExitCode()
			result.exit.At = time.Now()
		} else {
			result.err = err
		}
	} else {
		result.exit.Code = process.command.ProcessState.ExitCode()
		result.exit.At = time.Now()
	}
	process.exit <- result
}

func (process *darwinProcess) copyOutput(stream Stream, file *os.File) {
	if file == nil {
		return
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxChildOutputLineBytes)
	for scanner.Scan() {
		if process.sink != nil {
			process.sink.PublishOutput(Output{Stream: stream, Generation: process.generation, Line: scanner.Text()})
		}
	}
	if scanner.Err() != nil && process.sink != nil {
		process.sink.PublishOutput(Output{Stream: stream, Generation: process.generation, Truncated: true})
	}
}

type darwinJob struct {
	mu        sync.Mutex
	owner     *adapterIdentity
	assigned  map[LaunchGeneration]*darwinProcess
	closed    bool
	closeOnce sync.Once
}

func (job *darwinJob) Assign(candidate OwnedProcess) error {
	process, ok := candidate.(*darwinProcess)
	if !ok || process.owner != job.owner || process.generation == 0 {
		return ErrOwnership
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.closed || job.assigned[process.generation] != nil {
		return ErrOwnership
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.pid == 0 || process.assigned {
		return ErrOwnership
	}
	process.assigned = true
	job.assigned[process.generation] = process
	return nil
}

func (job *darwinJob) Close() error {
	var processes []*darwinProcess
	job.closeOnce.Do(func() {
		job.mu.Lock()
		job.closed = true
		for _, process := range job.assigned {
			processes = append(processes, process)
		}
		job.assigned = nil
		job.mu.Unlock()
	})
	var closeErr error
	for _, process := range processes {
		closeErr = errors.Join(closeErr, process.terminateGroup())
	}
	return closeErr
}

var _ Adapter = (*darwinAdapter)(nil)
var _ JobObject = (*darwinJob)(nil)
