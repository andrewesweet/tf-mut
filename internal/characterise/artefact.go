package characterise

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// The artefact parser. The non-executable artefact class is written by this
// context's renderer and read back only here: an answer a reader edited into
// a `todo` block reaches the engine through this parser and nothing else, so
// the artefact's shape — labels, the `value` attribute, the placeholder — is
// known in exactly one package.

// todoPlaceholder is the unanswered value in a non-executable artefact.
const todoPlaceholder = "TFMUT_TODO"

// ParseArtefactAnswers reads the answered values out of one non-executable
// artefact, keyed by judgement-point identifier. An unanswered point — the
// placeholder, or a value that names no text — is skipped, not an error.
func ParseArtefactAnswers(path string) (map[string]string, error) {
	content, err := os.ReadFile(path) //nolint:gosec // a discovery-owned test directory.
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	file, diagnostics := hclparse.NewParser().ParseHCL(content, path)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: %s: %s", discovery.ErrParse, path, diagnostics.Error())
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%w: %s: unexpected body type", discovery.ErrParse, path)
	}

	answers := map[string]string{}

	collectArtefactAnswers(body, content, answers)

	return answers, nil
}

// collectArtefactAnswers reads the answered value of each todo block, verbatim,
// so the answer written into the run block is the text the reader wrote.
func collectArtefactAnswers(body *hclsyntax.Body, content []byte, answers map[string]string) {
	for _, block := range body.Blocks {
		if block.Type != "todo" || len(block.Labels) != 1 {
			continue
		}

		attribute, found := block.Body.Attributes["value"]
		if !found {
			continue
		}

		source := expressionSource(content, attribute.Expr)
		if source == "" || source == todoPlaceholder {
			continue
		}

		answers[block.Labels[0]] = source
	}
}

func expressionSource(content []byte, expr hcl.Expression) string {
	span := expr.Range()
	if span.Start.Byte < 0 || span.End.Byte > len(content) || span.Start.Byte >= span.End.Byte {
		return ""
	}

	return strings.TrimSpace(string(content[span.Start.Byte:span.End.Byte]))
}
