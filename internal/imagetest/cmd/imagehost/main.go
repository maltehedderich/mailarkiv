// Command imagehost runs the image host as a process, for the browser tests.
// It serves HTTPS on 127.0.0.1, on a port the system picks, prints the
// address on its first line, and runs until SIGTERM or SIGINT.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/maltehedderich/mailarkiv/internal/imagetest"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "Usage: imagehost")
		return 2
	}
	// Registered before the address is printed, so a signal sent as soon as
	// the address arrives still stops the host cleanly.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	host, err := imagetest.Start()
	if err != nil {
		fmt.Fprintf(stderr, "start the host: %v\n", err)
		return 1
	}
	status := 0
	if _, err := fmt.Fprintln(stdout, host.Addr()); err != nil {
		fmt.Fprintf(stderr, "print the address: %v\n", err)
		status = 1
	} else {
		<-stop
	}
	if err := host.Close(); err != nil {
		fmt.Fprintf(stderr, "close the host: %v\n", err)
		status = 1
	}
	return status
}
