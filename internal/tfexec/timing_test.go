//nolint:testpackage // the timing contracts are about the unexported decoder's pairing.
package tfexec

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The recorded stream's measured timestamps (testdata/verbose-stream.jsonl,
// recorded from Terraform v1.15.8). The run blocks and their file take these
// wall times between the events the stream already carries:
//
//	applied run    19:59:01.479722 → 01.502972   23 ms
//	defaults run   19:59:01.512001 → 01.517096    5 ms
//	apply file     19:59:01.478369 → 01.511840   33 ms
//	unit file      19:59:01.511877 → 01.518453    6 ms
func TestDecodePairsTheStreamTimestampsIntoRunAndFileTiming(t *testing.T) {
	t.Parallel()

	result, err := decodeTestStream(bytes.NewReader(recorded(t)))
	if err != nil {
		t.Fatalf("decoding the recorded stream: %v", err)
	}

	elapsedOf(t, result.Runs, "tests/apply.tftest.hcl", "applied", 23)
	elapsedOf(t, result.Runs, "tests/unit.tftest.hcl", "defaults", 5)

	if len(result.Files) != 2 {
		t.Fatalf("files = %d (%+v), want one entry per test file", len(result.Files), result.Files)
	}

	first := result.Files[0]
	if first.File != "tests/apply.tftest.hcl" || first.Runs != 1 || *first.ElapsedMS != 33 {
		t.Fatalf("first file = %+v, want tests/apply.tftest.hcl at 33 ms with one run", first)
	}

	second := result.Files[1]
	if second.File != "tests/unit.tftest.hcl" || second.Runs != 1 || *second.ElapsedMS != 6 {
		t.Fatalf("second file = %+v, want tests/unit.tftest.hcl at 6 ms with one run", second)
	}
}

// A run block skipped after an earlier error never starts: Terraform 1.15.8
// emits its completion without a starting event, so the stream carries no pair
// to measure and the run outcome carries no elapsed time.
func TestARunSkippedWithoutAStartingEventCarriesNoElapsed(t *testing.T) {
	t.Parallel()

	stream := dropLine(recorded(t), `"run":"applied","progress":"starting"`)

	result, err := decodeTestStream(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("decoding the stream: %v", err)
	}

	for _, run := range result.Runs {
		if run.Run == "applied" && run.ElapsedMS != nil {
			t.Fatalf("run %s carries elapsed %d ms without a starting event", run.Run, *run.ElapsedMS)
		}
	}

	if result.ExecutedRuns() != 2 {
		t.Fatalf("executed runs = %d, want the stream's two outcomes regardless of timing", result.ExecutedRuns())
	}
}

// An unparseable timestamp is a timing gap, not a malformed stream: timing
// decides nothing, so it degrades to absent rather than failing the mutant.
func TestAnUnparseableTimestampCarriesNoElapsedRatherThanFailingTheStream(t *testing.T) {
	t.Parallel()

	stream := bytes.Replace(
		recorded(t),
		[]byte(`"@timestamp":"2026-08-16T19:59:01.502972+01:00"`),
		[]byte(`"@timestamp":"not-a-timestamp"`),
		1,
	)

	result, err := decodeTestStream(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("an unparseable timestamp failed the stream: %v", err)
	}

	elapsedOf(t, result.Runs, "tests/unit.tftest.hcl", "defaults", 5)

	for _, run := range result.Runs {
		if run.Run == "applied" && run.ElapsedMS != nil {
			t.Fatalf("run %s carries elapsed %d ms from an unparseable completion timestamp", run.Run, *run.ElapsedMS)
		}
	}
}

// elapsedOf asserts one run block's measured elapsed time, in milliseconds.
func elapsedOf(t *testing.T, runs []RunOutcome, file, name string, want int64) {
	t.Helper()

	index := slices.IndexFunc(runs, func(run RunOutcome) bool {
		return run.File == file && run.Run == name
	})
	if index < 0 {
		t.Fatalf("no outcome for run %s", name)
	}

	measured := runs[index].ElapsedMS
	if measured == nil {
		t.Fatalf("run %s carries no elapsed time", name)
	}

	if *measured != want {
		t.Fatalf("run %s elapsed = %d ms, want %d", name, *measured, want)
	}
}

// dropLine removes the one recorded line naming the given fragment.
func dropLine(stream []byte, fragment string) []byte {
	kept := make([]byte, 0, len(stream))

	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		if strings.Contains(string(line), fragment) {
			continue
		}

		kept = append(kept, line...)
		kept = append(kept, '\n')
	}

	return kept
}
