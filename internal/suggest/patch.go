package suggest

import (
	"errors"
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// ErrPatch reports a target run the writer could not rewrite.
var ErrPatch = errors.New("the target run could not be rewritten")

// The block and attribute names the writer emits.
const (
	runBlockType    = "run"
	assertBlockType = "assert"
	conditionName   = "condition"
	messageName     = "error_message"
)

// PatchFor renders the unified diff that adds the assertion to the target run.
func PatchFor(target discovery.RunBlock, expression, message string) (string, error) {
	original, err := os.ReadFile(target.File)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", target.Rel, err)
	}

	patched, err := Apply(original, target.Rel, target.Name, expression, message)
	if err != nil {
		return "", err
	}

	return mutation.UnifiedDiff(target.Rel, original, patched), nil
}

// Apply appends the assertion to the named run and returns the rewritten file.
//
// The new assert is appended rather than placed beside a named predecessor.
// `weak-assertion` means at least one assertion is reachable from the changed
// address, and several assertions can satisfy that predicate: the report would
// be claiming evidence it does not hold if it named one of them as the weak
// one. Appending is smaller and equally correct.
func Apply(original []byte, path, run, expression, message string) ([]byte, error) {
	file, diagnostics := hclwrite.ParseConfig(original, path, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", ErrPatch, path, diagnostics.Error())
	}

	block := findRun(file, run)
	if block == nil {
		return nil, fmt.Errorf("%w: %s declares no run %q", ErrPatch, path, run)
	}

	tokens, err := expressionTokens(expression)
	if err != nil {
		return nil, err
	}

	body := block.Body()
	body.AppendNewline()

	assertion := body.AppendNewBlock(assertBlockType, nil)
	assertion.Body().SetAttributeRaw(conditionName, tokens)
	assertion.Body().SetAttributeValue(messageName, cty.StringVal(message))

	return file.Bytes(), nil
}

// findRun locates a run block by its label.
func findRun(file *hclwrite.File, name string) *hclwrite.Block {
	for _, block := range file.Body().Blocks() {
		labels := block.Labels()
		if block.Type() == runBlockType && len(labels) == 1 && labels[0] == name {
			return block
		}
	}

	return nil
}

// expressionTokens turns the generated condition into writable tokens.
//
// The expression is lexed rather than pasted so that the emitted file is made
// of real tokens: a condition that does not lex cleanly must never reach a test
// file, and the address adapter has already proven this one parses.
func expressionTokens(expression string) (hclwrite.Tokens, error) {
	if _, diagnostics := hclsyntax.ParseExpression(
		[]byte(expression), "suggestion", hcl.InitialPos,
	); diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", ErrPatch, expression, diagnostics.Error())
	}

	tokens, diagnostics := hclwrite.ParseConfig(
		[]byte(conditionName+" = "+expression+"\n"), "suggestion", hcl.InitialPos,
	)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", ErrPatch, expression, diagnostics.Error())
	}

	attribute := tokens.Body().GetAttribute(conditionName)
	if attribute == nil {
		return nil, fmt.Errorf("%w: %s did not round-trip through the writer", ErrPatch, expression)
	}

	return attribute.Expr().BuildTokens(nil), nil
}
