// Command mailarkiv runs the MailArkiv server.
package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/maltehedderich/mailarkiv/internal/processing"
	"golang.org/x/term"
)

const (
	exitFailed  = 1
	exitBadArgs = 2

	usage = `Usage:
  mailarkiv serve --data-dir DIR [--listen ADDR] [--key-file PATH]
  mailarkiv account create --data-dir DIR EMAIL
  mailarkiv account set-password --data-dir DIR EMAIL
  mailarkiv key create --data-dir DIR PATH

Options:
  --data-dir DIR   Directory that holds the archive. Required.
  --listen ADDR    Address to listen on. Default: ` + defaultListen + `
  --key-file PATH  Key for stored IMAP passwords. Required for collection.

The account commands ask for the password. Without a terminal, they read one
line from stdin. The password is never an argument.
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
	case "key":
		return key(args[1:])
	case "parse-worker":
		// Internal, so the usage text doesn't name it. The server starts it
		// with pipes; someone typing it at a terminal gets the usage text.
		if len(args) != 1 || term.IsTerminal(int(os.Stdin.Fd())) {
			return badArgs()
		}
		return processing.RunWorker(os.Stdin, os.Stdout)
	default:
		return badArgs()
	}
}

func badArgs() int {
	fmt.Fprint(os.Stderr, usage)
	return exitBadArgs
}
