# Mutation operator catalogue

## Design rules

Every operator obeys the same five rules.

1. **AST-anchored.** An operator matches a node in the `hclsyntax` AST and rewrites the token
   range that node occupies. No operator matches on text.
2. **Type-preserving where the language demands it.** A mutant that cannot pass
   `terraform validate` is wasted work. Operators that would change an expression's type are
   either omitted or guarded by a static type check.
3. **Fault-realistic.** Each operator models a mistake a competent engineer actually makes.
   "Replace this string with `zzz`" is not a fault model; "drop the `if` clause from a `for`
   expression" is.
4. **Declares its expected killer.** Every operator states what an assertion would have to
   inspect in order to kill it. This is what powers the survivor diagnosis and the suggested
   assertion (see `product-design.md` §7).
5. **Tiered.** Operators belong to `smoke`, `standard` or `deep`. Tiers exist so the tool can
   be fast by default and thorough on request. The pack form operators belong to Tier 5, which
   is not a breadth tier: a pack is selected by name, never by `--tier`.

Notation: **site** = the AST node the operator fires on. Every mutant records file, line,
column, operator ID, site description and a one-line unified diff.

---

## Tier 0 — Extreme (`smoke`)

The Terraform analogue of Descartes' extreme mutation. These generate very few mutants, run in
seconds, and answer the question that Oasis's benchmark says actually matters: *is this
resource tested at all, or merely covered?*

| ID | Site | Mutation | Kills when |
| --- | --- | --- | --- |
| `EXT-ATTR-DELETE` | Any schema-optional argument assignment in a `resource`/`data`/`module` body | Delete the assignment entirely, so the provider or child-module default applies | Any assertion reads that attribute |
| `EXT-RESOURCE-DELETE` | A `resource` block where emptying is non-erroring (see multiplicity table below) | Empty the resource's instance set | Any **assertion** counts, indexes, or reads the resource |
| `EXT-BODY-BLANK` | A `resource` block where emptying would error (exact-index consumers, and any other excluded case) | Delete every optional argument in the body at once — the Descartes "empty the method body" analogue | Any assertion reads any configured attribute of the resource |
| `EXT-OUTPUT-NULL` | An `output` block | Replace `value` with `null` | Any assertion reads `output.<name>` |
| `EXT-LOCAL-NULL` | A `locals` entry | Replace the value with `null` | Any assertion reads anything downstream of that local |
| `EXT-MODULE-INPUT-DELETE` | An input argument on a `module` call | Delete the argument, falling back to the child's default | Any assertion observes the child's behaviour under that input |

`EXT-RESOURCE-DELETE` — emptying the instance set rather than deleting the block — took two
review rounds to specify correctly, and the failure history is worth keeping. Round one (C3)
caught the validity condition stated backwards: adding `count` to a resource makes every
*bare* reference (`null_resource.app.id`) statically invalid ("Missing resource instance
key"), while indexed references validate. Round two (R2-5) caught the repair leaking: an
*indexed* reference (`app[0].id`) against `count = 0` validates but **errors at evaluation**
("Invalid index"), producing `KilledByError` from a zero-assertion suite — which would report
the resource as tested; and `for_each` resources reject an added `count` outright ("Invalid
combination"). The resulting specification is multiplicity- and use-site-aware:

| Resource form | Consumers | Mutation | Else |
| --- | --- | --- | --- |
| Existing `count = n` | All tolerate empty collections (splats, `length`, `for`) | `count = 0` | `EXT-BODY-BLANK` |
| Existing `for_each` | All tolerate empty collections | `for_each = {}` — never an added `count` | `EXT-BODY-BLANK` |
| No meta-argument | Any bare or exact-index reference | — statically doomed — | `EXT-BODY-BLANK` |

And the definition that makes the headline honest (R2-5 / M8): a resource is
**pseudo-tested** only when every extreme mutant that executed was undetected by an
*assertion* — `KilledByError` never counts as evidence of testing, because Terraform's own
"Invalid index" on an empty set is not a test.

A resource whose every `EXT-*` mutant survives is a **pseudo-tested resource** — covered by a
plan, asserted on by nothing. This is the tool's headline finding.

---

## Tier 1 — Language / expression (`standard`)

These fire on any module in any provider ecosystem, which is the property Oasis's
domain-specific operators lack.

### Conditionals

| ID | Original | Mutated |
| --- | --- | --- |
| `COND-SWAP` | `c ? a : b` | `c ? b : a` |
| `COND-NEGATE` | `c ? a : b` | `!(c) ? a : b` |
| `COND-TRUE` | `c ? a : b` | `a` |
| `COND-FALSE` | `c ? a : b` | `b` |

`COND-TRUE`/`COND-FALSE` are always type-safe, since the language already requires both arms
to unify. Conditionals are the densest source of real logic in Terraform modules and this
group should be considered the core of the catalogue.

### Boolean and comparison

| ID | Original | Mutated | Fault modelled |
| --- | --- | --- | --- |
| `BOOL-AND-OR` | `a && b` | `a \|\| b` | Wrong combinator |
| `BOOL-OR-AND` | `a \|\| b` | `a && b` | Wrong combinator |
| `BOOL-NEGATE-INSERT` | `e` (boolean-typed) | `!(e)` | Inverted condition |
| `BOOL-NEGATE-REMOVE` | `!e` | `e` | Inverted condition |
| `CMP-EQ-NE` | `a == b` | `a != b` | Inverted equality |
| `CMP-BOUNDARY` | `a < b` | `a <= b`, and each of `>`,`>=`,`<=` to its neighbour | Off-by-one boundary |
| `CMP-INVERT` | `a < b` | `a > b` | Reversed comparison |

### Arithmetic and numeric literals

| ID | Original | Mutated |
| --- | --- | --- |
| `ARITH-SWAP` | `a + b` | `a - b`, and the reverse; `a * b` ↔ `a / b` |
| `NUM-OFF-BY-ONE` | numeric literal `n` | `n+1` and `n-1` |
| `NUM-ZERO` | numeric literal `n` (n ≠ 0) | `0` |
| `NUM-NEGATE` | numeric literal `n` | `-n` |

### Literals and strings

| ID | Original | Mutated |
| --- | --- | --- |
| `BOOL-LITERAL-FLIP` | `true` / `false` | the other |
| `STR-EMPTY` | string literal | `""` |
| `STR-CASE` | string literal | case-flipped (models tag/enum casing faults) |
| `NULL-INJECT` | any nullable argument value | `null` |

### Collections

| ID | Original | Mutated |
| --- | --- | --- |
| `COLL-DROP-FIRST` | tuple with ≥ 2 elements | first element removed |
| `COLL-DROP-LAST` | tuple with ≥ 2 elements | last element removed |
| `COLL-EMPTY` | tuple or object | `[]` / `{}` |
| `COLL-DROP-ENTRY` | object entry | entry removed (one mutant per entry) |
| `COLL-REVERSE` | tuple with ≥ 2 elements | order reversed |

`COLL-DROP-ENTRY` on a `tags` map is the language-level generalisation of Oasis's
`MUT-TAG-002`, and it fires on every provider rather than a curated list of resource types.

### For expressions and traversals

| ID | Original | Mutated | Fault modelled |
| --- | --- | --- | --- |
| `FOR-DROP-IF` | `[for x in xs : v if c]` | `[for x in xs : v]` | Missing filter |
| `FOR-NEGATE-IF` | `... if c]` | `... if !(c)]` | Inverted filter |
| `FOR-SWAP-KV` | `{for k, v in m : k => v}` | `{for k, v in m : v => k}` | Transposed key/value |
| `FOR-DROP-GROUPING` | `{... => v...}` | `{... => v}` | Lost grouping semantics |
| `IDX-SHIFT` | `xs[0]` | `xs[1]` | Off-by-one indexing |
| `SPLAT-FIRST` | `xs[*].id` / `xs.*.id` | `[xs[0].id]` | Collapsed collection |

### Templates

| ID | Original | Mutated |
| --- | --- | --- |
| `TPL-DROP-INTERP` | `"${a}-${b}"` | `"${a}-"` (one mutant per interpolation) |
| `TPL-STRIP-FLIP` | `${~ e }` / `${ e ~}` | strip marker removed or added |
| `TPL-IF-COLLAPSE` | `%{if c}A%{else}B%{endif}` | `A`, and separately `B` |
| `HEREDOC-INDENT-FLIP` | `<<-EOT` | `<<EOT` |

Template mutations matter more in Terraform than the operator count suggests: user-data
scripts, IAM policy documents and config files are all built this way, and they are almost
never asserted on.

### Function calls

Sequencing note (M2 spec review, M2): the **hard-coded high-signal pairs and forms in this
table ship in M2**; the general, `metadata functions -json`-driven catalogue that derives
substitutions from arity and type compatibility remains M3. Two deliberately different
deliverables — a curated list now, a generated one later — and the M2 applicability matrix
covers only the curated list.

| ID | Original | Mutated | Fault modelled |
| --- | --- | --- | --- |
| `FN-SWAP` | `min` ↔ `max`, `floor` ↔ `ceil`, `upper` ↔ `lower`, `startswith` ↔ `endswith`, `alltrue` ↔ `anytrue`, `concat` ↔ `setunion` | pairwise substitution | Wrong function |
| `FN-ARG-REORDER` | `coalesce(a, b)`, `merge(a, b)`, `concat(a, b)` | arguments swapped | Wrong precedence in a merge/fallback |
| `FN-DROP-DEFAULT` | `lookup(m, k, d)` / `try(a, b)` | `lookup(m, k)` / `try(a)` | Removed fallback |
| `FN-TRY-FIRST` | `try(a, b)` | `b` | Fallback always taken |
| `FN-CAN-TRUE` | `can(e)` | `true` | Neutered guard |
| `FN-DROP-WRAPPER` | `distinct(e)`, `sort(e)`, `compact(e)`, `flatten(e)`, `toset(e)` | `e` | Removed normalisation |
| `FN-JOIN-SEP` | `join(",", xs)` | `join("", xs)` | Wrong separator |
| `FN-DROP-EXPANSION` | `f(a, xs...)` | `f(a, xs)` | Lost expansion |

`FN-DROP-WRAPPER` deserves emphasis. `toset`, `distinct` and `compact` change semantics in
ways that are easy to get wrong and almost never asserted on — `toset` in particular changes
`for_each` key derivation.

---

### The generated function families (M3e, opt-in)

Each family is a justified fault model — functions an author actually confuses — never a
signature grouping. Members are real Terraform functions; `setsubtract` is substituted only
at its exact arity of two.

| Family | Members | Justification |
| --- | --- | --- |
| order-statistics | `min`, `max` | Choosing the wrong extreme is the classic boundary fault; fully covered by the curated pair |
| rounding | `floor`, `ceil` | Rounding direction faults; fully covered by the curated pair |
| case | `upper`, `lower`, `title` | Case normalisation confusions — `title` extends the curated `upper`/`lower` pair |
| string-search | `startswith`, `endswith`, `strcontains` | Anchored-versus-unanchored match confusions — `strcontains` extends the curated pair |
| set-algebra | `concat`, `setunion`, `setintersection`, `setsubtract` | Collection-combination faults: the wrong combinator changes cardinality and order semantics |

## Tier 2 — Terraform meta-arguments and structure (`standard`)

Multiplicity expressions are shared ground since M3a.3 (review C1): the conditional, boolean
and comparison operators of Tier 1 fire *inside* a `count` or `for_each` expression — "a
mutant inside the condition executes" needs a generation site there — while literals,
collections and every other expression shape inside a meta-argument stay with the Tier 2
operators below, as their matrix rows record.

| ID | Site | Mutation | Kills when |
| --- | --- | --- | --- |
| `COUNT-ZERO` | `count = n` | `count = 0` | Any assertion counts or indexes instances |
| `COUNT-ONE` | `count = n`, n ≠ 1 | `count = 1` | An assertion checks the instance count |
| `COUNT-OFF-BY-ONE` | `count = n` | `n ± 1` | An assertion checks an exact count |
| `FOREACH-EMPTY` | `for_each = e` | `for_each = {}` / `[]` | Any assertion counts instances |
| `FOREACH-SINGLE` | `for_each = e` | expression sliced to one element | An assertion checks the instance count |
| `FOREACH-TO-COUNT` | `for_each` over a set | equivalent `count` | An assertion depends on instance *keys* rather than ordinals |
| `DYNAMIC-ZERO` | `dynamic "b" { for_each = e }` | `for_each = []` | An assertion reads the generated nested block |
| `DYNAMIC-ONE` | `dynamic "b"` | `for_each` sliced to one | An assertion counts nested blocks |
| `DEPENDS-DROP` | `depends_on = [a, b]` | entry removed / block removed | Rarely killable — a deliberate pseudo-test detector |
| `PROVIDER-ALIAS-SWAP` | `provider = aws.a` | another declared alias of the same type **and identical mock status** — a swap must never move a resource from a mocked to an unmocked provider, which would bypass the safety gates (M2 spec review, M3) | An assertion checks region/account placement |

`FOREACH-TO-COUNT` is a Terraform-specific fault that has no analogue in general-purpose
mutation testing and causes real production incidents (resource replacement on list reorder).
It is worth its own operator.

`DEPENDS-DROP` is expected to survive nearly always. That is the point: it establishes the
floor. A tool that reports it as a failure without saying so would be crying wolf, so the
diagnosis engine flags it as `structurally-unassertable` rather than as a test-suite defect.

---

## Tier 3 — Module contract (`standard`)

These target the module's public interface. For a reusable module this is the highest-value
tier, because the contract is precisely what consumers depend on.

| ID | Site | Mutation | Kills when |
| --- | --- | --- | --- |
| `VAR-DEFAULT-CHANGE` | `variable { default = v }` | type-appropriate alternative | A run block relies on the default |
| `VAR-DEFAULT-REMOVE` | `variable { default = v }` | default removed → variable becomes required | A run block omits the variable |
| `VAR-DEFAULT-NULL` | `variable { default = v }`, `nullable` not false | `null` | A run block relies on the default |
| `VAR-NULLABLE-FLIP` | `nullable = false` | `true` | A test passes `null` explicitly |
| `VAR-OPTIONAL-DEFAULT-DROP` | `optional(string, "x")` | `optional(string)` | A test exercises the object without that attribute |
| `VAR-VALIDATION-REMOVE` | `validation { }` | block removed | A test uses `expect_failures = [var.x]` |
| `VAR-VALIDATION-WEAKEN` | `validation { condition = c }` | `condition = can(var.<name>)` — always true, still references the variable | As above |
| `VAR-VALIDATION-NEGATE` | `validation { condition = c }` | `condition = !(c)` | A test asserts a *valid* value is accepted |
| `VAR-SENSITIVE-FLIP` | `sensitive = true` | `false` | Almost never — pseudo-test detector for sensitivity handling |
| `PRE-POST-REMOVE` | `lifecycle { precondition \| postcondition }` | block removed | A test uses `expect_failures` on it |
| `PRE-POST-NEGATE` | `precondition { condition = c }` | `!(c)` | A test asserts the happy path |
| `CHECK-REMOVE` | `check "x" { assert { } }` | assert removed | A test exercises the check |
| `CHECK-NEGATE` | `check` assert condition | negated | A test exercises the check |
| `OUT-VALUE-NULL` | `output { value = v }` | `null` | Covered by `EXT-OUTPUT-NULL` at smoke tier; here as a fallback |
| `OUT-SENSITIVE-FLIP` | `output { sensitive = true }` | `false` | Rarely — sensitivity pseudo-test detector |

Two review corrections (M12) are load-bearing here. `VAR-VALIDATION-WEAKEN` cannot be
`condition = true`: Terraform rejects a validation condition that does not reference its
variable ("must refer to var.<name> in order to test incoming values"), so that form is 100%
`Invalid` — `can(var.<name>)` is the always-true form that validates. And `VAR-TYPE-LOOSEN`
(`type = string` → `any`) has been **deleted from the catalogue**: its proposed killer was a
test passing a wrongly-typed value under `expect_failures`, but `expect_failures` cannot
capture type-conversion errors (verified: the run fails with "Invalid value for input
variable" *and* "Missing expected failure"), so any test that would kill the mutant reddens
the unmutated baseline. The operator would survive on every module forever and read as a
finding.

The `VAR-VALIDATION-*` and `PRE-POST-*` groups are the natural partner of `expect_failures`,
which is the one part of `terraform test` explicitly designed for negative testing and is
consistently under-used. A module with validation rules and no `expect_failures` run blocks
will fail this tier comprehensively, and the fix is mechanical.

---

## Tier 4 — Lifecycle and state safety (`deep`)

| ID | Original | Mutated | M5-0.1 kill witness |
| --- | --- | --- | --- |
| `LC-CBD-FLIP` | `create_before_destroy = true` | `false` | no kill witness under shapes (a)–(e), Terraform v1.15.8 |
| `LC-PREVENT-DESTROY-FLIP` | `prevent_destroy = true` | `false` | no kill witness under shapes (a)–(e), Terraform v1.15.8 |
| `LC-IGNORE-DROP` | `ignore_changes = [a, b]` | entry removed / block removed | witnessed under (b) and (e); admitted to M5a |
| `LC-IGNORE-ALL` | `ignore_changes = [...]` | `ignore_changes = all` | witnessed under (b) and (e); admitted to M5a |
| `LC-REPLACE-TRIGGER-DROP` | `replace_triggered_by = [...]` | entry removed | witnessed under (e); admitted to M5a |

The witness column records the M5-0.1 measurement
([`docs/research/16-m5-01-lifecycle-witnesses.md`](../research/16-m5-01-lifecycle-witnesses.md)):
non-admitted rows carry the exact annotation the decision rule fixes and reopen on a witness;
admitted rows carry the witnessed shapes whose assertion the matrix row's "Kills when" and the
catalogue fix text both name. M5a admits the three witnessed rows; the two unadmitted ones stay
dropped.

These are near-unkillable by plan-mode tests, and that is diagnostic information rather than a
defect: it tells a team that their safety rails are entirely unverified. They are gated to the
`deep` tier so they do not distort the headline score.

---

## Tier 5 — Domain / policy packs (opt-in)

Oasis-style semantic faults, kept deliberately separate from the language catalogue because
they are provider-specific and their coverage of any given module is patchy by nature. Enabled
per pack, never by default.

| Pack | Status |
| --- | --- |
| `security-aws` | **shipped** (M5c.2): exactly the 11 entries the M5-0.3 census witnessed and admitted plus the one scalar candidate #176 admitted under its own form, of 115 loadable candidates; the pack document is [pack-security-aws.md](pack-security-aws.md) |
| `security-azure`, `security-gcp`, `capacity`, `compliance` | not in M5; each follows the same admission path in later work — a seed census, an admission decision against the measured rule, then the seeded pack |

Each pack entry follows Oasis's `(resource_type, attribute)` scoping model, which is the right
call and should be adopted directly: an attribute name alone is not enough context to know
whether mutating it models a real fault. The upstream the shipped pack draws on is the rule
catalogue Checkov and Trivy already publish: each rule describes a misconfiguration that
matters; inverting it is a ready-made, curated, realistic mutation, and the entry carries the
upstream rule identifier and the catalogue's licence.

### The pack mechanism (M5c.1)

**A pack is data, not operators.** The catalogue gains exactly the operators of the **form
vocabulary** — `PACK-FLIP` (a boolean literal inverted), `PACK-REPLACE` (one literal replaced
by another) and `PACK-WIDEN-CIDR` (any scalar IPv4 CIDR literal to `0.0.0.0/0`). The M5-0.3
census found the scalar entry the third form expresses and deferred it; #176 implemented and
measured it, and its entry ships. Every enabled form gets its own matrix row
and offline generation site. Pack entries parameterise those operators. "One row per enabled
operator" stays true; SARIF's one rule per operator stays meaningful. The offline witness is a user-defined pack over
`terraform_data.input` (schema type `dynamic`, optional) in `internal/engine/testdata/packs`; it
proves the mechanism, not any shipped pack. The shipped pack is `security-aws` (M5c.2):
embedded in the binary under its reserved name, parsed by the same `ParsePack` contract a user
pack goes through — a shipped pack that fails its own contract is an init-time failure with a
test, never a silent skip — and selected by `--pack security-aws` with no registration and no
pack block. Its witnessed set is **not empty**: exactly the eleven entries — the ten the M5-0.3 census
admitted plus the #176 widen-cidr entry — and the [pack document](pack-security-aws.md) lists witnessed and unwitnessed
entries with both witness counts.

**Deduplication unchanged; provenance preserved as origins.** Deduplication is by mutated file
content and the entry sorting earliest wins; the form operators sort after every language
operator, so a language operator — `BOOL-LITERAL-FLIP`, `NUM-ZERO`, `STR-EMPTY` — owns every row
a pack entry also produced, whatever its identifier spells. M5c keeps the algorithm and the mutant identity exactly as
they are — no baseline moves, no cached verdict changes — and adds **origins**: every
`(operator, pack, entry)` whose rewrite produced the surviving bytes is recorded on the surviving
mutant, **sorted and deduplicated by `(pack, entry)`**, whichever operator owns the row;
aggregation is independent of the winner, so a language-operator row and a pack-operator row
carry origins the same way. Two packs, or two entries of one pack with different identifiers,
requesting identical bytes both appear. The report's `tier` and `operator` are the survivor's;
`origins` is present exactly when at least one pack entry contributed. Witness counting publishes
both pre- and post-deduplication figures (M5-0.3). The red proof for the gate case **disables
origin aggregation**, not merely the sort order: reversing ownership must lose no contributor.

**The pack contract.** These tables are normative; every row is pinned through the seam in
`internal/engine/pack_test.go` and carried by `just gate-m5`.

| Rule | Contract |
| --- | --- |
| File shape | an HCL file parsed with the same library as `.tf-mut.hcl`, containing only `entry "ID" { … }` blocks — one labelled block per entry, no other block type, no top-level attributes; **literal values only** — no expressions, functions, variables or interpolation; a file with anything else is a configuration error naming the position |
| Entry identity | the label `ID` is author-supplied, `[a-z0-9-]+`, unique within the pack (a repeated label is a configuration error naming both ranges), and stable across reordering; an origin's wire identity is the pair `(pack, entry)` where `entry` is that label; renaming a label is a new identity and the pack document says so |
| Entry fields | `resource_type`, `attribute`, `form`, `from`, `to`, `source_rule`, `source_licence`; the last two are required for shipped packs and optional for user packs; two entries of one pack with identical `(resource_type, attribute, form, from, to)` but different labels are **allowed** and both become origins |
| Site matching | a site is a top-level argument assignment in a `resource` body of the entry's `resource_type` whose value is a **single literal token equal to `from` after HCL literal decoding** (for `widen-cidr`, any string literal that parses as an IPv4 CIDR other than `to`); nested blocks, `dynamic` bodies, meta-arguments, `data` bodies and string-internal structure are out of M5's scope, and an entry naming one is a no-op recorded in `preview`'s pack summary |
| Evidence required | the loaded provider schema describes the attribute on that resource type, **and** the entry's `to` literal is of the schema-declared type, where a schema type of `dynamic` accepts any literal kind and a concrete type must match; otherwise no site |
| Registration | shipped packs are embedded in the binary under reserved names; a user pack is registered by a `pack "NAME" { file = "PATH" }` block in `.tf-mut.hcl`, `PATH` resolved relative to the module root; a user pack may not shadow a reserved name |
| Selection and composition | `--pack NAME[,NAME]` on `run`, `preview` and `suggest`, and `operators { packs = [...] }` in configuration, by name only; the two lists are **merged as a union**, deduplicated by name; an unknown name is refused at configuration time with exit 2; the flag is refused by name on `characterise`, `todos` and `curate`, and configuration-narrowed populations stay refused at configuration time for `curate` and `--until-dry`, as the maintainer's ruling on #97 records. Pack selection is orthogonal to `--tier`; `--operator`/`--exclude-operator` act on the form operators by identifier; a pack is disabled by not selecting it |
| Snapshot rules | the selected pack names and, for a user pack, the file's bytes join the resolved-configuration dimension of the cache key and the input-closure digest the write protocols re-check, so a pack edit is a miss and a stale verified suggestion is refused; a changed user-pack file forces the full population under `--since`, as a changed `.tf-mut.hcl` does — each selected pack is diffed on its own from its own directory, independent of the closure root, and a pack outside any git work tree forces the full population too |

| Form | Constraints, checked at load | Operator |
| --- | --- | --- |
| `flip` | `from` is the literal `true` or `false` and `to` is the other, anything else is an error | `PACK-FLIP` |
| `replace` | `from` and `to` are literals of the same kind (string, number or bool), `to` differs from `from`, and a `from` equal to `to` is an error naming the no-op | `PACK-REPLACE` |
| `widen-cidr` | `from` is exactly the sentinel `any-cidr` and `to` is exactly the IPv4 any-prefix `0.0.0.0/0`, anything else is an error; the form is IPv4-only — a malformed CIDR or a bare address, an IPv6 prefix (including an IPv4-mapped one), list-valued and nested CIDRs and adjacent-port predicates stay out of scope; the value rule refuses `to` itself, a no-op | `PACK-WIDEN-CIDR` |

The one reserved name is `security-aws`, and its pack ships; the reserved names are derived
from the embedded pack files, so each further pack reserves its name in the change that ships
it, never ahead of it. A user pack named `security-aws` is refused by name while the shipped
pack still loads. **Scoring**: when a pack is enabled its mutants enter the scored set like any
Tier 1–3 mutant; no pack is ever in `standard`, and admission of any pack to a default is a
separate evidence-carrying change, exactly the M3e posture. **Suggestions**: a pack survivor
reaches the suggestion engine through the existing fail-closed address, rendering and sensitivity
adapters and gets a suggestion where those render one and the same closed skip reason where they
do not; nothing pack-specific is built there and no per-survivor suggestion is promised.
**Suppression**: the existing operator, path and resource exclusions apply to pack mutants
unchanged.

---

## Applicability matrix

From the M2 spec review (C5, M2): "type-preserving where decidable" is a design rule, not an
applicability specification, and per-operator error counts over an unspecified catalogue
measure specification gaps rather than operator quality. This is that specification. One row
per enabled operator: the exact source forms it accepts, the type or schema evidence it needs
in order to fire, any coordinated rewrite it performs, the cases it skips and why, and the
classification its mutants are expected to reach.

Every row is fixture-backed. `internal/engine/testdata/operators` carries a generation site for
each operator, and a test in `internal/engine` fails if this table names an operator the
catalogue does not enable, if the catalogue enables one this table does not describe, or if any
operator has no site in the fixture. A row that describes an operator nothing can fire on is a
test failure rather than a plausible paragraph.

Two conventions the table relies on. **Deduplication is by mutated file content**, so where two
operators would rewrite a file identically only one mutant survives, and the entry sorting
earliest wins — `EXT-RESOURCE-DELETE` therefore subsumes `COUNT-ZERO` and `FOREACH-EMPTY`
wherever both fire, which the rows record rather than hide. And **a mutant that does not parse
is discarded before execution**, with a warning naming the operator: that is a defect in the
operator, not a finding about the module, and spending a Terraform run to discover it would be
waste.

| Operator | Source forms accepted | Evidence required | Coordinated rewrite | Skipped | Expected classification |
| --- | --- | --- | --- | --- | --- |
| `EXT-ATTR-DELETE` | A top-level argument assignment in a `resource`, `data` or `module` body | Provider schema marks the attribute optional and not required | — | Meta-arguments; attributes the schema does not describe; required attributes | `Killed` where an assertion reads the attribute; otherwise a survivor diagnosed from the delta |
| `EXT-RESOURCE-DELETE` | A `resource` with `count` or `for_each` | Every recorded consumption of the address tolerates an empty collection | — | Bare or exact-index consumers, which get `EXT-BODY-BLANK` instead | `Killed` where an assertion counts or indexes instances |
| `EXT-BODY-BLANK` | A `resource` block with at least one schema-optional argument | Provider schema optionality of every argument removed | — | Resources with no optional argument, which raise the unanswerable-resource warning | `Killed` or `KilledByError`; a survivor makes the resource pseudo-tested |
| `EXT-OUTPUT-NULL` | An `output` block with a `value` | — | — | Outputs with no value | `Killed` where an assertion reads the output |
| `EXT-LOCAL-NULL` | A `locals` entry | — | — | — | `Killed` where an assertion reads anything downstream |
| `EXT-MODULE-INPUT-DELETE` | An input argument on a local `module` call | The child module declares a variable of that name | — | Inputs the child does not declare | `Invalid` where the child variable has no default; otherwise classified by execution |
| `COND-SWAP` | `c ? a : b` | — | Both arms are replaced with each other's source text | Template conditionals, which `TPL-IF-COLLAPSE` owns | `Killed` where an assertion distinguishes the arms |
| `COND-NEGATE` | `c ? a : b` | — | — | Template conditionals | As above; deduplicated against `BOOL-NEGATE-INSERT` where the predicate is a comparison |
| `COND-TRUE` | `c ? a : b` | Both arms already unify by the language's own rule, so the collapse is type-safe | — | Template conditionals | `Unobservable` where the collapsed arm is the one current inputs select |
| `COND-FALSE` | `c ? a : b` | As above | — | Template conditionals | As above |
| `BOOL-AND-OR` | `a && b` | — | — | — | `Killed` where an assertion exercises an input the combinators disagree on |
| `BOOL-OR-AND` | `a \|\| b` | — | — | — | As above |
| `BOOL-NEGATE-REMOVE` | `!e` | — | — | Unary operators other than logical not | `Killed` where an assertion exercises the condition |
| `BOOL-NEGATE-INSERT` | A binary comparison or logical operation | The operation's result type is boolean, which the operator itself decides | — | Any other expression, whose type is not decidable here | As above |
| `CMP-EQ-NE` | `a == b`, `a != b` | — | — | — | `Killed` where an assertion exercises a value equality matters for |
| `CMP-BOUNDARY` | `<`, `<=`, `>`, `>=` | — | — | — | `Killed` where an assertion exercises the boundary value |
| `CMP-INVERT` | `<`, `<=`, `>`, `>=` | — | — | — | `Killed` where an assertion exercises either side |
| `ARITH-SWAP` | `+`, `-`, `*`, `/` | — | — | — | `Killed` where an assertion reads the computed value |
| `NUM-OFF-BY-ONE` | A numeric literal | The literal's own type | — | Numbers inside a meta-argument, which Tier 2 owns | `Killed` where an assertion checks the exact value |
| `NUM-ZERO` | A numeric literal other than zero | As above | — | Zero, and meta-argument numbers | As above |
| `NUM-NEGATE` | A numeric literal other than zero | As above | — | As above | `Killed` where an assertion checks the sign |
| `BOOL-LITERAL-FLIP` | `true` or `false` | The literal's own type | — | — | `Killed` where an assertion reads the flag |
| `STR-EMPTY` | A quoted string literal, and the literal segments of an interpolated template | The value is a string; the segment's source carries no escape | — | Heredocs as a whole, already-empty strings, and segments containing a backslash | `Killed` where an assertion compares the string |
| `STR-CASE` | As `STR-EMPTY` | As above, and the text contains a letter | — | As above, and text with no letters | As above |
| `NULL-INJECT` | A top-level argument of a `resource` or `data` block | Provider schema marks the attribute optional and not required | — | Values that are already null; non-resource contexts | `Killed` or `KilledByError` where the attribute is read |
| `COLL-DROP-FIRST` | A tuple with at least two elements | — | — | Tuples with fewer than two elements | `Killed` where an assertion reads the collection |
| `COLL-DROP-LAST` | As above | — | — | As above | As above |
| `COLL-EMPTY` | Any tuple or object constructor | — | — | — | As above |
| `COLL-DROP-ENTRY` | One entry of an object constructor | — | — | — | `Killed` where an assertion reads that entry |
| `COLL-REVERSE` | A tuple with at least two elements | — | Every element is replaced with its mirror's source text | Tuples with fewer than two elements | `Killed` where an assertion reads the collection in order; `Unobservable` where order does not reach the plan |
| `FOR-DROP-IF` | A `for` expression with an `if` clause | The `if` keyword is present between the value and condition ranges | — | `for` expressions with no filter | `Killed` where an assertion exercises input the filter excludes |
| `FOR-NEGATE-IF` | As above | — | — | As above | `Killed` where an assertion exercises input the filter selects |
| `FOR-SWAP-KV` | An object `for` expression with a key expression | — | Key and value are replaced with each other's source text | Tuple `for` expressions, which have no key | `Killed` where an assertion reads the resulting keys |
| `FOR-DROP-GROUPING` | An object `for` expression in grouping mode | The `...` marker is present after the value | Non-grouping `for` expressions | — | `Killed` where an assertion reads a grouped value |
| `IDX-SHIFT` | A literal numeric index, whether an `IndexExpr` or an index step folded into a traversal | The key is a number literal | — | Computed and string keys | `KilledByError` or `Killed` where the index is read |
| `SPLAT-FIRST` | A splat expression | The marker range lies inside the expression | Source and projection are rebuilt around a literal index | — | `Killed` where an assertion reads the whole projection |
| `TPL-DROP-INTERP` | One interpolation of a template | The `${` and `}` delimiting it are locatable | — | Interpolations whose delimiters cannot be located | `Killed` where an assertion reads the rendered string |
| `TPL-STRIP-FLIP` | An interpolation carrying `${~` or `~}` | The marker is already present | — | Interpolations with no strip marker: adding one is a no-op unless adjacent whitespace exists, and would emit mutants unobservable by construction | `Unobservable` unless whitespace reaches an assertion |
| `TPL-IF-COLLAPSE` | `%{if c}A%{else}B%{endif}` | The conditional's source begins with `%{` | The whole directive is replaced by one branch's source | Ternary conditionals, which the `COND-*` operators own | `Killed` where an assertion exercises the other branch |
| `HEREDOC-INDENT-FLIP` | A template whose source begins `<<-` | — | — | Quoted templates and plain heredocs | `Killed` where an assertion compares rendered indentation |
| `FN-SWAP` | A call to one of the curated pairs: `min`/`max`, `floor`/`ceil`, `upper`/`lower`, `startswith`/`endswith`, `alltrue`/`anytrue`, `concat`/`setunion` | The function name is in the curated table | — | Every other function; the metadata-driven catalogue is M3 | `Killed` where an assertion reads a value the pair disagrees on |
| `FN-ARG-REORDER` | `coalesce`, `merge` or `concat` with at least two arguments | As above | The first two arguments are replaced with each other's source text | Calls with fewer than two arguments | `Killed` where an assertion reads a value precedence matters for |
| `FN-DROP-DEFAULT` | `lookup(m, k, d)` with three arguments; `try(a, b, …)` with at least two | Argument count | — | `lookup` with two arguments; `try` with one | `Killed` or `KilledByError` where the fallback path is exercised |
| `FN-TRY-FIRST` | `try(a, b, …)` with at least two arguments | Argument count | — | `try` with one argument | `Killed` where an assertion exercises the primary path |
| `FN-CAN-TRUE` | `can(e)` | Argument count of one | — | — | `Killed` where an assertion exercises input the guard rejects |
| `FN-DROP-WRAPPER` | `distinct`, `sort`, `compact`, `flatten` or `toset` with one argument | The function name is in the curated table and the call is not variadic | — | Variadic calls and every other function | `Killed` where an assertion reads the normalised value |
| `FN-JOIN-SEP` | `join(sep, xs)` where `sep` is a non-empty string literal | The separator is a quoted literal with content | — | Computed separators and already-empty ones, which model no fault | `Killed` where an assertion reads the joined string |
| `FN-DROP-EXPANSION` | A call whose final argument is expanded with `...` | The `...` marker is locatable before the closing parenthesis | — | Calls with no expansion | `Killed` or `KilledByError` where the call's result is read |
| `FN-FAMILY-SWAP` | A call to a member of a generated semantic family (M3e), behind `--generated-functions` | The family table in the Tier 1 section is the fault model; signature compatibility is not one (M3 spec review C7) | `core::` aliases canonicalised, the caller's spelling preserved; curated identifiers win deduplication; `setsubtract` substituted only at its exact arity; **not in `standard`** — admission requires the published M3e measurement, in a separate change | Cross-family pairs (`file` -> `upper` is the named impossible case) | `Killed` where an assertion reads a value the family members disagree on |
| `COUNT-ZERO` | `count = e` on a `resource` | Every recorded consumption tolerates an empty collection | — | Bare and exact-index consumers; `dynamic` blocks, which `DYNAMIC-ZERO` owns | Deduplicated against `EXT-RESOURCE-DELETE`, which emits the same mutant wherever both fire |
| `COUNT-ONE` | `count = n` where `n` is a literal other than one | The literal's own type | — | Computed counts | `Killed` where an assertion checks the instance count |
| `COUNT-OFF-BY-ONE` | `count = n` where `n` is a literal | As above | — | Computed counts; `n - 1` is skipped where `n` is one, so the multiplicity gate is not bypassed | `Killed` where an assertion checks an exact count |
| `FOREACH-EMPTY` | `for_each = e` on a `resource` | Every recorded consumption tolerates an empty collection | — | As `COUNT-ZERO` | Deduplicated against `EXT-RESOURCE-DELETE` wherever both fire |
| `FOREACH-TO-COUNT` | `for_each = toset(x)` or `for_each = [ … ]` on a `resource` | The collection is a set built from an indexable list | `count = length(x)` replaces the `for_each`, and every `each.key` and `each.value` in the block becomes `x[count.index]` | Map collections, whose keys `count` cannot reproduce | `Killed` where an assertion depends on instance keys rather than ordinals |
| `DYNAMIC-ZERO` | `for_each` inside a `dynamic` block | — | — | — | `Killed` where an assertion reads the generated nested block. Classified end-to-end in M3 (#50) on the network-gated `aws-mocked` fixture — a `dynamic "attribute"` on `aws_dynamodb_table`, killed by a length assertion; the offline `dynamic` fixture remains its generation-site witness |
| `DEPENDS-DROP` | `depends_on = [ … ]` | — | The whole line is removed, so no blank line is left behind | — | `StructurallyUnassertable`: ordering has no projection an assertion can read |
| `PROVIDER-ALIAS-SWAP` | `provider = <name>.<alias>` on a `resource` | Another alias of the same provider is declared, and the two have identical mock status | — | Swaps that would cross from a mocked configuration to an unmocked one — such a swap would route execution past a safety gate that has already run | `Killed` where an assertion checks placement |
| `VAR-DEFAULT-CHANGE` | `default = v` in a `variable` block | The default's own type is decidable from the literal | — | Defaults whose type is not decidable | `Killed` where a run block relies on the default |
| `VAR-DEFAULT-REMOVE` | As above | — | The whole line is removed | — | `KilledByError` where a run block omits the variable |
| `VAR-DEFAULT-NULL` | As above | The variable does not declare `nullable = false` | — | Variables declared not nullable, where the mutant is doomed | `Killed` where a run block relies on the default |
| `VAR-NULLABLE-FLIP` | `nullable = false` | The literal is exactly `false` | — | `nullable = true`, which models no fault | `Killed` where a test passes null explicitly |
| `VAR-OPTIONAL-DEFAULT-DROP` | `optional(type, default)` inside a `type` constraint | The call has exactly two arguments | — | `optional(type)` | `Killed` where a test exercises the object without that attribute |
| `VAR-VALIDATION-REMOVE` | A `validation` block inside a `variable` | The enclosing variable is known | The whole block is removed | `validation` blocks outside a variable | `StructurallyUnassertable` unless a run block uses `expect_failures` |
| `VAR-VALIDATION-WEAKEN` | `condition` inside a `validation` block | The enclosing variable's name | Emits exactly `can(var.<name>)` | — | `StructurallyUnassertable` unless exercised. `condition = true` is never emitted: Terraform rejects a validation condition that does not refer to its own variable, so that form is 100% `Invalid` |
| `VAR-VALIDATION-NEGATE` | As above | — | — | — | `KilledByError` where a run block passes a valid value |
| `VAR-SENSITIVE-FLIP` | `sensitive = true` in a `variable` | The literal is exactly `true` | — | `sensitive = false` | Rarely killed: a sensitivity pseudo-test detector |
| `PRE-POST-REMOVE` | A `precondition` or `postcondition` block | — | The whole block is removed | — | `StructurallyUnassertable` unless a run block uses `expect_failures` |
| `PRE-POST-NEGATE` | `condition` inside a `precondition` or `postcondition` | — | — | — | `KilledByError` where a run block asserts the happy path |
| `CHECK-REMOVE` | A `check` block containing at least one `assert` | The number of assertions the check declares | Where the check declares one assertion the whole `check` block is removed, because Terraform rejects a check with none and the mutant would be 100% `Invalid`; where it declares several, one assertion is removed at a time | `check` blocks with no assertion; `assert` blocks in test files, which are never mutated | `StructurallyUnassertable` unless the check is exercised |
| `CHECK-NEGATE` | `condition` inside a `check`'s `assert` | As above | — | — | `Killed` where a run block exercises the check |
| `OUT-SENSITIVE-FLIP` | `sensitive = true` in an `output` | The literal is exactly `true`, and the output's value reads no variable the module declares sensitive | — | `sensitive = false`; outputs whose value reads a sensitive variable, where Terraform refuses the non-sensitive output outright and the mutant is doomed | Rarely killed: a sensitivity pseudo-test detector |
| `LC-IGNORE-DROP` | An entry of `ignore_changes = [ … ]` inside a `resource`'s `lifecycle` block | — | A lone entry's removal takes the whole argument line, because the kill witnesses recorded the argument's removal rather than a list whose emptiness models no fault | `ignore_changes = all`, which `LC-IGNORE-ALL` owns; an empty list, which has no entry to drop | `Killed` where a day-two run pair shares one `state_key` and the second run asserts the attribute held — `terraform_data.<subject>.input == "old"` (shapes (b) and (e)); an identical fingerprint is `StructurallyUnassertable`, never `Unobservable` |
| `LC-IGNORE-ALL` | `ignore_changes = [ … ]` with at least one entry | — | — | `ignore_changes = all`, which models no fault; an empty list, which has no entry to widen | `Killed` where a day-two run pair shares one `state_key` and the second run asserts the attribute moved — `terraform_data.<subject>.input == "new"` (shapes (b) and (e)); an identical fingerprint is `StructurallyUnassertable` |
| `LC-REPLACE-TRIGGER-DROP` | An entry of `replace_triggered_by = [ … ]` inside a `resource`'s `lifecycle` block | — | A lone entry's removal takes the whole argument line, as `LC-IGNORE-DROP` | — | `Killed` where a second apply over one `state_key` changes the trigger and the assertion compares instance ids — `terraform_data.<subject>.id != run.<first>.subject_id` (shape (e)); an identical fingerprint is `StructurallyUnassertable` — the empty canonical delta beside a real phase-one kill is the recorded M5-0.1 finding |
| `PACK-FLIP` | A top-level argument assignment in a `resource` body of a selected pack entry's `resource_type` and `attribute`, whose value is the single boolean literal token equal to the entry's `from`; form `flip` | The loaded provider schema describes the attribute on the resource type, and its declared type is `bool` or `dynamic` | The literal becomes the entry's `to`; the mutant carries the `(pack, entry)` origins that asked for it | Nested blocks, `dynamic` bodies, meta-arguments, `data` bodies and string-internal structure (out of M5's scope: a no-op in `preview`'s pack summary); attributes the schema does not describe; a boolean the language operator also flips, where `BOOL-LITERAL-FLIP` owns the row and this entry is one of its origins | `Killed` where an assertion reads the attribute; a survivor is diagnosed from its delta like any Tier 1–3 mutant |
| `PACK-REPLACE` | As above for a string, number or boolean literal equal to the entry's `from`; form `replace` | The schema describes the attribute, and the entry's `to` is of the declared type — `dynamic` accepts any literal kind, a concrete type must match | The literal becomes the entry's `to`, rendered as an HCL literal; origins as above | As above; a rewrite a language operator also produces (`NUM-ZERO` for a `to` of `0`, `STR-EMPTY` for `""`), where that operator owns the row and the entry is an origin | `Killed` where an assertion reads the attribute |
| `PACK-WIDEN-CIDR` | As above for a string literal that parses as an IPv4 CIDR other than the entry's `to`; form `widen-cidr` | The schema describes the attribute, and the entry's `to` is of the declared type — `dynamic` accepts the string, a concrete type must match | The literal becomes `0.0.0.0/0`; origins as above | Malformed CIDRs and bare addresses; the `to` value itself, a no-op; IPv6 prefixes, including IPv4-mapped ones; list-valued and nested CIDRs; adjacent-port predicates; `dynamic` bodies, meta-arguments, `data` bodies, string-internal structure; attributes the schema does not describe | `Killed` where an assertion reads the attribute |

### Tier 4, Tier 5 and the packs

Three rows belong to Tier 4 — the lifecycle operators M5-0.1 admitted on their kill witnesses,
enabled under `--tier deep`, which includes everything `standard` enables. Two rows belong to
Tier 5 — the pack form operators — and to no breadth tier: packs land behind `--pack` selection,
never inside a tier, and the form operators fire only where a selected pack's entry
parameterises them. Their generation site is the `packs` fixture, witnessed in isolation because
a language operator owns every row a pack entry also produces.

## Suppression

Operators can be suppressed at four scopes:

1. **Config file** — `.tf-mut.hcl`, by operator ID, tier, pack, path glob, or resource address.
2. **Inline comment** — `# tf-mut:disable COND-SWAP — provider ignores this attribute` on the
   line above the site. A reason is mandatory; the tool reports suppressions without reasons
   as a warning.
3. **Baseline file** — accept the current set of survivors and fail only on new ones, so the
   tool can be adopted on an existing codebase without a flag day.
4. **Automatic** — sites inside `.terraform/`, vendored registry modules, generated files, and
   anything matching `.gitignore`.

## Operator count

Estimates re-baselined after the adversarial review (M9) counted mutation sites in the first
500 lines of `terraform-aws-modules/terraform-aws-vpc` — a canonical real module: 24 resource
blocks, **213 argument assignments**, 118 string literals, 51 conditionals. The original
estimates were 3–8× low.

| Tier | Operators | Typical mutants on 500 lines of a real module |
| --- | --- | --- |
| 0 — extreme (`smoke`) | 6 | 150–250 |
| 1 — language (`standard`) | ~35 | 800–1500 |
| 2 — meta-arguments (`standard`) | 10 | 20–60 |
| 3 — contract (`standard`) | 15 | 40–120 |
| 4 — lifecycle (`deep`) | 3 enabled of 5 designed | 5–20 |
| 5 — domain packs (opt-in) | 3 form operators; the shipped `security-aws` enables 11 of the census's 115 candidates | 0–50 |

Duration depends dominantly on provider schema size and test selection, not on operator count
(review C1). With the two-phase execution and run-block selection of the product design, the
realistic targets are: `smoke` tier in minutes on a real-provider module; a full `standard`
sweep (~1200–2000 mutants here) is scheduled work; `--since`-scoped runs over a typical diff
are the sub-minute case. The previously published "15–30 seconds on eight cores" figure was a
null-provider artefact and is withdrawn.
