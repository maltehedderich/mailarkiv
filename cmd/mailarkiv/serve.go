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
	"sync"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/frontend"
	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/collect"
	"github.com/maltehedderich/mailarkiv/internal/identity"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
	"github.com/maltehedderich/mailarkiv/internal/processing"
	"github.com/maltehedderich/mailarkiv/internal/search"
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

	// searchProgram is found in the server binary's directory.
	searchProgram = "mailarkiv-search"
)

// Startup causes, logged in the "cause" field when the server refuses to
// start, so the log names what failed.
const (
	causeLock    = "lock"
	causeStorage = "storage"
	causeCatalog = "catalog"
	causePort    = "port"
	causeKey     = "key"
	causeWorkers = "workers"
)

// serveOptions are serve's settings besides the data directory.
type serveOptions struct {
	listen string
	// key is nil without --key-file.
	key *passwordkey.Key
}

func serve(args []string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	dataDir := flags.String("data-dir", "", "")
	listen := flags.String("listen", defaultListen, "")
	keyFile := flags.String("key-file", "", "")

	if err := flags.Parse(args); err != nil {
		return exitBadArgs
	}
	if *dataDir == "" || flags.NArg() != 0 {
		return badArgs()
	}

	// JSON lines on stderr, for the journal or a container runtime to collect.
	// Nothing logs sign-in attempts or successful requests.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	// The umask set in main makes this 0700 too. MkdirAll leaves an existing
	// directory as it is.
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return startupFailed(logger, causeStorage, err)
	}

	// Before the lock, so a bad key fails fast and touches nothing else.
	options := serveOptions{listen: *listen}
	if *keyFile != "" {
		loaded, reason, err := loadKey(*dataDir, *keyFile)
		if err != nil {
			fields := []any{"cause", causeKey, "reason", reason}
			// An invalid file's error would describe its content, so the
			// log gets the reason alone.
			if reason != keyReasonInvalid {
				fields = append(fields, "error", err.Error())
			}
			logger.Error("startup failed", fields...)
			return exitFailed
		}
		options.key = loaded
	}

	lock, err := takeLock(*dataDir)
	if err != nil {
		return startupFailed(logger, causeLock, err)
	}
	// Closing it releases the lock, and exiting would too. It has nothing to flush.
	defer lock.Close()

	if err := archive.CheckStorage(*dataDir); err != nil {
		return startupFailed(logger, causeStorage, err)
	}

	db, err := sqlitedb.Open(filepath.Join(*dataDir, "catalog.db"))
	if err != nil {
		return startupFailed(logger, causeCatalog, err)
	}
	code := serveCatalog(logger, *dataDir, db, options)
	if err := db.Close(); err != nil {
		logger.Error("close the catalog", "error", err.Error())
		return exitFailed
	}
	return code
}

// takeLock holds <data-dir>/lock for as long as the returned file stays open, so
// a second server can't delete this one's temp files during recovery. The kernel
// releases it when the process ends, however it ends. The account commands don't
// take it, because they only touch the catalog, which SQLite protects.
func takeLock(dataDir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(dataDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("take the data directory's lock, which another server may hold: %w", err), file.Close())
	}
	return file, nil
}

// serveCatalog builds the modules on the open catalog, runs recovery, and serves.
func serveCatalog(logger *slog.Logger, dataDir string, db *sqlitedb.DB, options serveOptions) int {
	files, err := fs.Sub(frontend.Build, "build")
	if err != nil {
		logger.Error("open the embedded interface", "error", err.Error())
		return exitFailed
	}
	status := opstatus.New(logger, time.Now)
	originals, err := archive.Open(dataDir, db, status, logger, time.Now)
	if err != nil {
		return startupFailed(logger, causeStorage, err)
	}
	if err := originals.Recover(context.Background()); err != nil {
		if errors.Is(err, archive.ErrCatalogUnusable) {
			return startupFailed(logger, causeCatalog, err)
		}
		return startupFailed(logger, causeStorage, err)
	}

	// A failure here leaves search unavailable but doesn't stop the server.
	prepareErr := search.Prepare(context.Background(), dataDir, db)

	if options.key == nil {
		logger.Info("collection unavailable", "reason", "no key file given")
	}
	// Nil roots trust the system's certificates, which SSL_CERT_FILE can
	// replace.
	collector, err := collect.Open(context.Background(), collect.Deps{
		DB:      db,
		Archive: originals,
		Status:  status,
		Key:     options.key,
		Logger:  logger,
		Now:     time.Now,
	})
	if err != nil {
		return startupFailed(logger, causeCatalog, err)
	}

	// Workers run this binary, and the search program sits beside it.
	executable, err := os.Executable()
	if err != nil {
		return startupFailed(logger, causeWorkers, err)
	}
	pool, err := processing.NewPool(processing.BackgroundWorkers, processing.MessageLimit, processing.WorkerCommand(executable), logger)
	if err != nil {
		return startupFailed(logger, causeWorkers, err)
	}
	// After listenAndServe, so nothing still sends work.
	defer pool.Close()
	processor := processing.New(originals, pool, logger)
	// The message view has its own workers, so a view doesn't queue behind
	// processing.
	interactive, err := processing.NewInteractivePool(processing.InteractiveWorkers, processing.MessageLimit,
		processing.InteractiveWait, processing.WorkerCommand(executable), logger)
	if err != nil {
		return startupFailed(logger, causeWorkers, err)
	}
	defer interactive.Close()
	searcher := search.New(search.Config{
		DataDir:   dataDir,
		Program:   filepath.Join(filepath.Dir(executable), searchProgram),
		DB:        db,
		Archive:   originals,
		Processor: processor,
		Status:    status,
		Logger:    logger,
		Now:       time.Now,
	})
	tasks := []func(context.Context){processor.Run}
	if prepareErr != nil {
		searcher.PrepareFailed(prepareErr)
	} else {
		tasks = append(tasks, searcher.Run)
	}

	return listenAndServe(logger, options.listen, web.New(web.Deps{
		Files:      files,
		Logger:     logger,
		Identity:   identity.New(db, time.Now),
		Archive:    originals,
		Status:     status,
		Labels:     collect.NewLabeler(db),
		Collect:    collector,
		Search:     searcher,
		Processing: processing.NewViewer(originals, interactive, logger),
	}), collector, tasks)
}

// background runs tasks, each on its own goroutine, until stop.
type background struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func startBackground(tasks []func(context.Context)) *background {
	ctx, cancel := context.WithCancel(context.Background())
	b := &background{cancel: cancel}
	for _, task := range tasks {
		b.wg.Go(func() { task(ctx) })
	}
	return b
}

// stop ends the tasks and waits for them. The search module returns within
// its own 5 s wait for the search process.
func (b *background) stop() {
	b.cancel()
	b.wg.Wait()
}

// listenAndServe serves until SIGTERM or SIGINT, then gives requests in
// progress the grace period. The collector and then the background tasks
// start once the port is bound, so a slow search process doesn't delay
// serving. All of them stop during the grace period.
func listenAndServe(logger *slog.Logger, listen string, handler http.Handler, collector *collect.Collector, tasks []func(context.Context)) int {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return startupFailed(logger, causePort, err)
	}
	collector.Start()
	running := startBackground(tasks)

	server := &http.Server{
		Handler:           handler,
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
		collector.Stop()
		running.stop()
		return exitFailed
	case <-signals.Done():
	}
	// A second signal now ends the process the default way.
	stopSignals()
	logger.Info("shutting down")

	// Stopping a sync closes its connection, so it ends well inside the grace.
	// Stopping the background tasks closes the search process's standard
	// input and kills it if it hasn't exited after 5 s.
	stopping := make(chan struct{})
	go func() {
		defer close(stopping)
		var wg sync.WaitGroup
		wg.Go(collector.Stop)
		wg.Go(running.stop)
		wg.Wait()
	}()

	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(grace); err != nil {
		logger.Warn("requests still running after the grace period were cut off", "error", err.Error())
		if err := server.Close(); err != nil {
			logger.Error("close the listener", "error", err.Error())
		}
	}
	<-stopping
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
