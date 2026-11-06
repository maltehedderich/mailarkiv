package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/maltehedderich/mailarkiv/internal/identity"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// The contract's lines for each refusal, written to stderr.
const (
	msgInvalidAddress = "That email address isn't valid."
	msgAccountExists  = "An account with that email address already exists."
	msgPasswordShort  = "Passwords must have at least 12 characters."
	msgNoMatch        = "The passwords don't match."
	msgNotOwner       = "Run this as the user that owns the data directory."
)

var (
	errNotOwner        = errors.New("not the data directory's owner")
	errPasswordsDiffer = errors.New("the passwords differ")
)

func account(args []string) int {
	if len(args) == 0 || args[0] != "create" {
		return badArgs()
	}
	flags := flag.NewFlagSet("account create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", "", "")
	if err := flags.Parse(args[1:]); err != nil {
		return badArgs()
	}
	// The password is never an argument, so a second one is a usage error.
	if *dataDir == "" || flags.NArg() != 1 {
		return badArgs()
	}
	address, err := identity.ParseAddress(flags.Arg(0))
	if err != nil {
		return refuse(msgInvalidAddress)
	}

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return refuse(fmt.Sprintf("The data directory couldn't be created: %v", err))
	}
	info, err := os.Stat(*dataDir)
	if err != nil {
		return refuse(fmt.Sprintf("The data directory couldn't be read: %v", err))
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return refuse("The data directory's owner couldn't be read.")
	}
	if err := checkOwner(stat.Uid, os.Geteuid()); err != nil {
		return refuse(msgNotOwner)
	}

	password, err := readPassword()
	if errors.Is(err, errPasswordsDiffer) {
		return refuse(msgNoMatch)
	}
	if err != nil {
		return refuse(fmt.Sprintf("The password couldn't be read: %v", err))
	}

	db, err := sqlitedb.Open(filepath.Join(*dataDir, "catalog.db"))
	if err != nil {
		return refuse(fmt.Sprintf("The catalog couldn't be opened: %v", err))
	}
	err = identity.New(db, time.Now).CreateAccount(context.Background(), address, password)
	if closeErr := db.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("close the catalog: %w", closeErr)
	}
	switch {
	case errors.Is(err, identity.ErrAccountExists):
		return refuse(msgAccountExists)
	case errors.Is(err, identity.ErrPasswordTooShort):
		return refuse(msgPasswordShort)
	case err != nil:
		return refuse(fmt.Sprintf("The account wasn't created: %v", err))
	}
	fmt.Printf("Created the account %s.\n", address)
	return 0
}

// checkOwner allows only the data directory's owner. Root is refused on another
// user's directory too: sudo would leave root-owned catalog files behind that
// the service user can't open.
func checkOwner(dirUID uint32, currentUID int) error {
	if currentUID < 0 || uint32(currentUID) != dirUID {
		return errNotOwner
	}
	return nil
}

// readPassword asks twice with echo off on a terminal. Otherwise it reads one
// line from stdin, so scripts and tests can drive the command.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
	}

	first, err := askHidden(fd, "Password: ")
	if err != nil {
		return "", err
	}
	second, err := askHidden(fd, "Repeat the password: ")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(first, second) {
		return "", errPasswordsDiffer
	}
	return string(first), nil
}

func askHidden(fd int, prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(fd)
	// The terminal didn't echo the Enter key, so end the prompt's line here.
	fmt.Fprintln(os.Stderr)
	return password, err
}

func refuse(message string) int {
	fmt.Fprintln(os.Stderr, message)
	return exitFailed
}
