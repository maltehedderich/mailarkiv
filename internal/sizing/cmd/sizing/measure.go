package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/identity"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
	"github.com/maltehedderich/mailarkiv/internal/search"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

const (
	// searchProgram is the release build, from the repository root.
	searchProgram = "bin/mailarkiv-search"
	// searchPasses is how often each search runs. The first pass finds
	// cold pages, the later ones the page cache a running server keeps.
	searchPasses = 5
	// queriesPerClass is how many different searches each class holds.
	queriesPerClass = 10
	pollEvery       = 500 * time.Millisecond
	// memoryEvery is how often the search process's memory is read.
	memoryEvery = 100 * time.Millisecond
	// oldIndexWait is how long the old index may take to go after a swap.
	oldIndexWait = 2 * time.Minute
)

// The targets a supported size must meet.
const (
	targetP95     = time.Second
	targetRebuild = 8 * time.Hour
	targetMemory  = 1 << 30
)

type measureOptions struct {
	dataDir string
	results string
	out     io.Writer
	logger  *slog.Logger
}

// The phases memory peaks are kept for.
const (
	phaseBuild = iota
	phaseSearches
	phaseRebuild
	phaseCount
)

var phaseNames = [phaseCount]string{"Processing and first index build", "Searches", "Full rebuild"}

// findings is what one measure run found.
type findings struct {
	messages    int
	build       time.Duration
	searches    []classResult
	rebuild     time.Duration
	memoryPeaks [phaseCount]int64
	indexBytes  int64
	versions    versions
	host        string
}

// measure processes and indexes every message in the data directory as the
// server does, runs the search classes, times a full rebuild, and appends
// the results to o.results.
func measure(ctx context.Context, o measureOptions) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	// Resolved, so it compares equal to what /proc reports as the search
	// process's executable.
	program, err := filepath.EvalSymlinks(filepath.Join(root, searchProgram))
	if err != nil {
		return fmt.Errorf("run mise run search:build: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(o.dataDir, "catalog.db")); err != nil {
		return fmt.Errorf("run sizing seed first: %w", err)
	}
	db, err := sqlitedb.Open(filepath.Join(o.dataDir, "catalog.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	status := opstatus.New(o.logger, time.Now)
	originals, err := archive.Open(o.dataDir, db, status, o.logger, time.Now)
	if err != nil {
		return err
	}
	page, err := originals.List(ctx, 1)
	if err != nil {
		return err
	}
	fixed, err := filepath.Glob(filepath.Join(root, fixedSetDir, "*.eml"))
	if err != nil {
		return err
	}
	result := findings{messages: page.Total, host: hostDescription()}
	if result.versions, err = readVersions(ctx, program, o.dataDir); err != nil {
		return err
	}

	if err := search.Prepare(ctx, o.dataDir, db); err != nil {
		return err
	}
	pool, err := processing.NewPool(processing.BackgroundWorkers, processing.MessageLimit,
		processing.WorkerCommand(executable), o.logger)
	if err != nil {
		return err
	}
	processor := processing.New(originals, pool, o.logger)
	searcher := search.New(search.Config{
		DataDir:   o.dataDir,
		Program:   program,
		DB:        db,
		Archive:   originals,
		Processor: processor,
		Status:    status,
		Logger:    o.logger,
		Now:       time.Now,
	})
	memory := &memorySampler{program: program}

	running, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { processor.Run(running) })
	wg.Go(func() { searcher.Run(running) })
	wg.Go(func() { memory.run(running) })
	stop := sync.OnceFunc(func() {
		cancel()
		wg.Wait()
		pool.Close()
	})
	defer stop()

	fmt.Fprintf(o.out, "Processing and indexing %d messages\n", result.messages)
	started := time.Now()
	// The check reads the state and one ID, because counting millions of
	// rows twice a second would slow the run it measures.
	err = waitUntil(ctx, status, o.out, func() (bool, error) {
		if searcher.State() != upToDateState {
			return false, nil
		}
		waiting, err := originals.NotProcessed(ctx, 1, nil)
		if err != nil || len(waiting) > 0 {
			return false, err
		}
		report, err := searcher.Report(ctx)
		return err == nil && upToDate(report), err
	}, func() (string, error) {
		counts, err := originals.ProcessingCounts(ctx)
		if err != nil {
			return "", err
		}
		report, err := searcher.Report(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d not processed yet, %d processed and not indexed yet, after %s",
			counts.NotProcessed, report.Waiting, formatDuration(time.Since(started))), nil
	})
	if err != nil {
		return err
	}
	result.build = time.Since(started)

	fmt.Fprintf(o.out, "Searching\n")
	memory.setPhase(phaseSearches)
	// seed generates at least this many, so every token searched for exists.
	classes := searchClasses(newVocabulary(), result.messages-len(fixed))
	if result.searches, err = runSearches(ctx, searcher, classes); err != nil {
		return err
	}

	fmt.Fprintf(o.out, "Rebuilding\n")
	memory.setPhase(phaseRebuild)
	accountID, err := rebuildAccount(ctx, db)
	if err != nil {
		return err
	}
	if _, err := searcher.StartRebuild(ctx, accountID); err != nil {
		return err
	}
	started = time.Now()
	err = waitUntil(ctx, status, o.out, func() (bool, error) {
		report, err := searcher.Report(ctx)
		return err == nil && report.Rebuild == nil && upToDate(report), err
	}, func() (string, error) {
		report, err := searcher.Report(ctx)
		if err != nil || report.Progress == nil {
			return fmt.Sprintf("rebuild running after %s", formatDuration(time.Since(started))), err
		}
		return fmt.Sprintf("rebuild at %d of %d after %s",
			report.Progress.Done, report.Progress.ToDo, formatDuration(time.Since(started))), nil
	})
	if err != nil {
		return err
	}
	result.rebuild = time.Since(started)
	if result.indexBytes, err = rebuiltIndexSize(ctx, o.dataDir); err != nil {
		return err
	}

	stop()
	if err := failures(status); err != nil {
		return err
	}
	result.memoryPeaks = memory.peaks()
	if err := appendResults(o.results, result); err != nil {
		return err
	}
	fmt.Fprintf(o.out, "Appended the results to %s\n", o.results)
	return nil
}

var upToDateState = search.State{Search: search.SearchAvailable, Indexing: search.IndexingUpToDate}

func upToDate(report search.Report) bool {
	return report.State == upToDateState && report.Waiting == 0
}

// waitUntil polls done until it reports true, printing progress's line
// every progressEvery. A failure event ends the wait, because numbers from a
// run that failed don't describe normal operation.
func waitUntil(ctx context.Context, status *opstatus.Status, out io.Writer,
	done func() (bool, error), progress func() (string, error)) error {
	lastPrinted := time.Now()
	for {
		if err := failures(status); err != nil {
			return err
		}
		finished, err := done()
		if err != nil || finished {
			return err
		}
		if time.Since(lastPrinted) >= progressEvery {
			line, err := progress()
			if err != nil {
				return err
			}
			fmt.Fprintln(out, line)
			lastPrinted = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// failures names every failure event recorded so far.
func failures(status *opstatus.Status) error {
	events := status.Snapshot().Failures
	if len(events) == 0 {
		return nil
	}
	names := make([]string, len(events))
	for i, event := range events {
		names[i] = fmt.Sprintf("%s (%d times)", event.Category, event.Repeats)
	}
	return fmt.Errorf("the run recorded failure events, so its numbers aren't valid: %s; the log has the details",
		strings.Join(names, ", "))
}

// rebuildAccount returns the ID of the account a rebuild is recorded under.
// It signs in as the interface does, because a rebuild names who started
// it. A second run on the same data directory gives the account a new
// password.
func rebuildAccount(ctx context.Context, db *sqlitedb.DB) (int64, error) {
	accounts := identity.New(db, time.Now)
	password := rand.Text()
	err := accounts.CreateAccount(ctx, importer, password)
	if errors.Is(err, identity.ErrAccountExists) {
		err = accounts.SetPassword(ctx, importer, password)
	}
	if err != nil {
		return 0, fmt.Errorf("prepare the account that starts the rebuild: %w", err)
	}
	token, err := accounts.SignIn(ctx, importer, password)
	if err != nil {
		return 0, fmt.Errorf("sign in to start the rebuild: %w", err)
	}
	session, err := accounts.LookupSession(ctx, token)
	if err != nil {
		return 0, fmt.Errorf("sign in to start the rebuild: %w", err)
	}
	if err := accounts.SignOut(ctx, token); err != nil {
		return 0, err
	}
	return session.Account.ID, nil
}

// rebuiltIndexSize waits until the swap's old index is gone, then sums the
// sizes of the files in the one left.
func rebuiltIndexSize(ctx context.Context, dataDir string) (int64, error) {
	indexDir := filepath.Join(dataDir, "index")
	deadline := time.Now().Add(oldIndexWait)
	for {
		entries, err := os.ReadDir(indexDir)
		if err != nil {
			return 0, err
		}
		if len(entries) == 1 {
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("%s still holds %d entries %s after the rebuild", indexDir, len(entries), oldIndexWait)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
	var size int64
	err := filepath.WalkDir(indexDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

// searchClass is a kind of search, as an administrator might type it.
type searchClass struct {
	name string
	// prefix is true when a word is typed in part, so it matches many
	// different words.
	prefix  bool
	queries []search.Query
}

// searchClasses returns the classes, each with queriesPerClass searches
// picked from the vocabulary by frequency rank. generated is the number of
// generated messages, whose tokens the unique token class searches for.
func searchClasses(v *vocabulary, generated int) []searchClass {
	common := func(k int) string {
		if k%2 == 0 {
			return germanWords[k/2]
		}
		return englishWords[k/2]
	}
	lessCommon := func(k int) string { return v.invented[1000+k] }
	// Senders by how often they write, the first about 2% of all mail.
	senders := []int{0, 1, 2, 5, 10, 50, 100, 500, 1000, 5000}
	shortPrefixes := []string{"be", "ve", "ge", "an", "ma", "ko", "st", "pr", "re", "de"}
	yearFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	yearBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	class := func(name string, prefix bool, query func(k int) search.Query) searchClass {
		c := searchClass{name: name, prefix: prefix}
		for k := range queriesPerClass {
			q := query(k)
			q.Page = 1
			c.queries = append(c.queries, q)
		}
		return c
	}
	return []searchClass{
		class("Unique token", false, func(k int) search.Query {
			return search.Query{Words: token(k * generated / queriesPerClass)}
		}),
		class("Common word", false, func(k int) search.Query { return search.Query{Words: common(k)} }),
		class("Less common word", false, func(k int) search.Query { return search.Query{Words: lessCommon(k)} }),
		class("Rare word", false, func(k int) search.Query { return search.Query{Words: v.invented[200_000+k]} }),
		class("Common and less common word", false, func(k int) search.Query {
			return search.Query{Words: common(k) + " " + lessCommon(k)}
		}),
		class("Sender address", false, func(k int) search.Query {
			return search.Query{From: v.people[senders[k]].Address}
		}),
		class("Sender domain", false, func(k int) search.Query {
			_, domain, _ := strings.Cut(v.people[senders[k]].Address, "@")
			return search.Query{From: domain}
		}),
		class("Attachment name", false, func(k int) search.Query {
			return search.Query{Attachment: strings.ToLower(attachmentWords[k])}
		}),
		class("Less common word within one year", false, func(k int) search.Query {
			return search.Query{Words: lessCommon(k), ListDateFrom: &yearFrom, ListDateBefore: &yearBefore}
		}),
		class("Common word with attachments", false, func(k int) search.Query {
			return search.Query{Words: common(k), HasAttachments: true}
		}),
		class("4-character prefix", true, func(k int) search.Query {
			return search.Query{Words: v.invented[500+k*37][:4]}
		}),
		class("Two words and a 3-character prefix", true, func(k int) search.Query {
			return search.Query{Words: common(k) + " " + lessCommon(k) + " " + v.invented[700+k*41][:3]}
		}),
		class("2-character prefix", true, func(k int) search.Query { return search.Query{Words: shortPrefixes[k]} }),
	}
}

// classResult is one class's runs.
type classResult struct {
	class    searchClass
	times    []time.Duration
	hits     []int
	timedOut int
}

// runSearches runs every class's searches searchPasses times, one pass
// over all classes after another.
func runSearches(ctx context.Context, searcher *search.Module, classes []searchClass) ([]classResult, error) {
	results := make([]classResult, len(classes))
	for i, c := range classes {
		results[i].class = c
	}
	for range searchPasses {
		for i, c := range classes {
			for _, q := range c.queries {
				started := time.Now()
				answer, err := searcher.Search(ctx, q)
				elapsed := time.Since(started)
				switch {
				case errors.Is(err, search.ErrTookTooLong):
					results[i].timedOut++
				case err != nil:
					return nil, fmt.Errorf("search %q: %w", c.name, err)
				default:
					results[i].hits = append(results[i].hits, answer.Total)
				}
				results[i].times = append(results[i].times, elapsed)
			}
		}
	}
	return results, nil
}

// percentile is the nearest-rank percentile p of sorted values.
func percentile[T int | time.Duration](sorted []T, p float64) T {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[max(int(math.Ceil(p*float64(len(sorted))))-1, 0)]
}

func sortedCopy[T int | time.Duration](values []T) []T {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted
}

// memorySampler keeps the peak allocated memory of the search process in
// each phase. RssAnon counts allocated memory and leaves out the mapped
// index files, which the kernel can drop under pressure.
type memorySampler struct {
	program string

	mu    sync.Mutex
	phase int
	peak  [phaseCount]int64
	pid   int
}

func (m *memorySampler) setPhase(phase int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phase = phase
}

func (m *memorySampler) peaks() [phaseCount]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peak
}

func (m *memorySampler) run(ctx context.Context) {
	ticker := time.NewTicker(memoryEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sample()
		}
	}
}

func (m *memorySampler) sample() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pid == 0 {
		m.pid = childRunning(m.program)
		if m.pid == 0 {
			return
		}
	}
	bytes, err := rssAnon(m.pid)
	if err != nil {
		// It stopped, and the next sample looks for the new one.
		m.pid = 0
		return
	}
	m.peak[m.phase] = max(m.peak[m.phase], bytes)
}

// childRunning returns the PID of this process's child that runs program,
// or 0 when none does.
func childRunning(program string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	self := os.Getpid()
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// The command name in parentheses can hold spaces, so the fields
		// start after the last ")": state, then the parent's PID.
		end := strings.LastIndexByte(string(stat), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) < 2 || fields[1] != strconv.Itoa(self) {
			continue
		}
		if exe, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe")); err == nil && exe == program {
			return pid
		}
	}
	return 0
}

// rssAnon reads a process's RssAnon in bytes.
func rssAnon(pid int) (int64, error) {
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		value, found := strings.CutPrefix(scanner.Text(), "RssAnon:")
		if !found {
			continue
		}
		kib, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), " kB"), 10, 64)
		return kib << 10, err
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("no RssAnon for process %d", pid)
}

// versions names what was measured.
type versions struct {
	mailarkiv string
	goVersion string
	tantivy   string
}

// readVersions asks the search program for its hello, which names its
// Tantivy version, and reads this build's commit.
func readVersions(ctx context.Context, program, dataDir string) (versions, error) {
	v := versions{mailarkiv: "unknown", goVersion: runtime.Version()}
	if info, ok := debug.ReadBuildInfo(); ok {
		settings := map[string]string{}
		for _, setting := range info.Settings {
			settings[setting.Key] = setting.Value
		}
		if revision := settings["vcs.revision"]; revision != "" {
			v.mailarkiv = revision[:min(len(revision), 12)]
			if settings["vcs.modified"] == "true" {
				v.mailarkiv += " with uncommitted changes"
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program)
	cmd.Dir = dataDir
	cmd.Env = []string{}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return v, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return v, err
	}
	if err := cmd.Start(); err != nil {
		return v, err
	}
	var hello struct {
		Tantivy string `json:"tantivy"`
	}
	readErr := processing.ReadFrame(stdout, &hello)
	closeErr := stdin.Close()
	if err := errors.Join(readErr, closeErr, cmd.Wait()); err != nil {
		return v, fmt.Errorf("read the search program's hello: %w", err)
	}
	v.tantivy = hello.Tantivy
	return v, nil
}

// hostDescription names the cores, memory, processor, and kernel.
func hostDescription() string {
	description := fmt.Sprintf("%d cores", runtime.NumCPU())
	if cpuinfo, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for line := range strings.Lines(string(cpuinfo)) {
			if name, found := strings.CutPrefix(line, "model name"); found {
				description += " (" + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), ":")) + ")"
				break
			}
		}
	}
	if meminfo, err := os.ReadFile("/proc/meminfo"); err == nil {
		for line := range strings.Lines(string(meminfo)) {
			if value, found := strings.CutPrefix(line, "MemTotal:"); found {
				if kib, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), " kB"), 10, 64); err == nil {
					description += ", " + formatBytes(kib<<10) + " of memory"
				}
				break
			}
		}
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		description += ", Linux " + strings.TrimSpace(string(release))
	}
	return description
}

// appendResults appends one dated section to the results file.
func appendResults(path string, f findings) error {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("\n## Run of %s\n\n", time.Now().UTC().Format(time.DateOnly))
	w("| Item | Value |\n| --- | --- |\n")
	w("| Messages | %s |\n", formatCount(f.messages))
	w("| Host | %s |\n", f.host)
	w("| Versions | MailArkiv %s, %s, Tantivy %s |\n", f.versions.mailarkiv, f.versions.goVersion, f.versions.tantivy)
	w("| Processing and first index build | %s |\n", formatDuration(f.build))
	w("| Full rebuild | %s |\n", formatDuration(f.rebuild))
	peak := slices.Max(f.memoryPeaks[:])
	w("| Peak allocated memory of the search process | %s |\n", formatBytes(peak))
	w("| Index size | %s |\n", formatBytes(f.indexBytes))

	w("\n### Search times\n\n")
	w("Each search ran %d times, newest first, for the first page of %d rows. A time runs from the call to the search module until it returns the rows from the catalog. Percentiles are nearest rank.\n\n",
		searchPasses, archive.PageSize)
	w("| Searches | Runs | Median | 95th percentile | Stopped at 10 s |\n| --- | --- | --- | --- | --- |\n")
	var worstP95 time.Duration
	for _, group := range []struct {
		name   string
		prefix bool
	}{{"Without prefix words", false}, {"With prefix words", true}} {
		var times []time.Duration
		timedOut := 0
		for _, r := range f.searches {
			if r.class.prefix == group.prefix {
				times = append(times, r.times...)
				timedOut += r.timedOut
			}
		}
		sorted := sortedCopy(times)
		p95 := percentile(sorted, 0.95)
		worstP95 = max(worstP95, p95)
		w("| %s | %d | %s | %s | %d |\n", group.name, len(times),
			formatMillis(percentile(sorted, 0.5)), formatMillis(p95), timedOut)
	}
	w("\n| Search | Prefix words | Hits, median | Median | 95th percentile |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range f.searches {
		sorted := sortedCopy(r.times)
		prefix := "No"
		if r.class.prefix {
			prefix = "Yes"
		}
		w("| %s | %s | %s | %s | %s |\n", r.class.name, prefix, formatCount(percentile(sortedCopy(r.hits), 0.5)),
			formatMillis(percentile(sorted, 0.5)), formatMillis(percentile(sorted, 0.95)))
	}

	w("\n### Peak allocated memory by phase\n\n")
	w("RssAnon of the search process, read every %d ms.\n\n", memoryEvery.Milliseconds())
	w("| Phase | Peak |\n| --- | --- |\n")
	for phase, name := range phaseNames {
		w("| %s | %s |\n", name, formatBytes(f.memoryPeaks[phase]))
	}

	w("\n### Targets\n\n| Target | Result | Met |\n| --- | --- | --- |\n")
	w("| 95th percentile under 1 s | %s | %s |\n", formatMillis(worstP95), yesNo(worstP95 < targetP95))
	w("| Full rebuild within 8 hours | %s | %s |\n", formatDuration(f.rebuild), yesNo(f.rebuild <= targetRebuild))
	w("| Under 1 GiB allocated by search | %s | %s |\n", formatBytes(peak), yesNo(peak < targetMemory))

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(b.String())
	return errors.Join(writeErr, file.Close())
}

func yesNo(met bool) string {
	if met {
		return "Yes"
	}
	return "No"
}

func formatDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
}

func formatMillis(d time.Duration) string {
	return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
}

// formatCount writes n with a comma between each group of three digits.
func formatCount(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}
