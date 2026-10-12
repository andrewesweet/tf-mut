package engine

import (
	"bytes"
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

//nolint:gochecknoglobals // test seam, inert outside the suite.
var progressInterval = DefaultProgressInterval

type synchronizedWriter struct {
	mutex  sync.Mutex
	writer io.Writer
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return w.writer.Write(p)
}

// runPrefixWriter is the one stderr channel of a run: every line the engine
// itself writes — progress, tracing warnings, CPU-profile warnings — carries
// the run identifier as a `[<run_id>] ` prefix, so a reader can tie stderr to
// the report that publishes the same identifier in its telemetry block. Lines
// written before the identifier is minted — the tracer constructor's
// warnings, which precede the run span — are held and released under it.
// A nil writer, and every method on a nil receiver, writes nothing.
type runPrefixWriter struct {
	mutex       sync.Mutex
	writer      io.Writer
	prefix      string
	atLineStart bool
	pending     []byte
}

// newRunPrefixWriter wraps the run's stderr writer.
func newRunPrefixWriter(writer io.Writer) *runPrefixWriter {
	return &runPrefixWriter{writer: writer, atLineStart: true}
}

// Write prefixes every line with the run identifier. Best effort by
// contract: a line nobody could read must never fail a run.
func (w *runPrefixWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}

	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.prefix == "" {
		w.pending = append(w.pending, p...)

		return len(p), nil
	}

	return w.emit(p)
}

// start sets the run identifier and releases whatever was written before it
// existed. Safe to call once, immediately after the run span starts.
func (w *runPrefixWriter) start(identifier string) {
	if w == nil {
		return
	}

	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.prefix = "[" + identifier + "] "
	pending := w.pending
	w.pending = nil
	_, _ = w.emit(pending)
}

// emit writes p through to the underlying writer, prefixing each line start.
// A line and its prefix leave as one write, so a consumer reading writes —
// not only bytes — sees whole prefixed lines.
func (w *runPrefixWriter) emit(p []byte) (int, error) {
	written := 0

	for len(p) > 0 {
		chunk := []byte{}
		if w.atLineStart {
			chunk = append(chunk, w.prefix...)
			w.atLineStart = false
		}

		line := p
		if index := bytes.IndexByte(p, '\n'); index >= 0 {
			line = p[:index+1]
			p = p[index+1:]
			w.atLineStart = true
		} else {
			p = nil
		}

		chunk = append(chunk, line...)
		if _, err := w.writer.Write(chunk); err != nil {
			return written, err
		}

		written += len(line)
	}

	return written, nil
}

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
	interval := progressInterval
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
