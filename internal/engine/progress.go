package engine

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultProgressInterval is how often a run with a progress writer reports
// where it is. A runner polling more often than this gains nothing: the line
// is for a human glancing at a long run, not for a driver.
const DefaultProgressInterval = 10 * time.Second

// progressTracker writes the periodic long-run line — n/N mutants, elapsed,
// ETA — to the writer the caller pointed at the run's progress. A nil writer
// records nothing and costs one nil check per mutant; the CLI passes
// os.Stderr, which is never a report sink, so the line can never corrupt a
// report. Tests stay quiet by leaving the writer nil.
type progressTracker struct {
	total    int
	done     atomic.Int64
	writer   io.Writer
	interval time.Duration
	started  time.Time
	stop     chan struct{}
	loopDone chan struct{}
	finished sync.Once
	printed  bool
}

// startProgress begins the tracker for the pending population. It returns
// nil — and costs nothing — when there is no writer or nothing pending. The
// caller defers the returned tracker's stop exactly once.
func startProgress(ctx context.Context, plan executionPlan, pending int) *progressTracker {
	interval := plan.settings.ProgressIntervalForTesting
	if interval <= 0 {
		interval = DefaultProgressInterval
	}

	if plan.settings.Progress == nil || pending == 0 {
		return nil
	}

	tracker := &progressTracker{
		total:    pending,
		writer:   plan.settings.Progress,
		interval: interval,
		started:  time.Now(),
		stop:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}

	go tracker.loop(ctx)

	return tracker
}

// observe counts one finished mutant's evaluation. Safe on a nil tracker: the
// no-writer run pays a nil check per mutant and nothing else.
func (t *progressTracker) observe(MutantEvaluated) error {
	if t != nil {
		t.done.Add(1)
	}

	return nil
}

// stopLine ends the tracker's goroutine and writes the final count, so the
// reader sees the run reach its total and not a stale penultimate line. Safe
// on a nil tracker and more than once.
func (t *progressTracker) stopLine() {
	if t == nil {
		return
	}

	t.finished.Do(func() {
		close(t.stop)
		<-t.loopDone
		if t.printed {
			t.write()
		}
	})
}

func (t *progressTracker) loop(ctx context.Context) {
	defer close(t.loopDone)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stop:
			return
		case <-ticker.C:
			t.write()
		}
	}
}

// write emits one line. Elapsed and ETA are second-resolution: the line is
// read by a person, and a wall of changing milliseconds is noise.
func (t *progressTracker) write() {
	done := int(t.done.Load())
	elapsed := time.Since(t.started)

	var remaining string
	if done > 0 {
		pace := elapsed / time.Duration(done)
		eta := (pace * time.Duration(t.total-done)).Round(time.Second)
		remaining = "~" + eta.String()
	} else {
		remaining = "unknown"
	}

	// Best effort by contract: a progress line nobody could read must never
	// fail a run that was otherwise fine.
	n, _ := fmt.Fprintf(t.writer, "tf-mut: %d/%d mutants, %s elapsed, ETA %s\n",
		done, t.total, elapsed.Round(time.Second), remaining)
	if n > 0 {
		t.printed = true
	}
}
