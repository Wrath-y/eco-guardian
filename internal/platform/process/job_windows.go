//go:build windows

package process

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsJob struct {
	mu        sync.Mutex
	handle    windows.Handle
	owner     *adapterIdentity
	assigned  map[LaunchGeneration]bool
	closeOnce sync.Once
}

func newWindowsJob(owner *adapterIdentity) (JobObject, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsJob{handle: handle, owner: owner, assigned: map[LaunchGeneration]bool{}}, nil
}

func (job *windowsJob) Assign(candidate OwnedProcess) error {
	process, ok := candidate.(*windowsProcess)
	if !ok || process.owner != job.owner || process.generation == 0 {
		return ErrOwnership
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.handle == 0 || job.assigned[process.generation] {
		return ErrOwnership
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.process == 0 || process.thread == 0 || process.assigned {
		return ErrOwnership
	}
	if err := windows.AssignProcessToJobObject(job.handle, process.process); err != nil {
		return err
	}
	process.assigned = true
	job.assigned[process.generation] = true
	return nil
}

func (job *windowsJob) Close() error {
	var closeErr error
	job.closeOnce.Do(func() {
		job.mu.Lock()
		defer job.mu.Unlock()
		if job.handle != 0 {
			closeErr = windows.CloseHandle(job.handle)
			job.handle = 0
		}
		job.assigned = nil
	})
	return closeErr
}

var _ JobObject = (*windowsJob)(nil)
