package mutation

import (
	"cmp"
	"slices"
)

// Tier is the operator breadth band an operator belongs to.
type Tier string

// The tiers the tool can be asked for. `deep` exists as a name so that
// configuration can refer to it; no Tier 4 operator ships in this milestone.
const (
	// TierSmoke is Tier 0: the extreme operators.
	TierSmoke Tier = "smoke"
	// TierStandard is Tiers 1 to 3: expressions, meta-arguments and contracts.
	TierStandard Tier = "standard"
	// TierDeep is Tier 4 and above: lifecycle and state safety.
	TierDeep Tier = "deep"
)

// Includes reports whether a tier selection enables the operators of another.
func (t Tier) Includes(other Tier) bool {
	return t.rank() >= other.rank()
}

// Valid reports whether the tier names a band the tool knows.
func (t Tier) Valid() bool {
	return t.rank() > rankUnknown
}

// The breadth ordering of the tiers, so that a selection can include everything
// at or below it.
const (
	rankUnknown = iota
	rankSmoke
	rankStandard
	rankDeep
)

func (t Tier) rank() int {
	switch t {
	case TierSmoke:
		return rankSmoke
	case TierStandard:
		return rankStandard
	case TierDeep:
		return rankDeep
	default:
		return rankUnknown
	}
}

// Entry is one operator's catalogue metadata.
//
// The description is published: SARIF carries one rule per operator, and the
// rule's help text is this field, so a reader of a code-scanning annotation
// gets the catalogue's own words rather than a paraphrase.
type Entry struct {
	// Operator is the catalogue identifier.
	Operator Operator
	// Tier is the breadth band the operator belongs to.
	Tier Tier
	// Description states the fault the operator models.
	Description string
	// Killer states what an assertion would have to inspect to catch it.
	Killer string
	// Projects reports whether the mutated construct has a plan or state
	// projection at all. An operator that does not project can only ever be
	// StructurallyUnassertable when its fingerprint comes back identical, and
	// the fix it names is a negative test rather than an assertion.
	Projects bool
	// Fix is the guidance a StructurallyUnassertable finding carries.
	Fix string
}

// catalogue is the enabled operator set, keyed by identifier.
//
//nolint:gochecknoglobals // an immutable lookup table.
var catalogue = buildCatalogue()

// Catalogue lists every enabled operator in identifier order.
func Catalogue() []Entry {
	entries := make([]Entry, 0, len(catalogue))
	for _, entry := range catalogue {
		entries = append(entries, entry)
	}

	slices.SortFunc(entries, func(left, right Entry) int {
		return compareOperators(left.Operator, right.Operator)
	})

	return entries
}

// Describe returns the catalogue entry for an operator.
func Describe(operator Operator) (Entry, bool) {
	entry, found := catalogue[operator]

	return entry, found
}

// TierOf returns the tier of an operator, or the empty tier if unknown.
func TierOf(operator Operator) Tier {
	if entry, found := catalogue[operator]; found {
		return entry.Tier
	}

	return ""
}

// Projects reports whether the operator's construct has a plan or state
// projection.
func Projects(operator Operator) bool {
	if entry, found := catalogue[operator]; found {
		return entry.Projects
	}

	return true
}

func compareOperators(left, right Operator) int {
	return cmp.Compare(left, right)
}

// The fix guidance the non-projecting operators share.
const (
	fixExpectFailures = "add a run block with expect_failures naming the construct, " +
		"or accept the mutant: nothing in a plan or state can distinguish it"
	fixOrdering = "ordering is not observable in a plan or state; assert the effect the " +
		"dependency exists to guarantee, or accept the mutant"
)

// extremeEntries is the Tier 0 table.
func extremeEntries() []Entry {
	return []Entry{
		// Tier 0 — extreme.
		{
			AttrDelete, TierSmoke, "Deletes one schema-optional argument assignment so the provider default applies",
			"any assertion reads that attribute", true, "",
		},
		{
			ResourceDelete, TierSmoke, "Empties a resource's instance set",
			"any assertion counts, indexes or reads the resource", true, "",
		},
		{
			BodyBlank, TierSmoke, "Deletes every optional argument of a resource at once",
			"any assertion reads any configured attribute of the resource", true, "",
		},
		{
			OutputNull, TierSmoke, "Replaces an output value with null",
			"any assertion reads the output", true, "",
		},
		{
			LocalNull, TierSmoke, "Replaces a local value with null",
			"any assertion reads anything downstream of the local", true, "",
		},
		{
			ModuleInputDelete, TierSmoke, "Deletes an input argument on a module call",
			"any assertion observes the child's behaviour under that input", true, "",
		},
	}
}

// expressionEntries is the Tier 1 table for conditionals, boolean and
// comparison operators, arithmetic, literals and collections.
func expressionEntries() []Entry {
	return []Entry{
		// Tier 1 — conditionals.
		{
			CondSwap, TierStandard, "Swaps the arms of a conditional",
			"an assertion distinguishes the two arms", true, "",
		},
		{
			CondNegate, TierStandard, "Negates a conditional's predicate",
			"an assertion distinguishes the two arms", true, "",
		},
		{
			CondTrue, TierStandard, "Collapses a conditional to its true arm",
			"an assertion exercises the false arm", true, "",
		},
		{
			CondFalse, TierStandard, "Collapses a conditional to its false arm",
			"an assertion exercises the true arm", true, "",
		},

		// Tier 1 — boolean and comparison.
		{
			BoolAndOr, TierStandard, "Replaces a logical and with a logical or",
			"an assertion exercises an input where the combinators differ", true, "",
		},
		{
			BoolOrAnd, TierStandard, "Replaces a logical or with a logical and",
			"an assertion exercises an input where the combinators differ", true, "",
		},
		{
			BoolNegateRemove, TierStandard, "Removes a logical negation",
			"an assertion exercises the negated condition", true, "",
		},
		{
			BoolNegateInsert, TierStandard, "Inserts a logical negation around a comparison",
			"an assertion exercises the condition", true, "",
		},
		{
			CmpEqNe, TierStandard, "Inverts an equality comparison",
			"an assertion exercises a value where equality matters", true, "",
		},
		{
			CmpBoundary, TierStandard, "Shifts a comparison to its inclusive or exclusive neighbour",
			"an assertion exercises the boundary value", true, "",
		},
		{
			CmpInvert, TierStandard, "Reverses the direction of a comparison",
			"an assertion exercises a value on either side", true, "",
		},

		// Tier 1 — arithmetic and literals.
		{
			ArithSwap, TierStandard, "Swaps an arithmetic operator for its inverse",
			"an assertion reads the computed value", true, "",
		},
		{
			NumOffByOne, TierStandard, "Shifts a numeric literal by one",
			"an assertion checks the exact value", true, "",
		},
		{
			NumZero, TierStandard, "Replaces a numeric literal with zero",
			"an assertion checks the value", true, "",
		},
		{
			NumNegate, TierStandard, "Negates a numeric literal",
			"an assertion checks the sign", true, "",
		},
		{
			BoolLiteralFlip, TierStandard, "Flips a boolean literal",
			"an assertion reads the flag", true, "",
		},
		{
			StrEmpty, TierStandard, "Replaces a string literal with the empty string",
			"an assertion reads the string", true, "",
		},
		{
			StrCase, TierStandard, "Flips the case of a string literal",
			"an assertion compares the string exactly", true, "",
		},
		{
			NullInject, TierStandard, "Replaces a schema-optional argument value with null",
			"an assertion reads the attribute", true, "",
		},
	}
}

// collectionEntries is the Tier 1 table for collection operators.
func collectionEntries() []Entry {
	return []Entry{
		// Tier 1 — collections.
		{
			CollDropFirst, TierStandard, "Drops the first element of a tuple",
			"an assertion reads the collection's length or contents", true, "",
		},
		{
			CollDropLast, TierStandard, "Drops the last element of a tuple",
			"an assertion reads the collection's length or contents", true, "",
		},
		{
			CollEmpty, TierStandard, "Empties a tuple or object",
			"an assertion reads the collection", true, "",
		},
		{
			CollDropEntry, TierStandard, "Drops one entry of an object",
			"an assertion reads that entry", true, "",
		},
		{
			CollReverse, TierStandard, "Reverses the order of a tuple",
			"an assertion reads the collection in order", true, "",
		},
	}
}

// projectionEntries is the Tier 1 table for for-expressions, traversals,
// templates and the curated functions.
func projectionEntries() []Entry {
	return []Entry{
		// Tier 1 — for expressions and traversals.
		{
			ForDropIf, TierStandard, "Drops the filter of a for expression",
			"an assertion exercises input the filter excludes", true, "",
		},
		{
			ForNegateIf, TierStandard, "Negates the filter of a for expression",
			"an assertion exercises input the filter selects", true, "",
		},
		{
			ForSwapKV, TierStandard, "Transposes the key and value of an object for expression",
			"an assertion reads the resulting keys", true, "",
		},
		{
			ForDropGrouping, TierStandard, "Drops the grouping mode of an object for expression",
			"an assertion reads a grouped value", true, "",
		},
		{
			IdxShift, TierStandard, "Shifts a literal index by one",
			"an assertion reads the indexed element", true, "",
		},
		{
			SplatFirst, TierStandard, "Collapses a splat to its first element",
			"an assertion reads the whole projection", true, "",
		},

		// Tier 1 — templates.
		{
			TplDropInterp, TierStandard, "Drops one interpolation from a template",
			"an assertion reads the rendered string", true, "",
		},
		{
			TplStripFlip, TierStandard, "Flips a template strip marker",
			"an assertion compares the rendered whitespace", true, "",
		},
		{
			TplIfCollapse, TierStandard, "Collapses a template conditional to one branch",
			"an assertion exercises the other branch", true, "",
		},
		{
			HeredocIndentFlip, TierStandard, "Removes the indentation marker from a heredoc",
			"an assertion compares the rendered indentation", true, "",
		},

		// Tier 1 — curated functions.
		{
			FnSwap, TierStandard, "Substitutes a function for its curated counterpart",
			"an assertion reads a value where the two functions differ", true, "",
		},
		{
			FnArgReorder, TierStandard, "Swaps the first two arguments of a merge or fallback",
			"an assertion reads a value where precedence matters", true, "",
		},
		{
			FnDropDefault, TierStandard, "Removes the fallback argument of lookup or try",
			"an assertion exercises the fallback path", true, "",
		},
		{
			FnTryFirst, TierStandard, "Makes a try fall through to its last argument",
			"an assertion exercises the primary path", true, "",
		},
		{
			FnCanTrue, TierStandard, "Neuters a can guard",
			"an assertion exercises input the guard rejects", true, "",
		},
		{
			FnDropWrapper, TierStandard, "Removes a normalisation wrapper",
			"an assertion reads the normalised value", true, "",
		},
		{
			FnJoinSep, TierStandard, "Empties a join separator",
			"an assertion reads the joined string", true, "",
		},
		{
			FnDropExpansion, TierStandard, "Removes an argument expansion",
			"an assertion reads the call's result", true, "",
		},
		{
			FnFamilySwap, TierStandard,
			"Substitutes a function for another member of its semantic family (opt-in, M3e)",
			"an assertion reads a value the family members disagree on", true, "",
		},
	}
}

// metaEntries is the Tier 2 meta-argument table.
func metaEntries() []Entry {
	return []Entry{
		// Tier 2 — meta-arguments.
		{
			CountZero, TierStandard, "Empties a counted resource's instance set",
			"an assertion counts or indexes instances", true, "",
		},
		{
			CountOne, TierStandard, "Reduces a count to one",
			"an assertion checks the instance count", true, "",
		},
		{
			CountOffByOne, TierStandard, "Shifts a literal count by one",
			"an assertion checks the exact instance count", true, "",
		},
		{
			ForEachEmpty, TierStandard, "Empties a for_each collection",
			"an assertion counts instances", true, "",
		},
		{
			ForEachToCount, TierStandard, "Rewrites a for_each over a set as an equivalent count",
			"an assertion depends on instance keys rather than ordinals", true, "",
		},
		{
			DynamicZero, TierStandard, "Empties a dynamic block's for_each",
			"an assertion reads the generated nested block", true, "",
		},
		{
			DependsDrop, TierStandard, "Removes a depends_on meta-argument",
			"nothing in a plan or state: ordering has no projection", false, fixOrdering,
		},
		{
			ProviderAliasSwap, TierStandard, "Swaps a provider alias for another of identical mock status",
			"an assertion checks region or account placement", true, "",
		},
	}
}

// contractEntries is the Tier 3 module-contract table.
func contractEntries() []Entry {
	return []Entry{
		// Tier 3 — module contract.
		{
			VarDefaultChange, TierStandard, "Changes a variable's default to a type-appropriate alternative",
			"a run block relies on the default", true, "",
		},
		{
			VarDefaultRemove, TierStandard, "Removes a variable's default, making it required",
			"a run block omits the variable", true, "",
		},
		{
			VarDefaultNull, TierStandard, "Replaces a variable's default with null",
			"a run block relies on the default", true, "",
		},
		{
			VarNullableFlip, TierStandard, "Flips a variable's nullable flag",
			"a test passes null explicitly", true, "",
		},
		{
			VarOptionalDefaultDrop, TierStandard, "Drops the default from an optional object attribute",
			"a test exercises the object without that attribute", true, "",
		},
		{
			VarValidationRemove, TierStandard, "Removes a variable validation block",
			"a run block uses expect_failures on the variable", false, fixExpectFailures,
		},
		{
			VarValidationWeaken, TierStandard, "Weakens a validation condition to can(var.<name>)",
			"a run block uses expect_failures on the variable", false, fixExpectFailures,
		},
		{
			VarValidationNegate, TierStandard, "Negates a validation condition",
			"a run block asserts a valid value is accepted", false, fixExpectFailures,
		},
		{
			VarSensitiveFlip, TierStandard, "Clears a variable's sensitive flag",
			"rarely: a sensitivity pseudo-test detector", true, "",
		},
		{
			PrePostRemove, TierStandard, "Removes a precondition or postcondition",
			"a run block uses expect_failures on it", false, fixExpectFailures,
		},
		{
			PrePostNegate, TierStandard, "Negates a precondition or postcondition",
			"a run block asserts the happy path", false, fixExpectFailures,
		},
		{
			CheckRemove, TierStandard, "Removes a check block's assertion",
			"a run block exercises the check", false, fixExpectFailures,
		},
		{
			CheckNegate, TierStandard, "Negates a check block's assertion",
			"a run block exercises the check", false, fixExpectFailures,
		},
		{
			OutSensitiveFlip, TierStandard, "Clears an output's sensitive flag",
			"rarely: a sensitivity pseudo-test detector", true, "",
		},
	}
}

// catalogueCapacity sizes the assembled table; the audit tests hold the real
// count against the applicability matrix.
const catalogueCapacity = 96

// buildCatalogue assembles the flat per-tier tables into the catalogue.
func buildCatalogue() map[Operator]Entry {
	entries := make([]Entry, 0, catalogueCapacity)
	entries = append(entries, extremeEntries()...)
	entries = append(entries, expressionEntries()...)
	entries = append(entries, collectionEntries()...)
	entries = append(entries, projectionEntries()...)
	entries = append(entries, metaEntries()...)
	entries = append(entries, contractEntries()...)

	table := make(map[Operator]Entry, len(entries))
	for _, entry := range entries {
		table[entry.Operator] = entry
	}

	return table
}

// Descriptions renders the catalogue for a consumer that publishes it, keyed by
// operator identifier.
//
// The report package holds the SARIF rule metadata but must not import this
// one — reports are derived from a population, not the other way round — so the
// catalogue is handed over rather than reached into.
func Descriptions() map[string]Entry {
	described := make(map[string]Entry, len(catalogue))
	for operator, entry := range catalogue {
		described[string(operator)] = entry
	}

	return described
}
