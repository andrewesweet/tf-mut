# The expression consumer measurement (#111)

Date: 2026-09-15 · Ticket: [#111](https://github.com/andrewesweet/tf-mut/issues/111) ·
Programme: [#86](https://github.com/andrewesweet/tf-mut/issues/86) · Decision record:
[ADR-0004](../adr/0004-expression-boundary-and-atomic-replacement.md)

Standing rule 4 applied to a representation choice: before the discovery boundary's
expression type moves, measure which consumers actually need the concrete type. The
measurement is compiler-derived — the compiler produces the classification, not a reading
of the code — and it was performed in throwaway scratch commits, then reverted. Nothing in
this document changes production behaviour; `Attribute.Expr` and `Validation.Condition`
keep their current types.

**Outcome in one line: of 17 access sites on the two boundary fields, 13 are satisfied by
`hcl.Expression` unchanged, 4 genuinely require concrete `hclsyntax` nodes, and no
consumer is served by neither — so ADR-0004's condition for reopening the custom-expression
question is not met, and the named accessor is its complete answer.**

## Method

Three compiler-driven passes, each on a scratch branch that was never merged:

1. **The retype.** `Attribute.Expr` and `Validation.Condition` retyped from
   `hclsyntax.Expression` to `hcl.Expression` (scratch commit `3b6d133`). The build
   enumerated every consumer whose code needs more than the interface's four members —
   `Value`, `Variables`, `Range`, `StartRange` (the diagnostic names the gap exactly:
   `hcl.Expression does not implement hclsyntax.Expression (missing method
   walkChildNodes)`). Where a package failed, its failing call sites were bridged with the
   minimal assertion the accessor performs (`native, _ := expr.(hclsyntax.Expression)`) so
   the compiler could walk onward and enumerate the next package; bridging does not change
   what downstream consumers see — the field's type is the treatment.
2. **The refinement.** Each bridged helper's own signature was then narrowed to
   `hcl.Expression` and the build re-run. A helper whose body compiles against the
   interface was classified *satisfied* (its bridge was an artefact of the helper's own
   parameter spelling, not a requirement); a helper that still failed was classified
   *genuine*. This step matters: without it, the bridge would have silently hidden
   downstream sites behind a helper that never needed native syntax — the refinement
   surfaced one (`mutatedMultiplicity`'s second return) that the first pass's bridge had
   masked.
3. **The census.** The two fields renamed (`Expr` → `ExprCensus`,
   `Condition` → `ConditionCensus`) and the build/vet run to convergence, renaming only the
   positions the compiler names each round. The converged failure list is the authoritative
   count of *every* access site, reads and writes, production and test — including the
   assertion sites a method-set check cannot see (a type assertion against an interface
   field compiles; the census catches the access itself).

The census pass is what licenses the classification's completeness: a type switch or
assertion is invisible to pass 1, so "satisfied" would otherwise be claimed from absence of
errors rather than from an enumerated site list. Every site in the census is classified
below; nothing is judged by eye that the compiler could name.

Reproduction, from a clean tree on a scratch branch: apply the two field changes, run
`go build ./...` and `go vet ./...`, bridge/narrow per the failure list until green, then
apply the rename and iterate `go vet ./...` to convergence. The scratch branches carried
the exercise as two commits (`scratch/retype-measurement`); they are throwaway and not
pushed.

## The compiler's failure list

Failures in the order the passes surfaced them (file:line as of the scratch commit):

| Pass | Site | Rejected use |
| --- | --- | --- |
| retype 1 | `internal/discovery/closure.go:122` | `referencesOf(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 1 | `internal/discovery/module.go:331` | `literalString(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 2 | `internal/mutation/syntax.go:186` | `literalBool(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 2 | `internal/mutation/syntax.go:222` | `literalBool(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 3 | `internal/characterise/synthesise.go:309` | `mineExpression(validation.Condition)` — parameter is `hclsyntax.Expression` |
| retype 3 | `internal/characterise/synthesise.go` `typedValue` | `synthesiseType(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 4 | `internal/characterise/synthesise.go` `typedValue` | `synthesiseType(attribute.Expr)` — parameter is `hclsyntax.Expression` |
| retype 5 | `internal/engine/conditional.go:165` | `return attribute.Expr` — return type is `hclsyntax.Expression` |
| retype 5 | `internal/engine/conditional.go:487` | `return attribute.Expr` — return type is `hclsyntax.Expression` |
| refinement | `internal/engine/conditional.go:166` | second `return attribute.Expr` in `mutatedMultiplicity`, masked by pass 1's bridge until the helper signatures were narrowed |

`go vet ./...` — which type-checks every `_test.go` — reported **zero test-file failures**
throughout: no test reads either field, and the one test that constructs a
`discovery.Validation` passes a native expression, which satisfies either type. The
programme contract's "every existing test passes unchanged in name and in assertion" is
compile-true under the retype before any migration work begins.

## The classification

### Satisfied by `hcl.Expression` — 13 sites (8 read-consumers, 5 producers/constructions)

Read-consumers — each uses only interface members, verified by the refinement pass
compiling the consuming function with its signature narrowed to the interface:

| Site | What it does | Members used |
| --- | --- | --- |
| `internal/discovery/module.go:331` | module-call `source` literal | `Value` (`literalString` narrowed) |
| `internal/mutation/syntax.go:186` | `nullable` flag read | `Value` (`literalBool` narrowed) |
| `internal/mutation/syntax.go:222` | `sensitive` flag read | `Value` (`literalBool` narrowed) |
| `internal/characterise/synthesise.go:183` | `satisfiesOwnConstraints` | `Value` |
| `internal/characterise/synthesise.go:250` | validation-condition evaluation | `Value` |
| `internal/characterise/plan.go:229` | `sensitive`/`ephemeral` flag read | `Value` |
| `internal/characterise/plan.go:648` | variable `default` read | `Value` |
| `internal/engine/conditional.go:489` | `namedAttribute` result → `literalValue` | `Value` (return narrowed; `literalValue` already took the interface) |

Producers and test construction — a native value is written *into* the field, which the
interface accepts unchanged (native implements it):

| Site | What it writes |
| --- | --- |
| `internal/discovery/module.go:294` | `Validation{Condition: …}` from the native HCL parse |
| `internal/discovery/module.go:362` | `Attribute{Expr: …}` from the native HCL parse |
| `internal/discovery/jsonconfig.go:466` | `Attribute{Expr: …}` from a re-parsed `.tf.json` declaration |
| `internal/discovery/jsonconfig.go:539` | `Validation{Condition: …}` from a re-parsed `.tf.json` condition |
| `internal/characterise/functions_test.go:46` *(test)* | `Validation{Condition: …}` with a test-built native expression |

### Genuinely requiring concrete `hclsyntax` nodes — 4 sites

Each site's consuming function type-switches on concrete nodes or walks the expression's
children — structure the interface does not expose. This is the ticket's stated real
requirement: the mutation-adjacent readers inspect or rewrite the tokens an expression
owns, and only native syntax owns tokens.

| Site | Consuming function | Why the interface cannot serve it |
| --- | --- | --- |
| `internal/discovery/closure.go:122` | `referencesOf` → `collectRefs` | type-switches `ScopeTraversalExpr`, `RelativeTraversalExpr`, `SplatExpr`, `ForExpr`, `IndexExpr`, … and recurses through their child expressions to build the closure's reference set |
| `internal/characterise/synthesise.go:309` | `mineExpression` | type-switches `FunctionCallExpr`/`BinaryOpExpr` and inspects `Args`/`Op` to mine `contains(x, var.n)` and equality idioms out of a validation condition |
| `internal/characterise/synthesise.go` `typedValue` | `synthesiseType` | type-switches `ScopeTraversalExpr`/`FunctionCallExpr`/`ObjectConsExpr` and walks the type-constraint call tree to synthesise the declared type's simplest inhabitant |
| `internal/engine/conditional.go:166` | `mutatedMultiplicity` → `evaluateMultiplicity` → `supportedMultiplicityForm` | type-switch admitting exactly the multiplicity forms the static `NoCoverage` evaluator can walk (`LiteralValueExpr`, `ScopeTraversalExpr`, `ConditionalExpr`, `BinaryOpExpr`, `UnaryOpExpr`, `ParenthesesExpr`, `TupleConsExpr`); the switch is the evaluator's admission boundary |

One genuine site is inside `internal/discovery` itself; per #114, native-syntax use inside
the anti-corruption layer is legitimate — what leaves the boundary is what the migration
sequence governs.

### Consumers served by neither — none

No consumer requires an expression capability outside `hcl.Expression`'s members *and*
outside native syntax. The custom-expression-model question ADR-0004 reserved is therefore
closed on evidence: its reopen condition ("a measured consumer that requires one") did not
fire. No expression model is introduced by this ticket or warranted by this measurement.

## Count and per-package breakdown

17 access sites in total: 13 satisfied by `hcl.Expression` — 8 read-consumers plus 5
producer/construction sites — and 4 genuinely native.

| Package | Sites | Satisfied | Genuine native | Notes |
| --- | --- | --- | --- | --- |
| `internal/discovery` | 6 | 5 (4 producers + 1 read) | 1 (`closure.go:122`) | the read satisfied here is `literalString`, narrowed in the refinement |
| `internal/characterise` | 7 | 5 (4 reads + 1 test construction) | 2 (`synthesise.go:309`, `typedValue`) | the two pinning-side readers dominate |
| `internal/mutation` | 2 | 2 | 0 | see the scope note below |
| `internal/engine` | 2 | 1 | 1 (`conditional.go:166`) | |
| `internal/suggest` | 0 | — | — | `patch.go`'s `.Expr()` is `hclwrite`'s own method, not a boundary field |
| `internal/config` | 0 | — | — | its `evaluate` reads `*hclsyntax.Attribute`, its own parse |
| every other package and `cmd/` | 0 | — | — | |

## What this tells the migration tickets

- **#112 (the catalogue) is smaller than its title.** The mutation catalogue's token
  rewriting does not flow through the discovery boundary at all: its operators work on
  `*hclsyntax.Attribute` values the generator re-parses from source itself
  (`internal/mutation/sites.go`'s visitor). The catalogue's only reads of the boundary
  fields are the two `literalBool` sites — both satisfied by the interface, with no
  accessor call. Its one obligation under the programme is to stop depending on the
  concrete type *flowing through the boundary*, which after the migration is a property it
  already has.
- **#113's genuine sites are the two characterise readers** (`mineExpression`,
  `synthesiseType`) — the validation-function table itself evaluates through `Value` and
  never needs the accessor. Every engine/configuration consumer in #113 is
  interface-satisfied.
- **#114's one genuine site is the conditional evaluator** (`mutatedMultiplicity`); the
  TODO source quoter re-parses todo blocks natively itself and never touches the boundary
  fields. `internal/discovery`'s own reader is `closure.go:122`. Native syntax inside the
  layer remains legitimate.
- **The JSON judgement point is measurable, and this retype is what relaxes it.** Today a
  `.tf.json` variable whose type constraint or validation condition does not re-parse into
  native syntax is dropped at the boundary (`jsonconfig.go:460`, `:532` — fail-closed, but
  not the same as reading it). Under the interface boundary those sites can publish what
  the declaration actually says and let each consumer decide with `Value` — the four
  genuine sites above would then answer `NativeExpression` → `false` and fail closed
  individually, which is #86 story 36's behaviour. That change belongs to the migration
  tickets, not this one; this measurement is its evidence base.
- **The interface is already house style where no boundary field forced the concrete
  type**: `engine`'s variable-file reader already accumulates `map[string]hcl.Expression`,
  and `graph.go`'s JSON link takes the interface. The migration completes a direction the
  code has already started.

## What this ticket ships

Only the measurement (this document) and the accessor:

```go
func NativeExpression(expr hcl.Expression) (hclsyntax.Expression, bool)
```

It is the one named route to native syntax — documented as such on the function — and it
fails closed: a non-native expression, a nil expression and a nil native value all return
`false`, and a caller receiving `false` treats the site as unreadable rather than guessing
at syntax it cannot see. `Attribute.Expr` and `Validation.Condition` keep their current
types; every consumer compiles unchanged; `just ci` is the gate.
