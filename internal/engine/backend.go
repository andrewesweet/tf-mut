package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// neutraliseBackends drops every root-module backend declaration from the
// staged copy the warm workspace initialises, in place, before init runs.
//
// tf-mut always initialises with -backend=false, and a root that declares a
// backend — even a vacuous `backend "azurerm" {}` with no configured
// credentials anywhere — makes the next command in that workspace demand
// backend initialisation. Providers schema is that next command, so a
// backend-declaring root never gets its schema and the run dies before the
// baseline. The staged copy is tf-mut's own sandbox: the source tree is read
// only and is never touched. A file is rewritten only when it declares a
// backend, so every other staged file stays byte-identical.
//
// Mutant sandboxes keep the declaration: they materialise from the original
// sources, share only the warm data directory, and run terraform test, which
// runs green on such a tree after the warm workspace has been initialised
// without one.
func neutraliseBackends(rootDir string) error {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return fmt.Errorf("reading staged module: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		path := filepath.Join(rootDir, name)

		var stripErr error
		switch {
		case strings.HasSuffix(name, ".tf"):
			_, stripErr = stripHCLBackend(path)
		case strings.HasSuffix(name, ".tf.json"):
			_, stripErr = stripJSONBackend(path)
		default:
			continue
		}

		if stripErr != nil {
			return stripErr
		}
	}

	return nil
}

// backendBlock names the terraform sub-block that declares a backend, in
// both syntaxes.
const backendBlock = "backend"

// stagedFileMode is the permission the staged workspace's rewrites carry.
const stagedFileMode = 0o600

// stripHCLBackend removes every backend block from every terraform block in
// one staged .tf file, and reports whether it rewrote the file.
func stripHCLBackend(path string) (bool, error) {
	name := filepath.Base(path)

	source, err := os.ReadFile(path) //nolint:gosec // the staged module's own path.
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", name, err)
	}

	file, diagnostics := hclwrite.ParseConfig(source, name, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return false, fmt.Errorf("parsing %s: %w", name, diagnostics)
	}

	stripped := false
	for _, block := range file.Body().Blocks() {
		if block.Type() != "terraform" {
			continue
		}

		for _, declared := range block.Body().Blocks() {
			if declared.Type() == backendBlock {
				block.Body().RemoveBlock(declared)
				stripped = true
			}
		}
	}

	if !stripped {
		return false, nil
	}

	//nolint:gosec // the staged module's own path.
	if err := os.WriteFile(path, file.Bytes(), stagedFileMode); err != nil {
		return false, fmt.Errorf("writing %s: %w", name, err)
	}

	return true, nil
}

// stripJSONBackend removes the backend key from every terraform block of one
// staged .tf.json file, and reports whether it rewrote the file. Terraform's
// JSON configuration syntax lets a block type map to one object or to an
// array of them; both spellings appear in the wild.
func stripJSONBackend(path string) (bool, error) {
	name := filepath.Base(path)

	source, readErr := os.ReadFile(path) //nolint:gosec // the staged module's own path.
	if readErr != nil {
		return false, fmt.Errorf("reading %s: %w", name, readErr)
	}

	document := map[string]json.RawMessage{}
	if err := json.Unmarshal(source, &document); err != nil {
		return false, fmt.Errorf("decoding %s: %w", name, err)
	}

	blocks, declared := document["terraform"]
	if !declared {
		return false, nil
	}

	rewritten, stripped, err := stripJSONBackendBlocks(blocks)
	if err != nil {
		return false, fmt.Errorf("decoding %s: %w", name, err)
	}

	if !stripped {
		return false, nil
	}

	document["terraform"] = rewritten

	encoded, err := json.Marshal(document)
	if err != nil {
		return false, fmt.Errorf("encoding %s: %w", name, err)
	}

	if err := os.WriteFile(path, encoded, stagedFileMode); err != nil {
		return false, fmt.Errorf("writing %s: %w", name, err)
	}

	return true, nil
}

// stripJSONBackendBlocks removes the backend key from the terraform payload
// of a .tf.json document, in either legal spelling, and returns the rewritten
// payload and whether anything changed.
func stripJSONBackendBlocks(blocks json.RawMessage) (json.RawMessage, bool, error) {
	object := map[string]json.RawMessage{}
	if err := json.Unmarshal(blocks, &object); err == nil {
		if _, found := object[backendBlock]; !found {
			return nil, false, nil
		}

		delete(object, backendBlock)

		rewritten, marshalErr := json.Marshal(object)

		return rewritten, marshalErr == nil, marshalErr
	}

	array := []map[string]json.RawMessage{}
	if err := json.Unmarshal(blocks, &array); err != nil {
		return nil, false, err
	}

	stripped := false
	for _, block := range array {
		if _, found := block[backendBlock]; found {
			delete(block, backendBlock)
			stripped = true
		}
	}

	if !stripped {
		return nil, false, nil
	}

	rewritten, marshalErr := json.Marshal(array)

	return rewritten, marshalErr == nil, marshalErr
}
