# Characterisation mode — scaffolding unit tests for legacy Terraform

## 1. The problem

Most Terraform in production predates `terraform test` (v1.6, late 2023) and predates provider
mocking (v1.7), so it has no tests at all — and no safe way to start: you cannot refactor
untested configuration with confidence, and you cannot write tests for behaviour nobody
remembers specifying.

This is precisely the situation Michael Feathers' *Working Effectively with Legacy Code*
addresses with **characterisation tests**: tests that document what the code *actually does*
today, not what it should do. Feathers' loop is:

1. Write a test you know will fail, with a dummy expected value.
2. Run it; read the *actual* value out of the failure.
3. Pin the actual value as the expectation.
4. Repeat until the behaviour you care about is covered.

In a general-purpose language this loop is manual because "actual behaviour" is arbitrary
program state that a human must observe. The claim of this document: **in Terraform the loop
is fully automatable, deterministically, with zero LLM involvement** — because every piece
Feathers has to do by hand corresponds to something tf-mut already builds or something the
Terraform CLI already emits as structured data.

## 2. Why Terraform is unusually amenable

| Feathers concept | Terraform realisation | Status |
| --- | --- | --- |
| **Seam** — a place to alter behaviour without editing the code under test | `mock_provider` / `override_*`: severs every runtime dependency without touching a `.tf` file. The enabling seam that did not exist before v1.7 | Language feature |
| **Sensing point** — where behaviour becomes observable | The plan/state document. Structured, complete, addressed (`null_resource.backup[0].triggers.source`), machine-readable via `test -verbose -json` | Verified in the spike |
| **The actual value** | Harvested from `test_plan` / `test_state` JSON — no failure-message parsing, no human reading | Verified |
| **Pinning** | Generate `assert` blocks with `hclwrite` — same writer the suggested-assertion engine (product design §7) already needs | M4 machinery |
| **"Is my characterisation complete?"** | Mutation testing. This is the question mutation testing *answers* | The core product |

The keystone was verified in `research/spikes/` (see the session spike, now recorded here): a
run block with **no assertions at all** is legal, passes, and still yields the full mocked
state through `-verbose -json`:

```hcl
run "characterise_defaults" {
  command = apply          # mocked apply — verified working, exposes full state
}
```

```
outputs:  {app_id: "tfmut-null-0001", backup_count: 1}
resource: null_resource.app        -> {id: "tfmut-null-0001", triggers: {env: "dev", tier: "standard"}}
resource: null_resource.backup[0]  -> {id: "tfmut-null-0001", triggers: {source: "tfmut-null-0001"}}
```

An assertion-less run block is therefore a pure **harvest point**: scaffold it, run once,
read everything. Feathers' steps 1–3 collapse into a single execution.

## 3. The pipeline — `tf-mut characterise`

Every stage is deterministic. Inputs: the module's AST, the provider schemas, and Terraform's
own JSON output. No stage involves a language model.

### 3.1 Mock scaffolding

- Detect required providers from the AST (`required_providers` + implied).
- `terraform providers schema -json` gives every resource and data source schema in use.
- Emit one `mock_provider` block per provider. For every **computed** attribute that the
  configuration references downstream (AST tells us which), emit a pinned deterministic
  default: `id = "tfmut-<type>-0001"`, numbers `0`, and so on. Pinning is not cosmetic — the
  spike established that auto-generated mock values are **non-deterministic across runs**, so
  an unpinned computed attribute that reaches an assertion would make the scaffolded test
  flaky by construction. The schema's computed-flags plus the AST's reference set say exactly
  which attributes must be pinned; the rest are left to the generator.
- Data sources referenced by the configuration get `mock_data` blocks the same way, with
  type-driven placeholder values from the schema.

### 3.2 Input synthesis

Run blocks need variable values. In order of preference:

1. **Defaults** — characterise the module as it behaves with no inputs, where possible.
2. **Validation mining** — a `validation { condition = contains(["dev","stage","prod"], var.env) }`
   block *names the legal values in the AST*. Pick the first. Real modules encode their own
   input domains this way constantly; this is free, deterministic input generation.
3. **Type-driven synthesis** — `string` → `"tfmut-placeholder"`, `number` → `1`, `bool` →
   `true`, collections → one synthesised element, objects → recursively synthesised with
   `optional()` attributes omitted.
4. **Diagnostic-driven repair** — if a synthesised value fails at plan time (`cidrsubnet`
   needs a real CIDR), `validate -json` / the run error carries a source range and summary;
   a small table of repairs for common function domains (CIDR-shaped, ARN-shaped, region-
   shaped strings) handles the bulk. What remains is emitted as an explicit TODO placeholder
   in the generated test with the diagnostic attached — the tool marks the ~20% it cannot
   solve rather than guessing.

### 3.3 Harvest

One assertion-less run block per input scenario, `command = apply` (mocked apply is verified
safe and exposes state and outputs that plan mode leaves unknown). Run
`terraform test -verbose -json` **twice**; the double run identifies any residual volatile
values exactly as in the mutation baseline (product design §3), and volatile attributes are
excluded from pinning instead of generating flaky assertions.

### 3.4 Pinning

Generate `assert` blocks from the harvested values with `hclwrite`. Granularity is a ladder,
chosen per run block by flag, because characterisation has a brittleness trade-off that
Feathers is explicit about — pin what you need to sense, not everything:

| Level | Pins | Character |
| --- | --- | --- |
| `outputs` | Every output value | The module's contract surface; cheapest, least brittle. Default |
| `counts` | + instance counts and keys per resource | Catches `count`/`for_each` regressions |
| `configured` | + every *configured* (non-computed) attribute the plan records | Full characterisation; brittle by design |

The schema's computed-flags again do the work: `configured` level never pins a value the mock
invented, only values the configuration determined.

### 3.5 Branch expansion

A characterisation of the default path only pins one branch of every conditional. The AST
gives the branch conditions, and in Terraform they are overwhelmingly of the form
`var.x == "literal"` or `var.x != null` — the flipping value is sitting in the expression.
For each conditional reachable from a variable, synthesise the input that flips it and emit
another harvest run block. The post-MVP reference graph (product design §3) makes the
reachable-from-variable computation precise; until then a conservative AST walk suffices.

This is bounded, not exhaustive: one run block per flipped conditional, not the cross
product. Mutation testing then reports which residual branches remain unpinned — which is the
correct division of labour, because it measures the gap instead of trying to enumerate it
up front.

## 4. Curation — mutation testing as the completeness and minimality oracle

Scaffolding is the easy half. Feathers' harder question is *when to stop*, and its dual,
*what to throw away*. Both are exactly the questions the existing mutation machinery answers,
which is what makes characterisation a mode of tf-mut rather than a separate tool:

**Completeness.** Run the mutation loop against the scaffolded suite. Every surviving mutant
is un-pinned behaviour, and the suggested-assertion engine (§7 of the product design) emits
the assertion that pins it. `tf-mut characterise --until-dry` iterates scaffold → mutate →
pin-survivor-suggestions until survivors stop yielding new assertions. The result is a suite
whose completeness is *measured*, not assumed — a characterisation with a mutation score
attached.

**Minimality.** Over-pinned characterisation suites are brittle and expensive to maintain.
Mutation results give a principled pruning criterion: record, per assertion, which mutants'
kills it participated in. An assertion whose kill-set is empty senses nothing and is dead
weight; an assertion whose kill-set is a subset of another's is redundant. Greedy set-cover
over kill-sets yields a minimal assertion set with the same detection power. `tf-mut curate`
reports both classes with the evidence attached. No other approach to test curation has this
oracle available — it falls out of data the mutation run already produces.

**Regression semantics.** A characterisation suite pins today's behaviour *including today's
bugs* — Feathers is emphatic that this is the point, not a flaw: the suite detects *change*,
and the team decides which changes are fixes. The generated files carry a header comment
saying exactly that, and the report never calls pinned behaviour "correct".

## 5. Honest limits

A full failure-mode taxonomy — including which failures a driving coding agent can resolve and
how the tool is designed to be driven by one without ever making an LLM call itself — is in
[`agent-integration.md`](agent-integration.md). The headline limits:

- **It pins bugs.** Stated above; stated in the generated files; by design.
- **Plan-time failures without runtime dependencies severed elsewhere.** A module that shells
  out via `external` data sources, or provisioners, is not fully severable by `mock_provider`.
  The tool reports these as unseverable seams rather than failing opaquely.
- **Value synthesis is heuristic at the edges.** The diagnostic-repair table will not cover
  every function domain; the TODO-placeholder path is the honest fallback.
- **`configured`-level suites are brittle.** That is what the granularity ladder and `curate`
  are for; the default is the conservative end.
- **Modules that do not `init`/`validate` offline** cannot be characterised offline. Nothing
  to be done; fail fast with the diagnostic.

## 6. Prior art

The closest analogue is **DSpot** (Baudry et al.), which amplifies Java test suites by
generating assertions from observed runtime state and keeping those that improve the mutation
score — the same harvest-pin-measure loop. DSpot has to instrument the JVM and serialise
arbitrary object graphs to observe state; Terraform hands the equivalent over as a JSON
document. Test-generation tools like Randoop/EvoSuite generate *inputs* searching for
crashes; characterisation here generates *oracles* for inputs mined deterministically from
the module's own contract. The combination of a total, structured, addressable observation
surface with a native mocking seam is, as far as the research for this project found, unique
to the IaC setting — which is why the loop closes here and stays open elsewhere.

## 7. Roadmap fit

Characterisation is deliberately **not** a new milestone before M4 — it is a re-arrangement
of M2 and M4 machinery:

| Needs | Built in |
| --- | --- |
| Schema harvest, mock scaffolding data | M2 (`providers schema -json` integration) |
| Double-run volatile masking | M2 (baseline oracle) |
| Plan/state harvest via `-verbose -json` | M2 (fingerprint plumbing) |
| `hclwrite` assertion writer | M4 (suggested assertions) |
| Kill-set recording for `curate` | M4 (diagnosis engine) |

So: **M4.5 — `tf-mut characterise` (scaffold + pin + until-dry loop), `tf-mut curate`
(minimality report)**. The incremental cost over M4 is input synthesis (§3.2) and the
granularity ladder — the rest is sequencing existing parts.

One consequence worth making explicit in positioning: this inverts the tool's adoption story
for legacy codebases. The mutation-testing pitch assumes you have tests to grade; most
Terraform estates do not. `characterise` gives those estates their first credential-free unit
suite in minutes, *and that suite arrives with a mutation score attached*. Grading and
generation are the same machinery pointed in opposite directions, and the generation
direction is the one with no incumbent at all.
