package mutation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// A pack is data, not operators (M5c.1). The catalogue carries exactly the
// form operators — PACK-FLIP and PACK-REPLACE — and a pack file's entries
// parameterise them: each entry names a resource type, an attribute, a form
// and the literal pair the form rewrites. The contract below is the normative
// table in docs/design/mutation-operators.md, checked at load so that a pack
// that could never fire is a configuration error rather than a silent zero.

// The pack form operators. PACK-WIDEN-CIDR and its `widen-cidr` form are
// deferred to the M5-0.3 census (#161).
const (
	PackFlip    Operator = "PACK-FLIP"
	PackReplace Operator = "PACK-REPLACE"
)

// The forms a pack entry may declare.
const (
	FormFlip    = "flip"
	FormReplace = "replace"
)

// ErrPack reports a pack file the tool refuses to load.
var ErrPack = errors.New("pack is not usable")

// entryLabel is the grammar of an entry's author-supplied identifier.
var entryLabel = regexp.MustCompile(`^[a-z0-9-]+$`)

// reservedPackNames are the shipped packs' names. No shipped pack is embedded
// yet — `security-aws` arrives with M5c.2 on the M5-0.3 census — but its name
// is reserved now so a user pack registered today cannot be shadowed by it
// tomorrow. A further pack reserves its name in the change that ships it.
//
//nolint:gochecknoglobals // an immutable list.
var reservedPackNames = []string{"security-aws"}

// ReservedPackNames lists the names a user pack may not take.
func ReservedPackNames() []string {
	return append([]string{}, reservedPackNames...)
}

// Pack is one loaded pack: its registered name, the file it came from, the
// digest of that file's bytes and its entries in declaration order.
type Pack struct {
	Name    string
	Path    string
	Digest  string
	Entries []PackEntry
}

// PackEntry is one entry of a pack: the site it scopes to, the form it takes
// and the literal pair the form rewrites.
type PackEntry struct {
	// ID is the author-supplied label, unique within the pack; the pair
	// (pack, ID) is an origin's wire identity.
	ID           string
	ResourceType string
	Attribute    string
	Form         string
	From         cty.Value
	To           cty.Value
	// SourceRule and SourceLicence carry the upstream provenance a shipped
	// pack must state and a user pack may.
	SourceRule    string
	SourceLicence string
}

// Operator is the form operator the entry parameterises.
func (e PackEntry) Operator() Operator {
	if e.Form == FormFlip {
		return PackFlip
	}

	return PackReplace
}

// Origin is one (operator, pack, entry) whose rewrite produced a mutant's
// bytes. Origins are aggregated onto the deduplication survivor, sorted and
// deduplicated by (pack, entry), whichever operator owns the row.
type Origin struct {
	Operator Operator
	Pack     string
	Entry    string
}

// LoadPack reads and checks one pack file.
func LoadPack(name, path string) (Pack, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path is the caller's registered pack file.
	if err != nil {
		return Pack{}, fmt.Errorf("pack %q: %w: reading %s: %w", name, ErrPack, path, err)
	}

	return ParsePack(name, path, content)
}

// ParsePack checks pack content against the contract: only `entry "ID" { … }`
// blocks, literal values only, labels unique and well-formed, every form's
// constraints satisfied.
func ParsePack(name, path string, content []byte) (Pack, error) {
	parsed, diagnostics := hclsyntax.ParseConfig(content, path, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return Pack{}, fmt.Errorf("pack %q: %w: %s", name, ErrPack, diagnostics.Error())
	}

	body, ok := parsed.Body.(*hclsyntax.Body)
	if !ok {
		return Pack{}, fmt.Errorf("pack %q: %w: %s: unexpected body", name, ErrPack, path)
	}

	if attribute := firstAttribute(body.Attributes); attribute != nil {
		return Pack{}, fmt.Errorf("pack %q: %w: %s: top-level attribute %q is not an entry; "+
			"a pack contains only entry blocks", name, ErrPack, attribute.Range(), attribute.Name)
	}

	digest := sha256.Sum256(content)
	pack := Pack{Name: name, Path: path, Digest: hex.EncodeToString(digest[:]), Entries: []PackEntry{}}
	seen := map[string]hcl.Range{}

	for _, block := range body.Blocks {
		entry, err := parseEntry(block)
		if err != nil {
			return Pack{}, fmt.Errorf("pack %q: %w", name, err)
		}

		if previous, repeated := seen[entry.ID]; repeated {
			return Pack{}, fmt.Errorf("pack %q: %w: entry %q is declared twice, at %s and %s; "+
				"a label is an identity and renaming one is a new identity",
				name, ErrPack, entry.ID, previous, block.LabelRanges[0])
		}

		seen[entry.ID] = block.LabelRanges[0]
		pack.Entries = append(pack.Entries, entry)
	}

	return pack, nil
}

// The entry fields. The first five are required; the two provenance fields
// are required for a shipped pack and optional for a user pack.
const (
	fieldResourceType  = "resource_type"
	fieldAttribute     = "attribute"
	fieldForm          = "form"
	fieldFrom          = "from"
	fieldTo            = "to"
	fieldSourceRule    = "source_rule"
	fieldSourceLicence = "source_licence"
)

// literalKinds names what a pack value may be, for every literal-only error.
const literalKinds = "a pack value is a literal string, number or bool"

func parseEntry(block *hclsyntax.Block) (PackEntry, error) {
	if block.Type != "entry" {
		return PackEntry{}, fmt.Errorf("%w: %s: block %q is not an entry; a pack contains only entry blocks",
			ErrPack, block.TypeRange, block.Type)
	}

	if len(block.Labels) != 1 {
		return PackEntry{}, fmt.Errorf("%w: %s: an entry needs exactly one label, its identifier",
			ErrPack, block.DefRange())
	}

	entry := PackEntry{ //nolint:exhaustruct // filled from the attributes below.
		ID: block.Labels[0],
	}

	if !entryLabel.MatchString(entry.ID) {
		return PackEntry{}, fmt.Errorf("%w: %s: entry label %q is not [a-z0-9-]+",
			ErrPack, block.LabelRanges[0], entry.ID)
	}

	if len(block.Body.Blocks) > 0 {
		nested := block.Body.Blocks[0]

		return PackEntry{}, fmt.Errorf("%w: %s: entry %q carries a nested %q block; an entry holds "+
			"attributes only", ErrPack, nested.TypeRange, entry.ID, nested.Type)
	}

	set := map[string]bool{}

	for name, attribute := range block.Body.Attributes {
		value, err := literalValue(attribute)
		if err != nil {
			return PackEntry{}, err
		}

		if err := assignEntryField(&entry, name, value, attribute.Range()); err != nil {
			return PackEntry{}, err
		}

		set[name] = true
	}

	for _, name := range []string{fieldResourceType, fieldAttribute, fieldForm, fieldFrom, fieldTo} {
		if !set[name] {
			return PackEntry{}, fmt.Errorf("%w: %s: entry %q does not set %s",
				ErrPack, block.DefRange(), entry.ID, name)
		}
	}

	return entry, checkForm(entry, block.DefRange())
}

// firstAttribute is the earliest attribute of a body by position, so an error
// names the same position on every load.
func firstAttribute(attributes hclsyntax.Attributes) *hclsyntax.Attribute {
	var first *hclsyntax.Attribute

	for _, attribute := range attributes {
		if first == nil || attribute.Range().Start.Byte < first.Range().Start.Byte {
			first = attribute
		}
	}

	return first
}

// literalValue accepts exactly a literal: a bare true, false or number, or a
// quoted string with no interpolation. Everything else — a function call, a
// variable, a template, a collection — is a configuration error naming the
// position, because a pack is data and nothing in it may evaluate.
func literalValue(attribute *hclsyntax.Attribute) (cty.Value, error) {
	switch expr := attribute.Expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		if expr.Val.IsNull() {
			return cty.NilVal, fmt.Errorf("%w: %s: %s is null; %s",
				ErrPack, expr.Range(), attribute.Name, literalKinds)
		}

		return expr.Val, nil
	case *hclsyntax.TemplateExpr:
		if !expr.IsStringLiteral() {
			return cty.NilVal, fmt.Errorf("%w: %s: %s interpolates; %s",
				ErrPack, expr.Range(), attribute.Name, literalKinds)
		}

		value, diagnostics := expr.Value(nil)
		if diagnostics.HasErrors() {
			return cty.NilVal, fmt.Errorf("%w: %s: %s: %s",
				ErrPack, expr.Range(), attribute.Name, diagnostics.Error())
		}

		return value, nil
	case *hclsyntax.TemplateWrapExpr:
		return cty.NilVal, fmt.Errorf("%w: %s: %s interpolates; %s",
			ErrPack, expr.Range(), attribute.Name, literalKinds)
	default:
		return cty.NilVal, fmt.Errorf("%w: %s: %s is an expression; %s",
			ErrPack, attribute.Expr.Range(), attribute.Name, literalKinds)
	}
}

func assignEntryField(entry *PackEntry, name string, value cty.Value, rng hcl.Range) error {
	text := func() (string, error) {
		if value.Type() != cty.String {
			return "", fmt.Errorf("%w: %s: %s must be a string", ErrPack, rng, name)
		}

		return value.AsString(), nil
	}

	var err error

	switch name {
	case fieldResourceType:
		entry.ResourceType, err = text()
	case fieldAttribute:
		entry.Attribute, err = text()
	case fieldForm:
		entry.Form, err = text()
	case fieldFrom:
		entry.From = value
	case fieldTo:
		entry.To = value
	case fieldSourceRule:
		entry.SourceRule, err = text()
	case fieldSourceLicence:
		entry.SourceLicence, err = text()
	default:
		err = fmt.Errorf("%w: %s: %q is not an entry field", ErrPack, rng, name)
	}

	return err
}

// checkForm applies each form's load-time constraints.
func checkForm(entry PackEntry, rng hcl.Range) error {
	switch entry.Form {
	case FormFlip:
		if entry.From.Type() != cty.Bool || entry.To.Type() != cty.Bool {
			return fmt.Errorf("%w: %s: entry %q: flip needs a boolean from and the opposite boolean to; got %s and %s",
				ErrPack, rng, entry.ID, describeLiteral(entry.From), describeLiteral(entry.To))
		}

		if entry.From.True() == entry.To.True() {
			return fmt.Errorf("%w: %s: entry %q: flip needs the opposite boolean; from and to are both %s",
				ErrPack, rng, entry.ID, describeLiteral(entry.From))
		}

		return nil
	case FormReplace:
		if !entry.From.Type().Equals(entry.To.Type()) {
			return fmt.Errorf("%w: %s: entry %q: replace needs from and to of one kind; got %s and %s",
				ErrPack, rng, entry.ID, describeLiteral(entry.From), describeLiteral(entry.To))
		}

		if entry.From.Equals(entry.To).True() {
			return fmt.Errorf("%w: %s: entry %q: replace with from equal to its replacement (%s) is a no-op, not a fault",
				ErrPack, rng, entry.ID, describeLiteral(entry.From))
		}

		return nil
	default:
		return fmt.Errorf("%w: %s: entry %q: form %q is not one of %s, %s",
			ErrPack, rng, entry.ID, entry.Form, FormFlip, FormReplace)
	}
}

// describeLiteral renders a literal for an error message.
func describeLiteral(value cty.Value) string {
	switch value.Type() {
	case cty.String:
		return fmt.Sprintf("%q", value.AsString())
	case cty.Bool:
		return strconv.FormatBool(value.True())
	case cty.Number:
		return formatNumber(value.AsBigFloat())
	default:
		return value.Type().FriendlyName()
	}
}
