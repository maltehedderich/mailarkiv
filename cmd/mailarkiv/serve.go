package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/frontend"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
	"github.com/maltehedderich/mailarkiv/internal/web"
)

const (
	defaultListen = "127.0.0.1:8080"

	// msgListening is logged with the bound address once the server accepts
	// connections. Tests start the server on port 0 and read the port from it.
	msgListening = "listening"

	// shutdownGrace is how long requests in progress get after SIGTERM.
	shutdownGrace = 5 * time.Second
)

// Startup causes, logged in the "cause" field when the server refuses to start,
// so the log names what failed (FR-026).
const (
	causeStorage = "storage"
	causeCatalog = "catalog"
	causePort    = "port"
)

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

	// JSON lines on stderr, for the journal or a container runtime to collect.
	// Nothing logs sign-in attempts or successful requests (FR-029).
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	// The umask set in main makes this 0700 too. MkdirAll leaves an existing
	// directory as it is.
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return startupFailed(logger, causeStorage, err)
	}

	db, err := sqlitedb.Open(filepath.Join(*dataDir, "catalog.db"))
	if err != nil {
		return startupFailed(logger, causeCatalog, err)
	}
	code := listenAndServe(logger, *listen)
	if err := db.Close(); err != nil {
		logger.Error("close the catalog", "error", err.Error())
		return exitFailed
	}
	return code
}

// listenAndServe serves until SIGTERM or SIGINT, then gives requests in
// progress the grace period.
func listenAndServe(logger *slog.Logger, listen string) int {
	files, err := fs.Sub(frontend.Build, "build")
	if err != nil {
		logger.Error("open the embedded interface", "error", err.Error())
		return exitFailed
	}

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return startupFailed(logger, causePort, err)
	}

	server := &http.Server{
		Handler:           web.New(web.Deps{Files: files, Logger: logger}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	signals, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	logger.Info(msgListening, "addr", listener.Addr().String())

	select {
	case err := <-served:
		logger.Error("the server stopped by itself", "error", err.Error())
		return exitFailed
	case <-signals.Done():
	}
	// A second signal now ends the process the default way.
	stopSignals()
	logger.Info("shutting down")

	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(grace); err != nil {
		logger.Warn("requests still running after the grace period were cut off", "error", err.Error())
		if err := server.Close(); err != nil {
			logger.Error("close the listener", "error", err.Error())
		}
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		logger.Error("the server stopped", "error", err.Error())
		return exitFailed
	}
	logger.Info("stopped")
	return 0
}

func startupFailed(logger *slog.Logger, cause string, err error) int {
	logger.Error("startup failed", "cause", cause, "error", err.Error())
	return exitFailed
}
