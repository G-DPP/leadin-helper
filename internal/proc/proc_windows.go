//go:build windows

package proc

// Windows: claude runs in a job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
// like _windows_job of the earlier Python helper. Kill terminates the job,
// and if the helper dies Windows closes the handle and the job stops every
// process in it.
//
// claude is assigned to the job right after it starts: a child it starts in
// that short window stays outside the job. Accepted, as in the Python helper.
//
// If the job cannot be created, the run goes on without it and Kill stops
// only claude: a child of claude may then survive.

import (
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
}

type osTree struct {
	mu  *sync.Mutex
	job *windows.Handle
}

func attach(cmd *exec.Cmd) osTree {
	job, err := newJob(uint32(cmd.Process.Pid))
	if err != nil {
		job = 0
	}
	return osTree{mu: &sync.Mutex{}, job: &job}
}

func newJob(pid uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// kill: on Windows there is no SIGTERM to wait for, the job stops everything
// at once. Without a job, at least claude is stopped.
func (t osTree) kill(cmd *exec.Cmd, done <-chan struct{}, _ time.Duration) {
	if t.closeJob(true) {
		<-done
		return
	}
	select {
	case <-done:
		return
	default:
	}
	_ = cmd.Process.Kill()
	<-done
}

// release closes the job once claude has exited, so the handle does not
// outlive the run. Closing it stops what claude left running.
func (t osTree) release() { t.closeJob(false) }

// closeJob closes the job handle, terminating it first if terminate is set.
// It reports whether there was a job to close.
func (t osTree) closeJob(terminate bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if *t.job == 0 {
		return false
	}
	if terminate {
		_ = windows.TerminateJobObject(*t.job, 1)
	}
	_ = windows.CloseHandle(*t.job)
	*t.job = 0
	return true
}
