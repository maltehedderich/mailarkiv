package processing

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// fakeWait stands in for the clock the interactive wait reads. It notes the
// wait asked for, and the wait ends when the test fires it.
type fakeWait struct {
	asked chan time.Duration
	fire  chan time.Time
}

func newFakeWait() *fakeWait {
	return &fakeWait{asked: make(chan time.Duration, 1), fire: make(chan time.Time, 1)}
}

func (f *fakeWait) after(d time.Duration) <-chan time.Time {
	f.asked <- d
	return f.fire
}

// busyWorkers counts the workers a request holds.
func (p *Pool) busyWorkers() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	busy := 0
	for w := range p.workers {
		if w.busy {
			busy++
		}
	}
	return busy
}

func TestInteractivePoolBusy(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	// The full limit, so the hung workers stay busy until the test ends them.
	pool, err := NewInteractivePool(InteractiveWorkers, MessageLimit, InteractiveWait, modeWorkerCommand(marker, "hang"), slogDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	wait := newFakeWait()
	pool.after = wait.after

	original := plainFixture(t, "plain-text.eml")
	hung, stop := context.WithCancel(context.Background())
	defer stop()
	results := make(chan error, InteractiveWorkers)
	for range InteractiveWorkers {
		go func() {
			_, err := pool.read(hung, bytes.NewReader(original), int64(len(original)), false)
			results <- err
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for pool.busyWorkers() != InteractiveWorkers {
		if time.Now().After(deadline) {
			t.Fatalf("%d workers busy after 10 s, want %d", pool.busyWorkers(), InteractiveWorkers)
		}
		time.Sleep(5 * time.Millisecond)
	}

	third := make(chan error, 1)
	go func() {
		_, err := pool.read(context.Background(), bytes.NewReader(original), int64(len(original)), false)
		third <- err
	}()
	// Only a request that finds no idle worker starts the wait.
	if got := <-wait.asked; got != 30*time.Second {
		t.Fatalf("the third request waits up to %s, want 30 s", got)
	}
	select {
	case err := <-third:
		t.Fatalf("the third request returned %v before its wait ended", err)
	case <-time.After(100 * time.Millisecond):
	}
	wait.fire <- time.Now()
	select {
	case err := <-third:
		if !errors.Is(err, ErrBusy) {
			t.Errorf("the third request returned %v, want ErrBusy", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the third request didn't return after its wait")
	}

	stop()
	for range InteractiveWorkers {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Errorf("a hung request returned %v, want context.Canceled", err)
		}
	}
}

// The background pool has no wait: a request waits as long as its context
// allows.
func TestBackgroundPoolWaits(t *testing.T) {
	pool := newTestPool(t, 1, slogDiscard())
	asked := false
	pool.after = func(time.Duration) <-chan time.Time {
		asked = true
		return nil
	}
	original := plainFixture(t, "plain-text.eml")
	readThrough(t, pool, original, false)
	if asked {
		t.Error("a background request started a wait")
	}
}
