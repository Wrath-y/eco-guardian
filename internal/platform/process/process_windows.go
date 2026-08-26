//go:build windows

package process

import (
	"bufio"
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxChildOutputLineBytes = 1 << 20

type adapterIdentity struct{}

type windowsAdapter struct{ identity *adapterIdentity }

func newWindowsAdapter() Adapter { return &windowsAdapter{identity: &adapterIdentity{}} }

func (adapter *windowsAdapter) CreateSuspended(ctx context.Context, command Command, generation LaunchGeneration, sink OutputSink) (OwnedProcess, error) {
	if err := ValidateCommand(command); err != nil || generation == 0 {
		return nil, ErrInvalidCommand
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := createOutputPipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		if stdoutRead != 0 {
			_ = windows.CloseHandle(stdoutRead)
		}
		if stdoutWrite != 0 {
			_ = windows.CloseHandle(stdoutWrite)
		}
	}()
	stderrRead, stderrWrite, err := createOutputPipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		if stderrRead != 0 {
			_ = windows.CloseHandle(stderrRead)
		}
		if stderrWrite != 0 {
			_ = windows.CloseHandle(stderrWrite)
		}
	}()

	handles := []windows.Handle{stdoutWrite, stderrWrite}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	if err = attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return nil, err
	}
	executable, err := windows.UTF16PtrFromString(command.Executable)
	if err != nil {
		return nil, ErrInvalidCommand
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{command.Executable}, command.Arguments...)))
	if err != nil {
		return nil, ErrInvalidCommand
	}
	workingDirectory, err := windows.UTF16PtrFromString(command.WorkingDirectory)
	if err != nil {
		return nil, ErrInvalidCommand
	}
	environment := windowsEnvironment(command.Environment)
	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW,
			ShowWindow: windows.SW_HIDE, StdOutput: stdoutWrite, StdErr: stderrWrite,
		},
		ProcThreadAttributeList: attributes.List(),
	}
	var information windows.ProcessInformation
	err = windows.CreateProcess(
		executable, commandLine, nil, nil, true,
		windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		&environment[0], workingDirectory, &startup.StartupInfo, &information,
	)
	if err != nil {
		return nil, err
	}
	stdoutWrite, stderrWrite = 0, 0
	_ = windows.CloseHandle(handles[0])
	_ = windows.CloseHandle(handles[1])
	process := &windowsProcess{
		process: information.Process, thread: information.Thread, pid: information.ProcessId, generation: generation,
		stdout: os.NewFile(uintptr(stdoutRead), "owned-child-stdout"), stderr: os.NewFile(uintptr(stderrRead), "owned-child-stderr"),
		sink: sink, exit: make(chan exitResult, 1), owner: adapter.identity,
	}
	stdoutRead, stderrRead = 0, 0
	if err = ctx.Err(); err != nil {
		_ = process.Terminate(1)
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func (adapter *windowsAdapter) NewKillOnCloseJob() (JobObject, error) {
	return newWindowsJob(adapter.identity)
}

func createOutputPipe() (windows.Handle, windows.Handle, error) {
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var read, write windows.Handle
	if err := windows.CreatePipe(&read, &write, &attributes, 0); err != nil {
		return 0, 0, err
	}
	if err := windows.SetHandleInformation(read, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(read)
		_ = windows.CloseHandle(write)
		return 0, 0, err
	}
	return read, write, nil
}

func windowsEnvironment(values []string) []uint16 {
	values = append([]string(nil), values...)
	sort.Slice(values, func(left, right int) bool { return strings.ToUpper(values[left]) < strings.ToUpper(values[right]) })
	joined := strings.Join(values, "\x00") + "\x00\x00"
	return utf16.Encode([]rune(joined))
}

type exitResult struct {
	exit Exit
	err  error
}

type windowsProcess struct {
	mu         sync.Mutex
	process    windows.Handle
	thread     windows.Handle
	pid        uint32
	generation LaunchGeneration
	stdout     *os.File
	stderr     *os.File
	sink       OutputSink
	exit       chan exitResult
	resumeOnce sync.Once
	closeOnce  sync.Once
	owner      *adapterIdentity
	assigned   bool
}

func (process *windowsProcess) DiagnosticPID() uint32             { return process.pid }
func (process *windowsProcess) Generation() LaunchGeneration      { return process.generation }
func (process *windowsProcess) RequestStop(context.Context) error { return ErrGracefulStopUnsupported }

func (process *windowsProcess) Resume() error {
	var resumeErr error
	process.resumeOnce.Do(func() {
		process.mu.Lock()
		thread := process.thread
		handle := process.process
		assigned := process.assigned
		process.mu.Unlock()
		if thread == 0 || handle == 0 {
			resumeErr = ErrClosed
			return
		}
		if !assigned {
			resumeErr = ErrOwnership
			return
		}
		if _, err := windows.ResumeThread(thread); err != nil {
			resumeErr = err
			return
		}
		_ = windows.CloseHandle(thread)
		process.mu.Lock()
		process.thread = 0
		process.mu.Unlock()
		go process.copyOutput(StreamStdout, process.stdout)
		go process.copyOutput(StreamStderr, process.stderr)
		go process.wait(handle)
	})
	return resumeErr
}

func (process *windowsProcess) Wait(ctx context.Context) (Exit, error) {
	select {
	case result := <-process.exit:
		process.exit <- result
		return result.exit, result.err
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	}
}

func (process *windowsProcess) Terminate(code uint32) error {
	process.mu.Lock()
	handle := process.process
	process.mu.Unlock()
	if handle == 0 {
		return ErrClosed
	}
	return windows.TerminateProcess(handle, code)
}

func (process *windowsProcess) Close() error {
	var closeErr error
	process.closeOnce.Do(func() {
		process.mu.Lock()
		defer process.mu.Unlock()
		for _, file := range []*os.File{process.stdout, process.stderr} {
			if file != nil {
				closeErr = errors.Join(closeErr, file.Close())
			}
		}
		if process.thread != 0 {
			closeErr = errors.Join(closeErr, windows.CloseHandle(process.thread))
			process.thread = 0
		}
		if process.process != 0 {
			closeErr = errors.Join(closeErr, windows.CloseHandle(process.process))
			process.process = 0
		}
	})
	return closeErr
}

func (process *windowsProcess) wait(handle windows.Handle) {
	result := exitResult{exit: Exit{Generation: process.generation}}
	status, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		result.err = err
		if result.err == nil {
			result.err = errors.New("unexpected process wait status")
		}
	} else {
		var code uint32
		if result.err = windows.GetExitCodeProcess(handle, &code); result.err == nil {
			result.exit.Code = int(code)
			result.exit.At = time.Now()
		}
	}
	process.exit <- result
}

func (process *windowsProcess) copyOutput(stream Stream, file *os.File) {
	if file == nil {
		return
	}
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 4096)
	scanner.Buffer(buffer, maxChildOutputLineBytes)
	for scanner.Scan() {
		if process.sink != nil {
			process.sink.PublishOutput(Output{Stream: stream, Generation: process.generation, Line: scanner.Text()})
		}
	}
	if scanner.Err() != nil && process.sink != nil {
		process.sink.PublishOutput(Output{Stream: stream, Generation: process.generation, Truncated: true})
	}
}
