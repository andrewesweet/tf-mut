// Package mutation generates the Tier 0 extreme mutant population for a
// discovered Terraform configuration.
//
// Every mutant is anchored to a node in the hclsyntax AST and applied through
// hclwrite, so the rewritten file differs from the original only in the tokens
// the operator owns.
package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/hcl/v2"
)

// Operator identifies a Tier 0 mutation operator.
type Operator string

// The Tier 0 extreme operator catalogue.
const (
	// AttrDelete deletes one schema-optional argument assignment.
	AttrDelete Operator = "EXT-ATTR-DELETE"
	// ResourceDelete empties a resource's instance set.
	ResourceDelete Operator = "EXT-RESOURCE-DELETE"
	// BodyBlank deletes every optional argument of a resource at once.
	BodyBlank Operator = "EXT-BODY-BLANK"
	// OutputNull replaces an output value with null.
	OutputNull Operator = "EXT-OUTPUT-NULL"
	// LocalNull replaces a local value with null.
	LocalNull Operator = "EXT-LOCAL-NULL"
	// ModuleInputDelete deletes an input argument on a module call.
	ModuleInputDelete Operator = "EXT-MODULE-INPUT-DELETE"
)

// Mutant is one generated mutation of one file.
type Mutant struct {
	// ID is a stable content-derived identifier for the mutation site.
	ID string
	// Operator is the operator that produced the mutant.
	Operator Operator
	// ModuleRel is the closure-relative directory of the mutated module.
	ModuleRel string
	// File is the closure-relative path of the mutated file.
	File string
	// Site is the semantic address of the mutation site.
	Site string
	// Resource is the resource address the site belongs to, when it has one.
	Resource string
	// Range is the source range of the mutated construct.
	Range hcl.Range
	// Diff is a unified diff of the mutation.
	Diff string
	// Mutated is the rewritten file content.
	Mutated []byte
}

// idLength is the number of hexadecimal characters in a mutant identifier.
const idLength = 12

// identify derives a mutant identifier from its operator and site.
//
// The identifier deliberately excludes line numbers and file contents so that
// it survives unrelated edits elsewhere in the module.
func identify(operator Operator, file, site string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s", operator, file, site))

	return hex.EncodeToString(digest[:])[:idLength]
}

// FindingID derives a stable identifier for a finding about an address.
func FindingID(kind, module, address string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s", kind, module, address))

	return hex.EncodeToString(digest[:])[:idLength]
}
