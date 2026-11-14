package collect

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// fakeSync stands in for a sync. Each one reports its start and ends with
// the error the test sends.
type fakeSync struct {
	started chan time.Time
	results chan error
}

func (s *fakeSync) collect(ctx context.Context, _ *runner) (int, error) {
	select {
	case s.started <- time.Now():
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case err := <-s.results:
		return 0, err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// schedule runs one source's loop with a fake sync, in a synctest bubble
// where time.Now is the bubble's clock.
type schedule struct {
	t         *testing.T
	f         *fixture
	deps      Deps
	id        int64
	sync      *fakeSync
	collector *Collector
}

func newSchedule(t *testing.T) *schedule {
	f := newFixture(t)
	deps := f.deps()
	deps.Now = time.Now
	s := &schedule{t: t, f: f, deps: deps, sync: &fakeSync{started: make(chan time.Time), results: make(chan error)}}
	s.id = insertSource(t, deps, SourceInput{
		Name: "Anna", Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: testUser, Password: testPassword, Folders: []string{"INBOX"},
	})
	s.start()
	return s
}

// start opens a new loop on the catalog.
func (s *schedule) start() {
	collector, err := open(context.Background(), s.deps, testConfig)
	if err != nil {
		s.t.Fatal(err)
	}
	collector.collectFn = s.sync.collect
	collector.Start()
	s.t.Cleanup(collector.Stop)
	s.collector = collector
}

var errStalled = &failure{category: TimedOut, err: errors.New("stalled")}

// end ends the running sync with err, and checks that the next one starts
// wait later, as the source said while it waited.
func (s *schedule) end(err error, wait time.Duration) {
	s.t.Helper()
	ended := time.Now()
	s.sync.results <- err
	synctest.Wait()
	source, getErr := s.collector.Get(context.Background(), s.id)
	if getErr != nil {
		s.t.Fatal(getErr)
	}
	if want := ended.Add(wait); source.NextSyncAt == nil || !source.NextSyncAt.Equal(want) {
		s.t.Errorf("next sync at %v, want %v later", source.NextSyncAt, wait)
	}
	if started := <-s.sync.started; started.Sub(ended) != wait {
		s.t.Errorf("the next sync started %v after the last ended, want %v", started.Sub(ended), wait)
	}
}

func TestSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSchedule(t)
		if started := <-s.sync.started; !started.Equal(time.Now()) {
			t.Fatal("the first sync didn't start at once")
		}

		// A success waits 5 minutes. Failures in a row wait longer each
		// time, up to an hour. A success returns to 5 minutes.
		s.end(nil, 5*time.Minute)
		for _, wait := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
			s.end(errStalled, wait*time.Minute)
		}
		s.end(nil, 5*time.Minute)

		// A wake-up during a wait syncs at once.
		s.sync.results <- nil
		synctest.Wait()
		s.collector.wake(s.id)
		if started := <-s.sync.started; !started.Equal(time.Now()) {
			t.Error("the woken sync didn't start at once")
		}

		// A wake-up during a sync starts no second one.
		s.collector.wake(s.id)
		s.end(nil, 5*time.Minute)

		// A new loop syncs at start and goes on from the stored count.
		s.end(errStalled, time.Minute)
		s.end(errStalled, 2*time.Minute)
		s.sync.results <- errStalled
		synctest.Wait()
		before := time.Now()
		s.collector.Stop()
		if time.Now() != before {
			t.Errorf("Stop during a wait took %v", time.Since(before))
		}
		s.start()
		if started := <-s.sync.started; !started.Equal(time.Now()) {
			t.Error("the new loop didn't sync at start")
		}
		s.end(errStalled, 8*time.Minute)
	})
}
