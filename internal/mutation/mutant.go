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

// The Tier 1 language and expression operators. They fire on any module in any
// provider ecosystem, which is where a module's own logic lives.
const (
	CondSwap   Operator = "COND-SWAP"
	CondNegate Operator = "COND-NEGATE"
	CondTrue   Operator = "COND-TRUE"
	CondFalse  Operator = "COND-FALSE"

	BoolAndOr        Operator = "BOOL-AND-OR"
	BoolOrAnd        Operator = "BOOL-OR-AND"
	BoolNegateRemove Operator = "BOOL-NEGATE-REMOVE"
	BoolNegateInsert Operator = "BOOL-NEGATE-INSERT"
	CmpEqNe          Operator = "CMP-EQ-NE"
	CmpBoundary      Operator = "CMP-BOUNDARY"
	CmpInvert        Operator = "CMP-INVERT"

	ArithSwap   Operator = "ARITH-SWAP"
	NumOffByOne Operator = "NUM-OFF-BY-ONE"
	NumZero     Operator = "NUM-ZERO"
	NumNegate   Operator = "NUM-NEGATE"

	BoolLiteralFlip Operator = "BOOL-LITERAL-FLIP"
	StrEmpty        Operator = "STR-EMPTY"
	StrCase         Operator = "STR-CASE"
	NullInject      Operator = "NULL-INJECT"

	CollDropFirst Operator = "COLL-DROP-FIRST"
	CollDropLast  Operator = "COLL-DROP-LAST"
	CollEmpty     Operator = "COLL-EMPTY"
	CollDropEntry Operator = "COLL-DROP-ENTRY"
	CollReverse   Operator = "COLL-REVERSE"

	ForDropIf       Operator = "FOR-DROP-IF"
	ForNegateIf     Operator = "FOR-NEGATE-IF"
	ForSwapKV       Operator = "FOR-SWAP-KV"
	ForDropGrouping Operator = "FOR-DROP-GROUPING"
	IdxShift        Operator = "IDX-SHIFT"
	SplatFirst      Operator = "SPLAT-FIRST"

	TplDropInterp     Operator = "TPL-DROP-INTERP"
	TplStripFlip      Operator = "TPL-STRIP-FLIP"
	TplIfCollapse     Operator = "TPL-IF-COLLAPSE"
	HeredocIndentFlip Operator = "HEREDOC-INDENT-FLIP"

	FnSwap        Operator = "FN-SWAP"
	FnArgReorder  Operator = "FN-ARG-REORDER"
	FnDropDefault Operator = "FN-DROP-DEFAULT"
	FnTryFirst    Operator = "FN-TRY-FIRST"
	FnCanTrue     Operator = "FN-CAN-TRUE"
	//nolint:gosec // G101 matches the word "WRAPPER"; this is an operator identifier.
	FnDropWrapper   Operator = "FN-DROP-WRAPPER"
	FnJoinSep       Operator = "FN-JOIN-SEP"
	FnDropExpansion Operator = "FN-DROP-EXPANSION"

	// FnFamilySwap is the generated function catalogue (M3e, #53): pairwise
	// substitution inside explicitly justified semantic families, behind an
	// opt-in and never in `standard` until the admission gate is passed.
	FnFamilySwap Operator = "FN-FAMILY-SWAP"
)

// The Tier 2 meta-argument operators.
const (
	CountZero         Operator = "COUNT-ZERO"
	CountOne          Operator = "COUNT-ONE"
	CountOffByOne     Operator = "COUNT-OFF-BY-ONE"
	ForEachEmpty      Operator = "FOREACH-EMPTY"
	ForEachToCount    Operator = "FOREACH-TO-COUNT"
	DynamicZero       Operator = "DYNAMIC-ZERO"
	DependsDrop       Operator = "DEPENDS-DROP"
	ProviderAliasSwap Operator = "PROVIDER-ALIAS-SWAP"
)

// The Tier 3 module contract operators.
const (
	VarDefaultChange       Operator = "VAR-DEFAULT-CHANGE"
	VarDefaultRemove       Operator = "VAR-DEFAULT-REMOVE"
	VarDefaultNull         Operator = "VAR-DEFAULT-NULL"
	VarNullableFlip        Operator = "VAR-NULLABLE-FLIP"
	VarOptionalDefaultDrop Operator = "VAR-OPTIONAL-DEFAULT-DROP"
	VarValidationRemove    Operator = "VAR-VALIDATION-REMOVE"
	VarValidationWeaken    Operator = "VAR-VALIDATION-WEAKEN"
	VarValidationNegate    Operator = "VAR-VALIDATION-NEGATE"
	VarSensitiveFlip       Operator = "VAR-SENSITIVE-FLIP"
	PrePostRemove          Operator = "PRE-POST-REMOVE"
	PrePostNegate          Operator = "PRE-POST-NEGATE"
	CheckRemove            Operator = "CHECK-REMOVE"
	CheckNegate            Operator = "CHECK-NEGATE"
	OutSensitiveFlip       Operator = "OUT-SENSITIVE-FLIP"
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

// identify derives a mutant identifier from the operator, the closure-relative
// file, the site address, the source content the mutation replaces, and the
// replacement.
//
// It excludes line numbers and the rest of the file, so an identifier survives
// a line move and an unrelated edit elsewhere in the module. It is broken by
// renaming the file, and that is documented rather than worked around: an
// identifier that survived a rename would have to be derived from something
// less stable than the path.
//
// Site content and replacement are part of the derivation because Tier 1
// operators fire many times inside one attribute: without them, every
// conditional in one local would share an identifier.
func identify(operator Operator, file, site, original, replacement string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s|%s",
		operator, file, site, original, replacement))

	return hex.EncodeToString(digest[:])[:idLength]
}

// FindingID derives a stable identifier for a finding about an address.
func FindingID(kind, module, address string) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s", kind, module, address))

	return hex.EncodeToString(digest[:])[:idLength]
}
