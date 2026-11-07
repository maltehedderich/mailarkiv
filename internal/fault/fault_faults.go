//go:build faults

// Package fault marks the points in capture and recovery where tests stop the
// process or inject an operating system error. This file is only in the build
// that process tests make with -tags faults.
package fault

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// actions is what each point does, parsed once from MAILARKIV_FAULTS. A mistake
// in it stops the process at startup, because a fault that silently never fires
// would let a test pass without testing anything.
var actions = parse(os.Getenv("MAILARKIV_FAULTS"))

func parse(setting string) map[string]func() error {
	parsed := map[string]func() error{}
	if setting == "" {
		return parsed
	}
	for entry := range strings.SplitSeq(setting, ",") {
		point, action, found := strings.Cut(entry, "=")
		if !found || point == "" {
			panic(fmt.Sprintf("MAILARKIV_FAULTS: %q isn't point=action", entry))
		}
		switch action {
		case "kill":
			parsed[point] = kill
		case "ENOSPC":
			parsed[point] = returns(syscall.ENOSPC)
		case "EACCES":
			parsed[point] = returns(syscall.EACCES)
		case "EIO":
			parsed[point] = returns(syscall.EIO)
		default:
			panic(fmt.Sprintf("MAILARKIV_FAULTS: %q has an unknown action", entry))
		}
	}
	return parsed
}

func returns(errno syscall.Errno) func() error {
	return func() error { return errno }
}

// kill ends the process the way a crash does: no deferred call and no flush runs.
func kill() error {
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGKILL); err != nil {
		panic(fmt.Sprintf("kill this process: %v", err))
	}
	// SIGKILL is on its way, and nothing after this point may run.
	select {}
}

// At does what MAILARKIV_FAULTS says for this point, if anything.
func At(point string) error {
	action, set := actions[point]
	if !set {
		return nil
	}
	return action()
}
