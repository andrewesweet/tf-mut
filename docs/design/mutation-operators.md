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
   be fast by default and thorough on request.

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

## Tier 2 — Terraform meta-arguments and structure (`standard`)

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
| `PROVIDER-ALIAS-SWAP` | `provider = aws.a` | another declared alias of the same type | An assertion checks region/account placement |

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

| ID | Original | Mutated |
| --- | --- | --- |
| `LC-CBD-FLIP` | `create_before_destroy = true` | `false` |
| `LC-PREVENT-DESTROY-FLIP` | `prevent_destroy = true` | `false` |
| `LC-IGNORE-DROP` | `ignore_changes = [a, b]` | entry removed / block removed |
| `LC-IGNORE-ALL` | `ignore_changes = [...]` | `ignore_changes = all` |
| `LC-REPLACE-TRIGGER-DROP` | `replace_triggered_by = [...]` | entry removed |

These are near-unkillable by plan-mode tests, and that is diagnostic information rather than a
defect: it tells a team that their safety rails are entirely unverified. They are gated to the
`deep` tier so they do not distort the headline score.

---

## Tier 5 — Domain / policy packs (opt-in)

Oasis-style semantic faults, kept deliberately separate from the language catalogue because
they are provider-specific and their coverage of any given module is patchy by nature. Enabled
per pack, never by default.

| Pack | Examples |
| --- | --- |
| `security-aws` | `acl` private → public-read; `storage_encrypted`/`encrypted` true → false; `block_public_*` true → false; restrictive CIDR → `0.0.0.0/0`; `publicly_accessible` false → true; `deletion_protection` true → false; `versioning` enabled → disabled; IAM policy `Effect` Deny → Allow, `Action` narrowed → `*` |
| `security-azure` | Equivalent flags on `azurerm_*` |
| `security-gcp` | Equivalent flags on `google_*` |
| `capacity` | Instance sizing drift, `min_size`/`desired_capacity`/`replicas` reductions |
| `compliance` | Required tag/label removal; environment tag `prod` → `dev` |

Each pack entry follows Oasis's `(resource_type, attribute)` scoping model, which is the right
call and should be adopted directly: an attribute name alone is not enough context to know
whether mutating it models a real fault.

An additional source these packs should draw on: the rule catalogues already published by
Checkov, tfsec/Trivy and the CIS benchmarks. Each rule describes a misconfiguration that
matters; inverting it is a ready-made, curated, realistic mutation. That gives the packs a
maintained upstream instead of a hand-written list.

---

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
| 4 — lifecycle (`deep`) | 5 | 5–20 |
| 5 — domain packs (opt-in) | ~30 per pack | 0–50 |

Duration depends dominantly on provider schema size and test selection, not on operator count
(review C1). With the two-phase execution and run-block selection of the product design, the
realistic targets are: `smoke` tier in minutes on a real-provider module; a full `standard`
sweep (~1200–2000 mutants here) is scheduled work; `--since`-scoped runs over a typical diff
are the sub-minute case. The previously published "15–30 seconds on eight cores" figure was a
null-provider artefact and is withdrawn.
