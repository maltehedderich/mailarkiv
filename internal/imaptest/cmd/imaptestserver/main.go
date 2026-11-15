// Command imaptestserver runs the IMAP test double as a process, for the
// browser tests. It serves one mailbox over implicit TLS on 127.0.0.1, on a
// port the system picks, and prints the address on stdout once the mailbox
// is filled and the CA is written. It runs until SIGTERM or SIGINT.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

const usage = `Usage: imaptestserver --ca-out PATH --user NAME --password PASSWORD [--folder NAME=COUNT]... [--fetch-delay DURATION]

  --ca-out PATH          write the test CA's certificate here, for SSL_CERT_FILE
  --user NAME            the mailbox's user name
  --password PASSWORD    the password LOGIN accepts
  --folder NAME=COUNT    fill a folder with COUNT synthetic messages. Repeat it
                         for more folders. NAME may contain "=", because COUNT
                         follows the last one
  --fetch-delay DURATION hold each message's first body fetch this long
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	options, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n\n%s", err, usage)
		return 2
	}

	// Registered before the address is printed, so a signal sent as soon as
	// the address arrives still stops the server cleanly.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	double, err := imaptest.New(imaptest.Options{
		Security: imaptest.ImplicitTLS,
		User:     options.user,
		Password: options.password,
	})
	if err != nil {
		fmt.Fprintf(stderr, "start the server: %v\n", err)
		return 1
	}
	err = serve(double, options, stdout, stop)
	if closeErr := double.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close the server: %w", closeErr))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// serve fills the mailbox, writes the CA, prints the address, and waits for
// a signal.
func serve(double *imaptest.Server, options options, stdout io.Writer, stop <-chan os.Signal) error {
	if err := double.WriteCA(options.caOut); err != nil {
		return fmt.Errorf("write the CA: %w", err)
	}
	if err := fill(double, options.folders, options.fetchDelay); err != nil {
		return fmt.Errorf("fill the mailbox: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, double.Addr()); err != nil {
		return fmt.Errorf("print the address: %w", err)
	}
	<-stop
	return nil
}

type options struct {
	caOut      string
	user       string
	password   string
	folders    folderList
	fetchDelay time.Duration
}

func parseArgs(args []string) (options, error) {
	var o options
	flags := flag.NewFlagSet("imaptestserver", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.caOut, "ca-out", "", "")
	flags.StringVar(&o.user, "user", "", "")
	flags.StringVar(&o.password, "password", "", "")
	flags.Var(&o.folders, "folder", "")
	flags.DurationVar(&o.fetchDelay, "fetch-delay", 0, "")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	switch {
	case flags.NArg() != 0:
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	case o.caOut == "":
		return options{}, errors.New("--ca-out is required")
	case o.user == "" || o.password == "":
		return options{}, errors.New("--user and --password are required")
	case o.fetchDelay < 0:
		return options{}, errors.New("--fetch-delay can't be negative")
	}
	return o, nil
}

type folder struct {
	name  string
	count int
}

// folderList collects each --folder in the order given. LIST answers sorted
// by name whatever this order is.
type folderList []folder

func (l *folderList) String() string { return "" }

func (l *folderList) Set(value string) error {
	at := strings.LastIndex(value, "=")
	if at < 1 {
		return fmt.Errorf("%q isn't NAME=COUNT", value)
	}
	count, err := strconv.Atoi(value[at+1:])
	if err != nil || count < 0 {
		return fmt.Errorf("%q has no count of 0 or more", value)
	}
	*l = append(*l, folder{name: value[:at], count: count})
	return nil
}

// fill creates each folder and adds its messages. Every message has
// different bytes, so each is its own entry in the archive.
func fill(double *imaptest.Server, folders folderList, fetchDelay time.Duration) error {
	for index, f := range folders {
		// The double starts with an INBOX.
		if !strings.EqualFold(f.name, "INBOX") {
			if err := double.AddFolder(f.name); err != nil {
				return fmt.Errorf("add folder %d: %w", index+1, err)
			}
		}
		for n := 1; n <= f.count; n++ {
			body := fmt.Sprintf("From: sender@example.test\r\n"+
				"To: mailbox@example.test\r\n"+
				"Subject: Synthetic message %d in folder %d\r\n"+
				"Message-ID: <%d.%d@example.test>\r\n"+
				"\r\n"+
				"Synthetic message %d in folder %d.\r\n",
				n, index+1, n, index+1, n, index+1)
			uid, err := double.AddMessage(f.name, []byte(body), time.Time{})
			if err != nil {
				return fmt.Errorf("add message %d to folder %d: %w", n, index+1, err)
			}
			if fetchDelay > 0 {
				delay(double.HoldBody(f.name, uid), fetchDelay)
			}
		}
	}
	return nil
}

// delay releases hold once a body fetch has waited at it for d. Later
// fetches of the message pass at once.
func delay(hold *imaptest.Hold, d time.Duration) {
	go func() {
		<-hold.Reached()
		time.Sleep(d)
		hold.Release()
	}()
}
