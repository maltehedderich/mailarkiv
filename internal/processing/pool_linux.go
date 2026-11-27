package processing

import "syscall"

// workerProcAttr has the kernel kill a worker when the server goes, even a
// worker stuck on a message.
func workerProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
