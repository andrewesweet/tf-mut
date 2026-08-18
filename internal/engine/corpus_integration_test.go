//go:build integration

package engine_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// The M4.5-0 synthesis-rate measurement, which gates M4.5b's start.
//
// It runs the shipped preference pipeline — defaults, then validation mining,
// then typed synthesis, every candidate checked statically against the
// module's own validations — over a pinned corpus of public modules, and
// publishes the rate at which a module yields an executable default scenario
// rather than a judgement point.
//
// The decision rule is stated so the gate can fail: **if the median corpus
// module yields no executable default scenario without a TODO answer**, the
// `--answer` batch path becomes a mandatory M4.5b deliverable and the product
// claim is reworded from the measured rate. Otherwise the milestone ships as
// specified. Either way the number can change the plan, which is what a gate
// means.
//
// The measurement is network-gated: it needs the corpus archives, and this is
// the only test in the suite that reaches the network.

const (
	corpusManifest  = "../../research/corpus/m45-synthesis.json"
	corpusOutput    = "../../.artifacts/measurement/m45-synthesis.json"
	corpusTimeout   = 2 * time.Minute
	corpusUserAgent = "tf-mut-measurement"
)

type corpusModule struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	SHA256     string `json:"sha256"`
}

type corpus struct {
	Description string         `json:"description"`
	Modules     []corpusModule `json:"modules"`
}

// moduleMeasurement is one module's published row.
type moduleMeasurement struct {
	Module string `json:"module"`
	Tag    string `json:"tag"`
	// Variables is the number of inputs the root module declares.
	Variables int `json:"variables"`
	// Resolved is the number the pipeline found a value for, by rung.
	Defaults int `json:"defaults"`
	Mined    int `json:"mined"`
	Typed    int `json:"typed"`
	// Todos is the number of open judgement points.
	Todos int `json:"todos"`
	// Executable reports a default scenario that needs no answer at all.
	Executable bool `json:"executable_default_scenario"`
	// TodoRate is Todos ÷ Variables.
	TodoRate float64 `json:"todo_rate"`
	// Refused records a module the pipeline could not read at all, which is a
	// measurement outcome rather than an error to hide.
	Refused string `json:"refused,omitempty"`
}

type corpusMeasurement struct {
	Corpus       string              `json:"corpus"`
	Modules      []moduleMeasurement `json:"modules"`
	Measured     int                 `json:"measured"`
	Executable   int                 `json:"executable_modules"`
	Refused      int                 `json:"refused_modules"`
	MedianTodos  int                 `json:"median_open_todos"`
	MedianIsZero bool                `json:"median_module_is_executable"`
	Decision     string              `json:"decision"`
}

func TestTheSynthesisRateOverThePinnedCorpus(t *testing.T) {
	t.Parallel()
	requireRealInfrastructureOptIn(t)

	loaded := loadCorpus(t)
	cache := t.TempDir()

	measurement := corpusMeasurement{
		Corpus: corpusManifest, Modules: []moduleMeasurement{},
		Measured: 0, Executable: 0, Refused: 0,
		MedianTodos: 0, MedianIsZero: false, Decision: "",
	}

	for _, module := range loaded.Modules {
		measurement.Modules = append(measurement.Modules,
			measureModule(t, module, fetchModule(t, module, cache)))
	}

	summarise(&measurement)
	publishMeasurement(t, measurement)

	t.Logf("median open judgement points per module: %d (executable modules %d of %d, refused %d)",
		measurement.MedianTodos, measurement.Executable, measurement.Measured, measurement.Refused)
	t.Logf("decision: %s", measurement.Decision)
}

// measureModule runs the shipped pipeline through the engine seam.
func measureModule(t *testing.T, module corpusModule, dir string) moduleMeasurement {
	t.Helper()

	row := moduleMeasurement{
		Module: module.Name, Tag: module.Tag,
		Variables: 0, Defaults: 0, Mined: 0, Typed: 0, Todos: 0,
		Executable: false, TodoRate: 0, Refused: "",
	}

	config := baseConfig(t, dir)
	config.Todos = true

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		row.Refused = err.Error()

		return row
	}

	block := result.Characterisation
	row.Todos = block.OpenTodos()

	for _, scenario := range block.Scenarios {
		if scenario.Name != "defaults" {
			continue
		}

		for _, input := range scenario.Inputs {
			switch input.Provenance {
			case report.FromValidation:
				row.Mined++
			case report.FromType:
				row.Typed++
			case report.FromDefault, report.FromAnswer:
				// A default needs no assignment and an answer is not in play
				// here: neither rung is part of the synthesis rate.
				continue
			default:
				t.Fatalf("unknown input provenance %q", input.Provenance)
			}
		}
	}

	row.Variables = countVariables(t, dir)
	row.Defaults = row.Variables - row.Mined - row.Typed - row.Todos
	row.Executable = row.Todos == 0

	if row.Variables > 0 {
		row.TodoRate = float64(row.Todos) / float64(row.Variables)
	}

	return row
}

// countVariables counts the root module's declared inputs, which is the
// denominator the published rate is expressed over.
func countVariables(t *testing.T, dir string) int {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	count := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tf") {
			continue
		}

		content, readErr := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // a corpus-owned temporary path.
		if readErr != nil {
			t.Fatalf("reading %s: %v", entry.Name(), readErr)
		}

		count += strings.Count(string(content), "\nvariable \"")
		if strings.HasPrefix(string(content), "variable \"") {
			count++
		}
	}

	return count
}

func summarise(measurement *corpusMeasurement) {
	counts := []int{}

	for _, row := range measurement.Modules {
		if row.Refused != "" {
			measurement.Refused++

			continue
		}

		measurement.Measured++

		if row.Executable {
			measurement.Executable++
		}

		counts = append(counts, row.Todos)
	}

	slices.Sort(counts)

	if len(counts) > 0 {
		measurement.MedianTodos = counts[len(counts)/2]
	}

	measurement.MedianIsZero = len(counts) > 0 && measurement.MedianTodos == 0

	measurement.Decision = "the median corpus module yields an executable default scenario " +
		"with no TODO answer: the milestone ships as specified"
	if !measurement.MedianIsZero {
		measurement.Decision = "the median corpus module yields no executable default scenario " +
			"without a TODO answer: the --answer batch path is a mandatory M4.5b deliverable " +
			"and the product claim is reworded from the measured rate"
	}
}

func publishMeasurement(t *testing.T, measurement corpusMeasurement) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(corpusOutput), 0o750); err != nil {
		t.Fatalf("creating the measurement directory: %v", err)
	}

	encoded, err := json.MarshalIndent(measurement, "", "  ")
	if err != nil {
		t.Fatalf("encoding the measurement: %v", err)
	}

	if err := os.WriteFile(corpusOutput, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the measurement: %v", err)
	}
}

func loadCorpus(t *testing.T) corpus {
	t.Helper()

	content, err := os.ReadFile(corpusManifest)
	if err != nil {
		t.Fatalf("reading the corpus manifest: %v", err)
	}

	loaded := corpus{Description: "", Modules: nil}
	if err := json.Unmarshal(content, &loaded); err != nil {
		t.Fatalf("decoding the corpus manifest: %v", err)
	}

	return loaded
}

// fetchModule downloads a pinned archive, checks its digest and extracts the
// root module. A digest that does not match is a failure and never a warning:
// the whole point of pinning is that the measurement cannot drift.
func fetchModule(t *testing.T, module corpusModule, cache string) string {
	t.Helper()

	url := fmt.Sprintf("https://codeload.github.com/%s/tar.gz/refs/tags/%s",
		module.Repository, module.Tag)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building the request for %s: %v", module.Name, err)
	}

	request.Header.Set("User-Agent", corpusUserAgent)

	client := http.Client{Timeout: corpusTimeout}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("fetching %s: %v", module.Name, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("fetching %s: %s", module.Name, response.Status)
	}

	archive, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", module.Name, err)
	}

	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != module.SHA256 {
		t.Fatalf("%s: digest %s does not match the pinned %s",
			module.Name, hex.EncodeToString(sum[:]), module.SHA256)
	}

	return extract(t, archive, filepath.Join(cache, module.Name))
}

// extract unpacks the archive's root directory and returns it.
func extract(t *testing.T, archive []byte, target string) string {
	t.Helper()

	reader, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		t.Fatalf("opening the archive: %v", err)
	}

	defer func() { _ = reader.Close() }()

	root := ""
	entries := tar.NewReader(reader)

	for {
		header, readErr := entries.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			t.Fatalf("reading the archive: %v", readErr)
		}

		name := filepath.Clean(header.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			t.Fatalf("the archive escapes its root: %s", header.Name)
		}

		// GitHub's archives lead with a pax global header, which is not part
		// of the module and is not the root directory either.
		if name == "pax_global_header" {
			continue
		}

		if root == "" {
			root = strings.Split(name, string(filepath.Separator))[0]
		}

		extractEntry(t, entries, header, filepath.Join(target, name))
	}

	return filepath.Join(target, root)
}

// extractEntry writes one archive member.
func extractEntry(t *testing.T, entries *tar.Reader, header *tar.Header, path string) {
	t.Helper()

	if header.Typeflag == tar.TypeDir {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}

		return
	}

	if header.Typeflag != tar.TypeReg {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	content, err := io.ReadAll(entries)
	if err != nil {
		t.Fatalf("reading %s: %v", header.Name, err)
	}

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
