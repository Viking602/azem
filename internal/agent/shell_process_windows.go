//go:build windows

package agent

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shellProcessOwner struct {
	job windows.Handle
}

func newShellProcessOwner(command *exec.Cmd) (*shellProcessOwner, error) {
	// Keep the process suspended until it belongs to the kill-on-close job.
	// This closes the post-Start window in which cmd.exe could otherwise spawn
	// a descendant outside the job.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &shellProcessOwner{job: job}, nil
}

func (o *shellProcessOwner) Assign(command *exec.Cmd) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(o.job, handle); err != nil {
		return err
	}
	return resumeProcessThreads(uint32(command.Process.Pid))
}

func resumeProcessThreads(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil {
				return resumeErr
			}
			return closeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				return fmt.Errorf("primary thread for process %d was not found", pid)
			}
			return err
		}
	}
}
func (o *shellProcessOwner) PGID() int     { return 0 }
func (o *shellProcessOwner) JobID() string { return fmt.Sprintf("0x%x", uintptr(o.job)) }
func (o *shellProcessOwner) Terminate() error {
	if err := windows.TerminateJobObject(o.job, 1); err != nil && err != windows.ERROR_ACCESS_DENIED {
		return err
	}
	return nil
}
func (o *shellProcessOwner) Close() error { return windows.CloseHandle(o.job) }

func shellProcessExists(int) bool { return false }
