//nolint:testpackage // the memory gate measures an unexported decoder.
package tfexec

import (
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedStream is a real `terraform test -verbose -json` stream, recorded
// from Terraform v1.15.8 against a two-run fixture. The malformed cases below
// manipulate it rather than synthesising output no Terraform ever produced.
const recordedStream = "testdata/verbose-stream.jsonl"

func TestDecodeKeepsRunOutcomesAndPayloadsAndDropsProviderSchemas(t *testing.T) {
	t.Parallel()

	result, err := decodeTestStream(bytes.NewReader(recorded(t)))
	if err != nil {
		t.Fatalf("decoding the recorded stream: %v", err)
	}

	if result.ExecutedRuns() != 2 {
		t.Fatalf("executed runs = %d, want 2 (%v)", result.ExecutedRuns(), result.Runs)
	}

	if len(result.Payloads) != 2 {
		t.Fatalf("payloads = %d, want one per run block", len(result.Payloads))
	}

	kinds := map[string]bool{}

	for _, payload := range result.Payloads {
		kinds[payload.Kind] = true

		if payload.Run == "" || payload.File == "" {
			t.Fatalf("payload %+v is not attributed to a run block", payload)
		}

		if _, found := payload.Value[fieldProviders]; found {
			t.Fatalf("payload for %s retained %s", payload.Run, fieldProviders)
		}
	}

	if !kinds[PayloadPlan] || !kinds[PayloadState] {
		t.Fatalf("payload kinds = %v, want both a plan and a state", kinds)
	}
}

func TestTruncatedStreamIsAnErrorRatherThanAnEmptyResult(t *testing.T) {
	t.Parallel()

	stream := recorded(t)

	if _, err := decodeTestStream(bytes.NewReader(stream[:len(stream)*2/3])); !errors.Is(err, ErrMalformedStream) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedStream)
	}
}

func TestMalformedStreamIsAnErrorRatherThanAnEmptyResult(t *testing.T) {
	t.Parallel()

	corrupted := bytes.Replace(recorded(t), []byte(`"type":"test_plan"`), []byte(`"type":!!!`), 1)

	if _, err := decodeTestStream(bytes.NewReader(corrupted)); !errors.Is(err, ErrMalformedStream) {
		t.Fatalf("error = %v, want %v", err, ErrMalformedStream)
	}
}

// The streaming memory gate from issue #21: a stream of at least 100 MB must
// decode with peak retained engine heap under 64 MB at four concurrent workers.
//
// The bound is chosen so that a decoder which materialises a message before
// discarding `provider_schemas` fails by construction: one inflated message is
// itself larger than the whole budget.
const (
	gateStreamBytes  = 100 * 1024 * 1024
	gateHeapCeiling  = 64 * 1024 * 1024
	gateWorkers      = 4
	gateSchemaBytes  = 8 * 1024 * 1024
	gateSchemaString = 64 * 1024
	liveHeapMetric   = "/gc/heap/live:bytes"
	heapSamplePeriod = 2 * time.Millisecond
)

//nolint:paralleltest // the measurement is of this process's heap.
func TestVerboseDecodeOfALargeStreamStaysWithinTheHeapCeiling(t *testing.T) {
	// Deliberately not parallel: the measurement is of this process's heap.
	//
	// One inflated message is itself larger than a quarter of the ceiling, so a
	// decoder that materialised a message before discarding its provider schemas
	// would fail this by construction rather than by tuning.
	inflated := inflate(recorded(t))
	repeats := gateStreamBytes/len(inflated) + 1

	peak := measurePeakLiveHeap(t, func() {
		group := sync.WaitGroup{}

		for range gateWorkers {
			group.Go(func() {
				result, err := decodeTestStream(repeatReader(inflated, repeats))
				if err != nil {
					t.Errorf("decoding the inflated stream: %v", err)

					return
				}

				if len(result.Payloads) == 0 {
					t.Error("the inflated stream decoded no payloads")
				}
			})
		}

		group.Wait()
	})

	if peak > gateHeapCeiling {
		t.Fatalf("peak retained heap %d bytes exceeds the %d byte ceiling at %d workers",
			peak, gateHeapCeiling, gateWorkers)
	}
}

// inflate grows the recorded stream's `provider_schemas` members to the size a
// large-provider run produces, keeping every other byte of real output.
func inflate(stream []byte) []byte {
	entries := make([]string, 0, gateSchemaBytes/gateSchemaString)
	for range gateSchemaBytes / gateSchemaString {
		entries = append(entries, `"`+strings.Repeat("s", gateSchemaString)+`"`)
	}

	filler := `"filler":[` + strings.Join(entries, ",") + `],`

	return bytes.ReplaceAll(stream,
		[]byte(`"provider_schemas":{`),
		[]byte(`"provider_schemas":{`+filler))
}

// repeatReader streams one buffer n times without copying it, so that the
// measurement sees the decoder's retention rather than the harness's.
func repeatReader(content []byte, times int) io.Reader {
	readers := make([]io.Reader, 0, times)
	for range times {
		readers = append(readers, bytes.NewReader(content))
	}

	return io.MultiReader(readers...)
}

// measurePeakLiveHeap samples the live heap — the bytes the collector cannot
// reclaim — rather than process RSS, which on a mutation run is dominated by
// Terraform and its provider plugins and says nothing about this decoder.
func measurePeakLiveHeap(t *testing.T, work func()) uint64 {
	t.Helper()

	// The baseline has to be a collected heap, or the peak would include
	// whatever the previous test left behind.
	runtime.GC() //nolint:revive // deliberate: see above.

	done := make(chan struct{})
	peaks := make(chan uint64, 1)

	go func() {
		sample := []metrics.Sample{{Name: liveHeapMetric}} //nolint:exhaustruct // filled by the runtime.
		peak := uint64(0)

		for {
			select {
			case <-done:
				peaks <- peak

				return
			default:
			}

			metrics.Read(sample)

			if value := sample[0].Value.Uint64(); value > peak {
				peak = value
			}

			time.Sleep(heapSamplePeriod)
		}
	}()

	work()
	close(done)

	return <-peaks
}

func recorded(t *testing.T) []byte {
	t.Helper()

	content, err := os.ReadFile(recordedStream)
	if err != nil {
		t.Fatalf("reading %s: %v", recordedStream, err)
	}

	return content
}
