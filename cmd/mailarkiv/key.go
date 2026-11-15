package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"

	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
)

// key create's output.
const (
	msgKeyCreated    = "Created the key at %s. Back it up apart from the data directory. Without it, every IMAP password must be saved again.\n"
	msgKeyInsideData = "The key must be outside the data directory."
	msgKeyExists     = "A file already exists at %s. MailArkiv never replaces a key."
	msgKeyNotWritten = "The key couldn't be written: %v"
)

// Reasons serve logs when it can't load the key.
const (
	keyReasonInside  = "inside_data_dir"
	keyReasonMissing = "missing"
	keyReasonNoRead  = "unreadable"
	keyReasonInvalid = "invalid"
)

// key runs the key commands. Only key create exists.
func key(args []string) int {
	if len(args) == 0 || args[0] != "create" {
		return badArgs()
	}
	flags := flag.NewFlagSet("key create", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	dataDir := flags.String("data-dir", "", "")
	if err := flags.Parse(args[1:]); err != nil {
		return exitBadArgs
	}
	if *dataDir == "" || flags.NArg() != 1 {
		return badArgs()
	}
	path := flags.Arg(0)

	inside, err := passwordkey.Inside(*dataDir, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, msgKeyNotWritten+"\n", err)
		return exitFailed
	}
	if inside {
		fmt.Fprintln(os.Stderr, msgKeyInsideData)
		return exitFailed
	}
	if err := passwordkey.Create(path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			fmt.Fprintf(os.Stderr, msgKeyExists+"\n", path)
		} else {
			fmt.Fprintf(os.Stderr, msgKeyNotWritten+"\n", err)
		}
		return exitFailed
	}
	fmt.Fprintf(os.Stdout, msgKeyCreated, path)
	return 0
}

// loadKey loads serve's key, refusing one inside the data directory. The
// reason names the cause for the log, which never holds the key's content.
func loadKey(dataDir, path string) (*passwordkey.Key, string, error) {
	inside, err := passwordkey.Inside(dataDir, path)
	if err != nil {
		return nil, keyReasonNoRead, err
	}
	if inside {
		return nil, keyReasonInside, errors.New("the key file is inside the data directory")
	}
	loaded, err := passwordkey.Load(path)
	switch {
	case err == nil:
		return loaded, "", nil
	case errors.Is(err, passwordkey.ErrMissing):
		return nil, keyReasonMissing, err
	case errors.Is(err, passwordkey.ErrInvalid):
		return nil, keyReasonInvalid, err
	default:
		return nil, keyReasonNoRead, err
	}
}
