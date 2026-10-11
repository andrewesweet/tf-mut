package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/sandbox"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

type backendRewrite struct {
	target   string
	source   string
	original []byte
}

// neutraliseBackends drops every root-module backend declaration from the
// staged copy the warm workspace initialises, before init runs.
//
// tf-mut always initialises with -backend=false, and a root that declares a
// backend — even a vacuous `backend "azurerm" {}` with no configured
// credentials anywhere — makes the next command in that workspace demand
// backend initialisation. Providers schema is that next command, so a
// backend-declaring root never gets its schema and the run dies before the
// baseline. The staged copy is tf-mut's own sandbox: the source tree is read
// only and is never touched. Files unaffected by backend neutralisation
// stay byte-identical.
//
// Mutant sandboxes keep the declaration: they materialise from the original
// sources, share only the warm data directory, and run terraform test, which
// runs green on such a tree after the warm workspace has been initialised
// without one.
func neutraliseBackends(rootDir, sourceDir string) ([]backendRewrite, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("reading staged module: %w", err)
	}

	backendOverride := ""
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !isOverrideFile(entry.Name()) {
			continue
		}

		declared, declarationErr := declaresBackend(filepath.Join(rootDir, entry.Name()))
		if declarationErr != nil {
			return nil, declarationErr
		}

		if declared {
			backendOverride = entry.Name()
		}
	}

	rewrites := []backendRewrite{}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		name := entry.Name()
		path := filepath.Join(rootDir, name)
		source := filepath.Join(sourceDir, name)
		removeCloud := backendOverride != "" && (!isOverrideFile(name) || name < backendOverride)

		var original []byte
		var stripErr error
		switch {
		case strings.HasSuffix(name, ".tf"):
			original, stripErr = stripHCLBackend(path, source, removeCloud)
		case strings.HasSuffix(name, ".tf.json"):
			original, stripErr = stripJSONBackend(path, source, removeCloud)
		default:
			continue
		}

		if stripErr != nil {
			return nil, stripErr
		}

		if original != nil {
			rewrites = append(rewrites, backendRewrite{target: path, source: source, original: original})
		}
	}

	return rewrites, nil
}

func isOverrideFile(name string) bool {
	return name == "override.tf" || name == "override.tf.json" ||
		strings.HasSuffix(name, "_override.tf") || strings.HasSuffix(name, "_override.tf.json")
}

func declaresBackend(path string) (bool, error) {
	parser := hclparse.NewParser()
	var file *hcl.File
	var diagnostics hcl.Diagnostics
	if strings.HasSuffix(path, ".tf.json") {
		file, diagnostics = parser.ParseJSONFile(path)
	} else {
		file, diagnostics = parser.ParseHCLFile(path)
	}

	if diagnostics.HasErrors() {
		return false, fmt.Errorf("parsing %s: %w", filepath.Base(path), diagnostics)
	}

	terraform, _, diagnostics := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "terraform"}},
	})
	if diagnostics.HasErrors() {
		return false, fmt.Errorf("reading terraform blocks in %s: %w", filepath.Base(path), diagnostics)
	}

	for _, block := range terraform.Blocks {
		content, _, blockDiagnostics := block.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: backendBlock, LabelNames: []string{"type"}}},
		})
		if blockDiagnostics.HasErrors() {
			return false, fmt.Errorf("reading backend blocks in %s: %w", filepath.Base(path), blockDiagnostics)
		}

		if len(content.Blocks) > 0 {
			return true, nil
		}
	}

	return false, nil
}

// backendBlock names the terraform sub-block that declares a backend, in
// both syntaxes.
const backendBlock = "backend"

// stripHCLBackend removes backend blocks and, when requested, cloud blocks
// from a staged .tf file, returning the original bytes when it rewrites the file.
func stripHCLBackend(path, sourcePath string, removeCloud bool) ([]byte, error) {
	name := filepath.Base(path)

	source, err := os.ReadFile(path) //nolint:gosec // the staged module's own path.
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	file, diagnostics := hclwrite.ParseConfig(source, name, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parsing %s: %w", name, diagnostics)
	}

	stripped := false
	for _, block := range file.Body().Blocks() {
		if block.Type() != "terraform" {
			continue
		}

		for _, declared := range block.Body().Blocks() {
			if declared.Type() == backendBlock || removeCloud && declared.Type() == "cloud" {
				block.Body().RemoveBlock(declared)
				stripped = true
			}
		}
	}

	if !stripped {
		return nil, nil
	}

	if err := sandbox.WriteFresh(path, sourcePath, file.Bytes()); err != nil {
		return nil, fmt.Errorf("writing %s: %w", name, err)
	}

	return source, nil
}

// stripJSONBackend removes backend keys and, when requested, cloud keys from
// every terraform body of one staged .tf.json file, returning the original
// bytes when rewritten. Terraform's JSON bodies may be objects or arrays.
func stripJSONBackend(path, sourcePath string, removeCloud bool) ([]byte, error) {
	name := filepath.Base(path)

	source, readErr := os.ReadFile(path) //nolint:gosec // the staged module's own path.
	if readErr != nil {
		return nil, fmt.Errorf("reading %s: %w", name, readErr)
	}

	if !json.Valid(source) {
		return nil, fmt.Errorf("decoding %s: invalid JSON", name)
	}

	rewritten, stripped, err := stripJSONBackendBody(source, false, removeCloud)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", name, err)
	}

	if !stripped {
		return nil, nil
	}

	if err := sandbox.WriteFresh(path, sourcePath, rewritten); err != nil {
		return nil, fmt.Errorf("writing %s: %w", name, err)
	}

	return source, nil
}

func stripJSONBackendBody(source []byte, terraformBody, removeCloud bool) ([]byte, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	opening, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}

	delimiter, ok := opening.(json.Delim)
	if !ok || delimiter != '{' && delimiter != '[' {
		return nil, false, fmt.Errorf("expected a JSON body, got %v", opening)
	}

	object := delimiter == '{'
	rewritten := []byte{byte(delimiter)}
	stripped := false
	kept := false
	for decoder.More() {
		key := ""
		if object {
			token, tokenErr := decoder.Token()
			if tokenErr != nil {
				return nil, false, tokenErr
			}

			key, ok = token.(string)
			if !ok {
				return nil, false, fmt.Errorf("expected a JSON property name, got %v", token)
			}
		}

		var value json.RawMessage
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, false, decodeErr
		}

		if object && terraformBody && (key == backendBlock || removeCloud && key == "cloud") {
			stripped = true

			continue
		}

		if !object || !terraformBody && key == "terraform" {
			if !object && bytes.TrimSpace(value)[0] != '{' {
				return nil, false, fmt.Errorf("expected a JSON body object, got %s", value)
			}

			var changed bool
			value, changed, err = stripJSONBackendBody(value, terraformBody || object, removeCloud)
			if err != nil {
				return nil, false, err
			}

			stripped = stripped || changed
		}

		if kept {
			rewritten = append(rewritten, ',')
		}

		if object {
			encodedKey, encodeErr := json.Marshal(key)
			if encodeErr != nil {
				return nil, false, encodeErr
			}

			rewritten = append(rewritten, encodedKey...)
			rewritten = append(rewritten, ':')
		}

		rewritten = append(rewritten, value...)
		kept = true
	}

	closing, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}

	delimiter, ok = closing.(json.Delim)
	if !ok {
		return nil, false, fmt.Errorf("expected a closing JSON delimiter, got %v", closing)
	}

	rewritten = append(rewritten, byte(delimiter))

	return rewritten, stripped, nil
}
