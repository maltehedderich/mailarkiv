// Command mailarkiv runs the MailArkiv server.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/frontend"
	"github.com/maltehedderich/mailarkiv/internal/web"
)

const (
	exitFailed  = 1
	exitBadArgs = 2

	defaultListen = "127.0.0.1:8080"

	usage = `Usage:
  mailarkiv serve --data-dir DIR [--listen ADDR]

Options:
  --data-dir DIR   Directory that holds the archive. Required.
  --listen ADDR    Address to listen on. Default: ` + defaultListen + `
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
	default:
		return badArgs()
	}
}

func serve(args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	dataDir := flags.String("data-dir", "", "")
	listen := flags.String("listen", defaultListen, "")

	if err := flags.Parse(args); err != nil {
		return exitBadArgs
	}
	if *dataDir == "" || flags.NArg() != 0 {
		return badArgs()
	}

	build, err := fs.Sub(frontend.Build, "build")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mailarkiv: open the embedded interface: %v\n", err)
		return exitFailed
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           web.Static(build),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	err = server.ListenAndServe()
	fmt.Fprintf(os.Stderr, "mailarkiv: serve: %v\n", err)
	return exitFailed
}

func badArgs() int {
	fmt.Fprint(os.Stderr, usage)
	return exitBadArgs
}
