//go:build !linux

package processing

import "syscall"

// workerProcAttr sets nothing, because only Linux has Pdeathsig. MailArkiv
// runs on Linux, and this build is for development on macOS. A worker still
// exits when the server goes and its standard input closes, but a worker
// stuck on a message can outlive the server.
func workerProcAttr() *syscall.SysProcAttr {
	return nil
}
