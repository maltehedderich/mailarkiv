// Command mailarkiv runs the MailArkiv server.
package main

import (
	"fmt"
	"os"
	"syscall"
)

const (
	exitFailed  = 1
	exitBadArgs = 2

	usage = `Usage:
  mailarkiv serve --data-dir DIR [--listen ADDR]
  mailarkiv account create --data-dir DIR EMAIL

Options:
  --data-dir DIR   Directory that holds the archive. Required.
  --listen ADDR    Address to listen on. Default: ` + defaultListen + `

account create asks for the password. Without a terminal, it reads one line
from stdin. The password is never an argument.
`
)

func main() {
	// First, so nothing this process creates is readable by other users.
	syscall.Umask(0o077)
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return badArgs()
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "account":
		return account(args[1:])
	default:
		return badArgs()
	}
}

func badArgs() int {
	fmt.Fprint(os.Stderr, usage)
	return exitBadArgs
}
