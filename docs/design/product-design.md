# tf-mut — product design

> Mutation testing for `terraform test`, built for fully-mocked unit tests.

This document proposes the product capabilities. It rests on the research in
`docs/research/`, and in particular on the measurements in `04-harness-spike.md` — every
performance and feasibility claim below was verified locally against Terraform v1.15.8 before
being designed around.

---

## 1. The problem

Terraform test suites tend to assert that a plan succeeds, not that it is correct. Oasis's
benchmark across 23 public Terraform repositories put a number on it: a **24.9% mutation
score**, with **78% of surviving mutants never covered by any test at all**. Terraform has no
native coverage capability and no accepted proposal to ship one
([hashicorp/terraform#37605](https://github.com/hashicorp/terraform/issues/37605), open since
September 2025), so today a team has no way to answer "are my Terraform tests any good?"

A green `terraform test` run is currently compatible with a module whose every meaningful
attribute could be changed without any test noticing.

## 2. The thesis

Mutation testing answers that question directly, and for mocked tests it needs no cloud
credentials and has no cost per run. Its speed, however, depends dominantly on one variable
the adversarial review (see `../reviews/2026-08-15-adversarial-review.md`, C1) exposed:
**provider schema size**. The `-verbose` JSON that the fingerprint oracle consumes embeds the
full provider schema in every `test_plan`/`test_state` message — 3.4 KB for `hashicorp/null`,
**14.5 MB for `hashicorp/aws`** — and `terraform validate` itself costs ~1.7 s once a large
provider is involved. Measured honestly:

| Configuration | Throughput |
| --- | --- |
| Null-provider fixture, plain `-json`, 8 workers | ~43 mutants/s |
| Mocked-AWS module (10 resources, 10 runs), plain `-json` | ~0.36 mutants/s |
| Same, with per-mutant `validate` + `-verbose` (the naive sequence) | ~0.10 mutants/s |

The product stance follows from engineering around that, not from ignoring it:

**tf-mut is a fast, local, credential-free correctness tool for module authors.** For the
inner loop — `--since` runs over changed lines, smoke tier, run-block-selected execution —
it targets seconds-to-a-minute even on real-provider modules. Full `standard` sweeps of a
large module are minutes-to-hours and are scheduled work, and the tool says so up front
rather than pretending otherwise. Three design decisions make the inner loop achievable:
two-phase execution (§3 — Execute), run-block-level test selection (§3 — Schedule), and
selective validation (§3 — Execute).

### Scope

**In scope.** Unit tests where all provider behaviour is mocked (`mock_provider`,
`override_resource`, `override_data`, `override_module`), executed with `command = plan` or
`command = apply`. Root modules and local child modules.

**Out of scope by default, permitted with an explicit flag.** Tests that touch real providers.
Mutation testing multiplies test executions by hundreds; against real infrastructure that is
slow, expensive and potentially destructive. `tf-mut` **refuses to run** when it detects a
non-mocked provider unless `--allow-real-infrastructure` is passed, and prints an estimated
run count and duration before proceeding.

A second, distinct gate (R2-10, verified): a fully-mocked configuration can still execute
arbitrary local commands, because **provisioners run under mocked `apply`** — a `local-exec`
fired with no real provider present, so the real-infrastructure gate never triggers. Apply-mode
runs whose configuration contains provisioners, `external` or `http` data sources, or
`terraform_remote_state` are refused without a separate `--allow-unsandboxed-effects` opt-in.
These effects are unsevered by mocking; excluding their addresses from reports does not stop
Terraform executing them, and a mutation run would execute them hundreds of times.

**Never in scope.** Mutating vendored registry or remote modules, or anything under
`.terraform/`.

---

## 3. Architecture

```
  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌───────────┐   ┌──────────┐   ┌────────┐
  │ Discover │──▶│ Baseline │──▶│ Generate │──▶│ Schedule  │──▶│ Execute  │──▶│ Report │
  └──────────┘   └──────────┘   └──────────┘   └───────────┘   └──────────┘   └────────┘
     parse         warm cache      operators      select /        sandbox        classify
     hclsyntax     init once       hclwrite       prioritise      + run          + diagnose
```

**Implementation language: Go.** Non-negotiable, for one reason: `github.com/hashicorp/hcl/v2`
is the parser Terraform itself uses. Every third-party HCL parser diverges from Terraform's
grammar — Oasis documented hitting exactly this and responded by abandoning AST parsing for
regexes. Using HashiCorp's own library removes the problem by construction, and yields a single
static binary, which is what the Terraform ecosystem expects.

### Discover

Parse every `.tf` with `hclsyntax` (accurate AST plus source ranges) and open a parallel
`hclwrite` view for editing. Parse `.tftest.hcl` files to build the run-block inventory, the
mocked-provider inventory, and the assertion inventory. Excludes `.terraform/`, `.gitignore`d
paths and vendored modules.

### Baseline

1. `terraform init` **once**, in one shared directory. Measured at 5.2 s cold versus 0.167 s
   for a test run — initialisation is ~31× the cost of a run, so doing it once is the single
   most important performance decision in the design.
2. `terraform validate` — a module that does not validate cannot be mutation tested.
3. `terraform test -verbose -json` — **twice**. Establish that the suite is green, record
   per-run plan fingerprints and `resource_changes` (which resources each run block plans —
   the resource-coverage source), and time each run block for timeout calibration.

The second baseline run guards the fingerprint oracle against volatility, but the review
(M5) established that the run-diff alone is insufficient in three ways: it is empty in plan
mode (computed attributes stay unknown and never materialise), it wrongly captures impure
*configured* values (`uuid()`, `timestamp()`) as if they were mock artefacts, and two
back-to-back runs can land inside one clock second and mask nothing. The volatile set is
therefore the **union** of a static AST scan — impure functions (`timestamp`,
`plantimestamp`, `uuid`, `bcrypt` — **not** `uuidv5`, which is deterministic; R2-9) and
unmocked `random_*`/`time_*` providers — and the two-run diff. Masking is
**component-granular, not scalar-granular** (R2-9): `"${uuid()}-stable"` has a volatile
prefix and an assertable stable suffix, verified killable via `endswith`, so masking the
whole value would erase a real finding. Where the AST identifies the volatile subcomponent
of a template, the stable components stay in the fingerprint; where the value cannot be
soundly decomposed, the fingerprint is `indeterminate` — classified `Survived`, never
treated as identical. The `mock-masked` diagnosis is decoupled from this entirely: it
derives from the provider schema's `computed` flags and applies only to apply-mode runs,
because plan mode leaves computed attributes unknown and mock masking cannot occur there.

A red baseline aborts with the failing run blocks named. A test file that executes zero runs
also aborts — `-filter` exits 0 on no match, so this must be checked explicitly.

### Generate

Apply the operator catalogue (`mutation-operators.md`) to the AST. Each mutant is a
`(file, range, replacement tokens)` triple plus metadata, materialised lazily.

### Schedule

Order mutants to surface findings early and cut work:

- **Static reachability.** A mutant in a block no run block instantiates is `NoCoverage` — no
  execution needed.
- **Test selection.** A run block is selected iff it **instantiates the mutated block** —
  plan reachability, determined by the run's module targeting, variables, and the block's
  `count`/`for_each` conditions. R2 review finding R2-1 established why this, and not the
  assertion cone, must be the basis: the product's primary finding class is a resource that
  runs *plan* but that no assertion *reads* — its assertion cone is empty, so cone-based
  exclusion would run nothing, produce no fingerprint and no `no-assertion` diagnosis, and
  select zero runs from the assertion-less suites `characterise` scaffolds. The assertion
  inventory ∩ forward cone (see round one, C2, for why `relevant_attributes` cannot supply
  either side) still matters — as a **prioritisation and predicted-diagnosis signal, never an
  exclusion rule**. Sound instantiation-reachability selection lands in M2; the graph-based
  cone refinement in M3.
- **Run-block granularity via file splitting.** `-filter` is file-scoped, but the sandbox is
  ours: split each test file into synthesised one-run-per-file test files (verified working,
  round-one M7). Three constraints, all handled: run blocks containing a `module {}` block
  are keyed in `modules.json` as `test.<dir>.<file>.<run>`, so the sandbox synthesises its
  own `modules.json`; run blocks consuming `run.<name>` outputs or an explicit `state_key`
  join a dependency-closed prefix; and — R2-4, verified — runs against the same module share
  **implicit in-memory state even with no `state_key` and no `run` reference**, so the prefix
  must include every preceding state-producing (apply) run whose state identity the selected
  run shares. Honest consequence: apply→plan suites largely do not split, and the speedup
  from splitting accrues mainly to independent plan-mode suites — which is the design's
  target shape, but the claim is now scoped to it.
- **Prioritisation.** Extreme-tier mutants first (few, cheap, highest signal), then contract,
  then language, then lifecycle. With `--fail-fast`, an early finding stops the run.
- **Deduplication.** Mutants with identical `(file, range, replacement)` collapse.

**The in-process reference graph — roles split across milestones (revised by both reviews).**
An attribute-level reference graph built from the AST (`hclsyntax` exposes every reference
via `Expression.Variables()`) serves distinct roles landing at different times: (a) static
`Unobservable` pre-classification — a site with no path to any resource, output or `check`
needs no execution (M3); (b) forward-cone **prioritisation** — assertions reading the
mutation site's downstream cone are the likely killers, so run them first, and predict the
`no-assertion` diagnosis where the cone misses every assertion (M3; per R2-1 never an
exclusion rule); (c) doomed-mutant avoidance — the reference scan gating
`EXT-RESOURCE-DELETE` (a **bare** dependent reference makes `count = 0` statically invalid;
an *indexed* dependent validates but errors at evaluation — see the operator catalogue and
R2-5) needs only a local scan and ships with the operator in M1; (d) survivor explanations
as paths: "`local.tags` → `aws_instance.app.tags` → nothing asserts on it" (post-MVP).

`terraform graph` itself is deliberately **not** the data source: its nodes are
resource/local/output-granular where operators fire on sub-expressions, its DOT output is
human-oriented and outside compatibility promises, and since v1.7 the default output is
simplified unless a plan is supplied. It keeps two niche jobs: a cross-validation oracle in
tf-mut's own test suite (assert the in-process graph's edges are a subset of Terraform's real
evaluation graph, catching hidden edges such as `depends_on` and provider references), and a
human-facing cone rendering in `tf-mut explain`.

### Execute

Per mutant, in parallel across `min(NumCPU, --jobs)` workers:

1. Materialise a sandbox rooted at the **`..`-closure of local module sources** (the review's
   M6: a `source = "../shared"` — the standard monorepo layout — escapes a sandbox rooted at
   the module directory and fails with "Module not installed"; `fixture-c` only proved the
   downward case). Files are hardlinked or reflinked; for large closures an overlay mount is
   the planned optimisation. The sandbox gets its **own `.terraform` directory** containing a
   synthesised `modules.json` (needed for run-block file splitting) and `.terraform.lock.hcl`
   is always copied — omitting it classifies the entire population `Invalid` with zero tests
   executed (m14). Two sharing rules, both review-corrected: `providers/` is shared
   read-only (symlink, NTFS junction on Windows, `TF_DATA_DIR` as the portable fallback;
   verified: eight concurrent readers neither contend nor corrupt), and — R2-7 — **remote
   module payloads under `.terraform/modules/` are shared as well**, with the synthesised
   `modules.json` entries rewritten to sandbox-visible paths. Remote modules are excluded
   from mutation *generation*, but a root consuming a registry or git child cannot execute
   without their code present.
2. Write the mutated file — **always as a fresh inode, never through a hardlink**. R2-3
   demonstrated the trap: a hardlink shares the source's inode, so an in-place write
   (`os.WriteFile`) mutates the original checkout — exactly the drift this design claims is
   structurally impossible. Hardlinks are permitted only for files that will never be
   written; mutated files are produced by write-to-temp + atomic rename, and the writer
   asserts before writing that the target does not share an inode with the source tree.
3. **Lazy** `terraform validate` — round one (M11) showed validating every mutant is a net
   loss (1.7 s on a mocked-AWS module, more than the test run it precedes); R2-6 then showed
   the planned post-hoc alternative is impossible, because static and dynamic failures emit
   byte-identical diagnostics in the test stream. The synthesis: `validate` runs **only after
   a phase-one `error` result**, where it is the sole verified discriminator between
   `Invalid` (validate fails too) and `KilledByError` (validate passes). Mutants that pass
   or fail assertions — the majority — never pay for it.
4. **Phase one:** `terraform -chdir=<sandbox> test -json -filter=<selected files>` — plain
   output, no `-verbose`. Killed mutants stop here (after lazy validation if they errored).
5. **Phase two, non-killed mutants only:** re-run with `-verbose -json` to obtain the plan
   fingerprint for the fingerprint-dependent states (`Survived` and its diagnoses,
   `StructurallyUnassertable`, `Unobservable`) and suggestion generation. This two-phase split exists because `-verbose` embeds the full
   provider schema per run-block message (C1) — a 26× marginal cost with `hashicorp/aws` —
   and only the non-killed minority needs fingerprints.
6. Assert the executed-run count is non-zero (a `-filter` matching nothing exits 0 — this is
   a **per-mutant invariant**, not a baseline-only check), classify, delete the sandbox.

**The source tree is never written to.** This is a deliberate divergence from Oasis's
in-place-plus-`git checkout` model: it removes the git dependency, removes any possibility of
leaving mutated source behind on a crash, and is what makes parallelism possible at all.

### Report

Classify, diagnose, render.

---

## 3a. Leverage from the wider Terraform CLI

`terraform test` is not the only subcommand with something to contribute. Everything below was
verified against v1.15.8 in the spike fixtures; milestones refer to §12.

| Subcommand | Verified behaviour | Use | Milestone |
| --- | --- | --- | --- |
| `terraform validate -json` | Structured diagnostics with summary and exact source range (`{"valid":false,"error_count":2,...,"range":{"filename":"main.tf","start":{"line":28,...}}}`) | Classify *why* a mutant is invalid, not just that it is. Aggregating diagnostics by operator ID is a self-test: an operator producing systematic invalids is a bug in the operator, and this surfaces it from ordinary runs | M1 — strictly better than the exit code at no cost |
| `terraform version -json` | Machine-readable version | Gate version-dependent features (`state_key` v1.9+, mocking v1.7+); detect `tofu` | M1 |
| `terraform fmt` | Canonical formatting | Pre-flight check that source is fmt-clean, so every mutant diff is guaranteed one-line; not a validity gate (that is `validate`'s job) | M1 |
| `terraform providers schema -json` | Full provider schemas from the shared `.terraform`, per-attribute `required`/`optional`/`computed` flags and types — e.g. `null_resource`: `id` computed, `triggers` optional | **The highest-value item.** (1) `EXT-ATTR-DELETE` fires only on attributes the schema marks optional — required-attribute deletions are statically doomed and never generated. (2) Type-aware value substitution cuts the `Invalid` discard rate. (3) Computed-attribute knowledge derives the `mock-masked` diagnosis statically. (4) Domain packs can enumerate mutation targets (boolean security flags, CIDR-typed attributes) from the schema instead of hand-curated lists | M1 (moved from M2 per R2-12 — the Tier 0 deletion gates need it) |
| `terraform metadata functions -json` | 238 builtin function signatures: parameter names, types, variadics, return types | Drive `FN-SWAP` / `FN-ARG-REORDER` / `FN-DROP-DEFAULT` from data rather than a hand-written table: substitutions are generated only between arity- and type-compatible functions, and the catalogue tracks the installed Terraform version automatically | M3 |
| `terraform graph` | See §3 — Schedule | Cross-validation oracle for the in-process reference graph; `explain` visualisation | Post-MVP |
| `terraform console` | Evaluates expressions against the config (`var.env == "prod" ? 3 : 1` → `1`) | **Inspiration, not integration** — one subprocess per expression is the wrong cost model. The idea it points at: pure-expression mutants (locals arithmetic, conditionals over variables) could be micro-evaluated in-process with `go-cty` under sampled inputs, screening equivalent mutants without any Terraform run. Speculative; needs the cty stdlib to cover enough of Terraform's function set to be worth it | Unscheduled |
| `terraform providers mirror` | Vendors providers to a local directory | Hermetic CI runs; an ops note rather than a feature | — |

## 4. Mutant states

Extends Stryker's standard model with three Terraform-specific states. Report consumers get
the vocabulary they already know, plus the distinctions Terraform actually needs.

Per-run outcomes (`pass`/`fail`/`error`/`timeout` per executed run block) are recorded
orthogonally, and the mutant's single aggregate state is assigned by **explicit precedence,
top to bottom** — R2-8 showed the states are overlapping predicates without one (a mutant can
fail one run and error another; changing file order must not change the verdict):

| Precedence | State | Assigned when | Counts toward score? |
| --- | --- | --- | --- |
| 1 | `Invalid` | Lazy `validate` fails after an `error` result | **Excluded** |
| 2 | `Killed` | ≥ 1 executed run reports `fail` — an **assertion** caught it | Numerator |
| 3 | `KilledByError` | No `fail`, ≥ 1 run reports `error`, lazy `validate` passes — **Terraform** caught it | Numerator, always reported separately |
| 4 | `Timeout` | No kill; ≥ 1 run exceeded `max(factor × baseline, 30 s)` | **Denominator** — and any timeout marks the whole score *incomplete* |
| 5 | `Survived` | All runs pass; masked fingerprint differs from baseline — or is `indeterminate` (unknown values or undecomposable volatility in scope, R2-2/R2-9) | Denominator. Diagnoses include `mock-masked` (differs only in schema-`computed` attributes, apply mode) and `indeterminate-unknown-values` |
| 6 | `StructurallyUnassertable` | Fingerprint identical **and** the construct has no plan/state projection (`lifecycle`, `depends_on`, unexercised `validation`) | Denominator, with fix guidance |
| 7 | `Unobservable` | Fingerprint identical, the construct projects, **and no value in the mutant's evaluated paths is unknown** | **Excluded** |
| 8 | `NoCoverage` | No run block instantiates the mutated block (assigned statically, before execution) | Denominator (reported separately) |
| — | `Ignored` | Suppressed by config, comment or baseline | **Excluded** |

Three R2 corrections are embedded there. **`MockMasked` is a diagnosis, not a state** (R2-8):
as a state it overlapped `Survived` by definition. **`Timeout` sits in the denominator and
poisons score completeness** (R2-11): excluding it let machine load *raise* the score —
K=1, S=1 scores 50%, and if load turns the survivor into a timeout the score became 100%;
in-denominator, load can only lower it, and `--min-score` gates fail on an incomplete score
unless `--allow-incomplete-score` is set. **`Unobservable` requires no unknowns in scope**
(R2-2): the reviewer proved plan-JSON equality does not imply unassertability — cty retains
*refinements* of unknown values (the known `"stable-"` prefix of an unknown string) that the
plan serialisation discards, and a legal `startswith` assertion killed a fingerprint-identical
mutant. Fingerprint-identical mutants with unknowns in their evaluated paths are `Survived`
with the `indeterminate-unknown-values` diagnosis, and the suggestion engine never claims
impossibility for them.

The `Killed`/`KilledByError` split (review M8) exists because Terraform's plan-time evaluation
is strong enough that **a suite with zero assertions still kills mutants** — verified: an
assertion-less run block killed a `cidrsubnet(…, 200, …)` mutant outright. Both count in the
headline score, consistent with the field convention that a runtime crash is a detection, but
the split is always visible, and the characterisation loops (`--until-dry`, `curate`) count
assertion kills only — otherwise a freshly scaffolded, assertion-free suite would start life
with a flattering score.

The `StructurallyUnassertable`/`Unobservable` split (review C4) repairs a contradiction: the
previous design excluded all fingerprint-identical mutants, which silently erased Tier 3
validation mutants and all of Tier 4 from the score — the two groups the catalogue calls
highest-value — and made the `structurally-unassertable` diagnosis unreachable, since it was
defined as a *survivor* diagnosis while survivors required a differing fingerprint. The state
is assigned statically from the construct class, carries its fix ("add an `expect_failures`
run block", or accept), and sits in the denominator: an untested validation rule is a real
finding, not noise to exclude.

**`Invalid` versus `KilledByError`.** `terraform validate` cleanly separates static from
dynamic failure. A reference to a non-existent resource fails `validate` (exit 1) and is
discarded — a human would never have written it. A mutation like `cidrsubnet(cidr, 200, i)`
passes `validate` but errors at plan time; that is a genuine dynamic fault Terraform detected.
Both cases were verified.

**`Unobservable`.** Fingerprinting the `test_plan`/`test_state` JSON per run block (minus
`provider_schemas`, minus the volatile mask) is stable under an unobservable change and moves
under a behavioural one — verified: an added unused `local` produced an identical hash, an
`&&` → `||` swap did not.

This oracle's soundness claim was **narrowed twice** and must be stated precisely. It never
proves semantic equivalence — different variable values might expose the mutant. And per
R2-2 it does not even prove unassertability under current inputs when unknown values are in
scope: the assertion evaluator sees cty *refinements* of unknowns (a known string prefix, a
known collection bound) that the plan JSON discards, so a fingerprint-identical mutant can
still be killed by a legal assertion. What identical fingerprints *do* prove: no assertion
over the **serialised** plan/state, under current inputs, with **no unknowns in the mutant's
evaluated paths**, can tell the mutant apart. Only that case is excluded as `Unobservable`;
it reports as `unobservable-under-current-inputs`, simultaneously an equivalence signal and
a coverage signal, and the tool tells the user which reading applies:

```
UNOBSERVABLE  modules/net/main.tf:14  FOR-DROP-IF
  No run block produces a different plan under this mutation.
  Either the filter is genuinely redundant, or no test supplies input where it matters.
  Every run block passed var.azs = ["a","b"]. Try a case where the filter excludes something.
```

That is the difference between a tool that generates work and one that generates insight.

---

## 5. Metrics

Three numbers, always reported together, because any one alone is misleading. Let
`K = Killed`, `KE = KilledByError`, `S = Survived` (including its `mock-masked` and
`indeterminate` diagnoses), `NC = NoCoverage`, `SU = StructurallyUnassertable`,
`T = Timeout`. States are exclusive by the precedence table above, so these are true
partitions. The **scored set** is `K + KE + S + NC + SU + T`; `Invalid`, `Unobservable` and
`Ignored` are excluded and reported as counts.

| Metric | Definition | Answers |
| --- | --- | --- |
| **Mutation score** | (K + KE) ÷ scored set | Overall detection quality |
| **Assertion score** | K ÷ (K + S + SU + T) | Assertion strength specifically — excludes Terraform's own error-catching |
| **Reachability** | (K + KE + S + T) ÷ scored set | Coverage |

`T` sits in every denominator it can reach (R2-11): machine load can then only *lower* a
score, never raise it, and any `T > 0` marks the score **incomplete** — `--min-score` gates
fail on incomplete scores unless `--allow-incomplete-score` is passed. The `K` versus `KE`
share is always displayed alongside the mutation score: a score composed mostly of `KE`
means Terraform is doing the detecting, not the tests.

Given Oasis's finding that 78% of survivors were uncovered, the gap between the first two
numbers is usually the most actionable thing on the screen. A module at 25% / 80% / 31% has a
coverage problem; one at 25% / 27% / 93% has an assertion problem. These need entirely
different fixes and no single score distinguishes them.

Plus a headline count: **pseudo-tested resources** — resources whose every extreme-tier mutant
survived. Covered by a plan, asserted on by nothing.

---

## 6. Coverage as a first-class output

From the verbose baseline's `resource_changes` (which resources each run block plans) and the
assertion inventory (which addresses the assertions actually read — parsed from the
`.tftest.hcl` AST), `tf-mut` can emit the coverage report Terraform does not have.
**Not** from `relevant_attributes`: the review (C2) established that field is the
refresh/targeting dependency set — identical across run blocks and blind to assertion reads —
and cannot support this report. The cost is the verbose baseline itself: seconds and
potentially hundreds of MB of transient JSON with large-schema providers, not the previously
claimed "about a second", though still no mutants and no extra runs:

```
$ tf-mut coverage

  Resource coverage        18/23  (78%)   5 resources never planned by any test
  Attribute coverage      142/391 (36%)   249 configured attributes never asserted on
  Variable coverage        11/14  (79%)   3 variables only ever used at their default
  Output coverage           6/9   (67%)   3 outputs never read by an assertion
  Validation coverage       2/7   (29%)   5 validation rules never exercised by expect_failures

  Never planned by any test:
    aws_cloudwatch_log_group.audit        main.tf:88
    aws_kms_key.backup                    kms.tf:12
```

`tf-mut coverage` runs the baseline only — no mutants. Seconds on real modules, and for many
teams it is the entry point: a cheap, useful answer that earns the right to ask for the slower
one.

---

## 7. The differentiator — suggested assertions

For every survivor, `tf-mut` holds two plan JSONs: the baseline and the mutant's. The diff
between them is precisely the observable consequence the test suite failed to notice. That is
enough to *generate the assertion that would have killed it*.

```
$ tf-mut run

SURVIVED  main.tf:22  COND-SWAP
  - tier = var.env == "prod" ? "critical" : "standard"
  + tier = var.env == "prod" ? "standard" : "critical"

  Diagnosis: weak assertion. Run block "prod" plans this resource, but no
  assertion reads null_resource.app.triggers.tier.

  Observable difference in run "prod":
    null_resource.app.triggers.tier   "critical" → "standard"

  Add to tests/unit.tftest.hcl, run "prod":

    assert {
      condition     = null_resource.app.triggers.tier == "critical"
      error_message = "prod workloads must be tagged critical tier"
    }
```

`tf-mut suggest --apply` writes accepted suggestions into the test files with `hclwrite`,
preserving formatting and comments. A `--dry-run` prints the patch.

No other mutation testing tool in any ecosystem can do this reliably, because in a general
programming language the observable difference is an arbitrary program state. In Terraform it
is a structured plan document with stable addresses — the assertion practically writes itself.
This is the capability that makes `tf-mut` a *test-improvement* tool rather than a
*test-grading* tool, and it is where the product should concentrate its effort.

### Survivor diagnoses

Every survivor gets one of:

| Diagnosis | Meaning | Fix |
| --- | --- | --- |
| `no-coverage` | No run block plans this block | Add a run block |
| `no-assertion` | Planned, but no assertion reads the affected address | Add the suggested assertion |
| `weak-assertion` | An assertion reads the address but is too loose (e.g. `!= ""`) | Tighten it — suggestion provided |
| `mock-masked` | Apply-mode: the mock's generated value (schema-`computed`) overwrote the mutated one | Add a `mock_resource` default or an `override_resource` |
| `structurally-unassertable` | `depends_on`, `lifecycle`, unexercised `validation` — the construct has no plan/state projection (this is the `StructurallyUnassertable` state, §4) | Add an `expect_failures` run block where one applies; otherwise accept, or move to an integration test |
| `unobservable-under-current-inputs` | No plan difference under any current run block, for a construct that does project | Add a run block with different variables, or suppress |

`mock-masked` and `structurally-unassertable` are the two diagnoses that stop the tool crying
wolf. Without them a mocked suite is told to fix things that assertions cannot reach, and users
correctly conclude the tool does not understand Terraform.

---

## 8. Command surface

```
tf-mut run       [PATH]   Full mutation run
tf-mut preview   [PATH]   List mutants as unified diffs; execute nothing
tf-mut coverage  [PATH]   Baseline-only coverage report (~1s)
tf-mut suggest   [PATH]   Survivors plus generated assertions; --apply to write them
tf-mut explain   ID       Everything about one mutant: diff, plan delta, why it survived
tf-mut baseline  [PATH]   Write/update the accepted-survivors baseline
tf-mut init      [PATH]   Scaffold .tf-mut.hcl
```

Key flags for `run`:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--tier smoke\|standard\|deep` | `standard` | Operator breadth |
| `--pack security-aws,...` | none | Opt-in domain packs |
| `--operator ID` / `--exclude ID` | — | Fine selection |
| `--since REF` | — | Mutate only lines changed versus a git ref |
| `--incremental` | on | Reuse cached results for unchanged sites |
| `--jobs N` | `NumCPU` | Parallelism |
| `--timeout-factor F` | `10` | Multiple of baseline run time |
| `--min-score N` | — | Exit non-zero below this score |
| `--fail-on-new` | on with a baseline | Fail only on new survivors |
| `--engine terraform\|tofu` | `terraform` | OpenTofu support |
| `--allow-real-infrastructure` | off | Required if any provider is unmocked |
| `--reporter ...` | `terminal` | See below |
| `--seed N` | — | Deterministic sampling |
| `--sample N%` | — | Sample the mutant population for very large modules |

### Configuration — `.tf-mut.hcl`

Written in HCL, parsed with the same library, because a Terraform tool configured in YAML is
an unforced error.

```hcl
tf-mut {
  engine     = "terraform"
  test_dir   = "tests"
  min_score  = 70
}

operators {
  tier    = "standard"
  packs   = ["security-aws"]
  exclude = ["STR-CASE", "LC-IGNORE-ALL"]
}

exclude {
  paths     = ["examples/**", "generated/**"]
  resources = ["aws_cloudwatch_log_group.debug"]
}

reporter "sarif" { path = "tf-mut.sarif" }
reporter "html"  { path = "tf-mut-report/" }
```

Inline suppression, with a mandatory reason:

```hcl
# tf-mut:disable DEPENDS-DROP — ordering only, not observable in plan
depends_on = [aws_iam_role_policy.attach]
```

---

## 9. Reporters

| Reporter | Purpose |
| --- | --- |
| `terminal` | Default. Live progress, survivors with diffs and suggested assertions |
| `json` | Full machine-readable results; the integration substrate |
| `sarif` | GitHub/GitLab code scanning — annotates the exact mutated line in the PR diff |
| `junit` | CI test-report panes; mirrors `terraform test -junit-xml` |
| `html` | Browsable per-file report with mutants inline in the source |
| `markdown` | PR comment summary |
| `csv` | Analysis and research; matches Oasis's column set for comparability |
| `mutation-testing-elements` | The Stryker JSON schema, so existing dashboards and the Stryker HTML viewer work unchanged |

Supporting the Stryker schema is deliberate leverage: an entire ecosystem of report viewers
and dashboards already consumes it, and adopting it costs one serialiser.

---

## 10. CI integration

The design target is a PR gate that finishes in under a minute:

```yaml
- run: tf-mut run --since origin/main --reporter sarif --fail-on-new
- uses: github/codeql-action/upload-sarif@v3
  with: { sarif_file: tf-mut.sarif }
```

`--since` restricts mutation to lines the branch touched, `--fail-on-new` compares against the
committed baseline, and SARIF puts each survivor as an annotation on the exact line in the
diff, with the suggested assertion in the message. A developer sees "this line you just wrote
is not tested, here is the assertion that would test it" without leaving the PR.

Adoption on an existing codebase is via `tf-mut baseline`: accept today's survivors, fail only
on regressions. No flag day.

---

## 11. What makes this defensible

Against the one incumbent (Oasis) and against a hypothetical native HashiCorp feature:

| Capability | Basis |
| --- | --- |
| Real HCL AST via `hashicorp/hcl/v2` | Enables expression-level operators that regex matching structurally cannot reach |
| Language-level operator catalogue | Fires on every module and every provider, not a curated list of AWS resource types |
| Copy-on-write sandboxes, shared provider tree | No git dependency, no drift on crash, parallel by construction |
| Plan-fingerprint unobservability oracle | Removes the biggest usability tax in mutation testing; verified working |
| `validate`-based invalid/killed split | Correct classification instead of a heuristic; verified |
| Mock-aware diagnosis | The `mock-masked` state stops false findings against mocked suites — the target use case |
| Suggested assertions from plan diffs | Only possible because the observable state is a structured document. Turns grading into improvement |
| Coverage as a by-product | Fills a documented three-year gap with no extra execution cost |
| Stryker-compatible output | Instant ecosystem reuse |

The moat is not the mutation engine — anyone can write operators. Nor is it suggestion
generation alone: once the baseline↔mutant plan delta exists as structured data, converting it
into an `assert` block is mechanical, and a coding agent handed the delta could do it unaided
(review m16). The defensible layer is the **verified classification and diagnosis loop plus
the substrate that produces trustworthy deltas at all** — volatility masking, two-phase
execution, state discrimination, per-mutant invariants — of which suggestion generation is the
cheapest consumer. That layer is also exactly what makes the tool a reliable substrate for
agents (see `agent-integration.md`), which compounds rather than competes.

---

## 12. Roadmap

**M1 — Prove the loop, correctly.** Discover, baseline, sandbox execution (`..`-closure
rooting, per-sandbox `.terraform` with shared providers *and* remote-module payloads,
fresh-inode mutation writes), **`providers schema -json` integration** — moved here from M2
per R2-12, because the Tier 0 deletion operators are gated on schema optionality and M1's
headline is unbuildable without it — Tier 0 extreme operators with the multiplicity-aware
deletion gate, lazy-validate classification
(`Invalid`/`Killed`/`KilledByError`/`Survived`), terminal reporter. Answers "which resources
are pseudo-tested?", defined over **assertion kills only** (R2-5). **Exit gate: correctness
— the R1/R2 reproduction fixtures pass as the tool's own test suite** (hardlink isolation,
deletion-gate cases, split-semantics cases), plus the real-provider re-measurement published
in `docs/research/`.

**M2 — Honesty and the speed levers.** Tiers 1–3, plan fingerprinting with the
component-granular volatile mask, the full precedence-ordered state model
(`StructurallyUnassertable`, `indeterminate-unknown-values`, in-denominator `Timeout`),
survivor diagnosis, JSON and SARIF reporters, `.tf-mut.hcl`. Two-phase execution, run-block
file splitting with the **state-identity-aware prefix closure** (R2-4), and **sound
instantiation-reachability selection** (R2-1/R2-13: selection by "does this run instantiate
the mutated block", computable without the full reference graph — the only sound exclusion
rule). **Exit gate: the demonstrated real-provider inner loop** — a `--since`-scoped run on
a mocked-AWS module inside the stated time envelope; this gate moved here from M1 because
M1 lacks the speed levers that make it meaningful (R2-13).

**M3 — Refinement and CI.** The attribute-level reference graph for cone-based
*prioritisation* and predicted diagnoses (never exclusion — R2-1), static `Unobservable`
pre-classification, incremental cache, `--since`, baseline file, JUnit/HTML/Stryker
reporters, GitHub Action. Function-operator catalogue driven by `metadata functions -json`.

**Post-MVP (unscheduled).** The explanatory uses of the reference graph: path-based survivor
explanations, `terraform graph` as the cross-validation oracle for the in-process graph, cone
rendering in `explain`. The graph's *selection* role moved to M3; what remains deferred is
explanation quality, which changes no verdict.

**M4 — The differentiator.** Suggested assertions and `suggest --apply`. Deferred deliberately:
it depends on plan-diff analysis that M2 has to build anyway, and it is worth doing well rather
than early.

**M4.5 — Characterisation mode.** `tf-mut characterise` scaffolds a fully-mocked unit suite
for an untested legacy module — mock blocks from provider schemas, inputs mined from variable
validations, assertions pinned from harvested plan/state — then uses the mutation loop as the
completeness oracle and kill-set analysis as the minimality oracle (`tf-mut curate`). Entirely
deterministic; no LLM. Design: `characterisation.md`. Sequenced after M4 because it is M2 and
M4 machinery pointed in the opposite direction: generation instead of grading.

**End-of-MVP gate — agent skills.** Two shipped skills plus `tf-mut skill install`: the
mutation-loop skill lands with M4 (it needs `suggest` to be teachable), the characterisation
skill with M4.5. MVP is not complete until a coding agent can drive both loops end-to-end from
the shipped skills alone. Design: `agent-integration.md`. Post-MVP: empirical evaluation and
optimisation of the skills themselves on a public legacy-module corpus.

**M5 — Breadth.** Domain packs seeded from Checkov/tfsec rule catalogues, OpenTofu parity,
Tier 4 lifecycle operators, and a published benchmark over a public-repository corpus —
directly comparable to Oasis's 23-repository evaluation, which is the fair way to make the
comparison.

---

## 13. Open questions

1. ~~**`terraform test` has no run-block filter.**~~ **Resolved by the adversarial review
   (M7): run-block granularity is achievable today** by splitting test files one-run-per-file
   inside the sandbox, with a synthesised `modules.json` and dependency-closed prefixes for
   `run.<name>` consumers. An upstream `-filter=file::run` would still be cleaner and cheaper;
   worth the issue, no longer a blocker.
2. **`apply`-mode mocked tests are richer but slower.** They expose state and outputs that plan
   mode leaves unknown, widening the killable surface — and `MockMasked` only exists there.
   Whether to prefer them per run block, or let the suite decide, needs measuring on real
   modules.
3. **Per-mutant volatility.** The static-plus-runtime volatile mask (§3) handles baseline
   volatility, but a mutation can itself change *which* values are generated (e.g. altering
   how many mock instances exist), producing volatility the baseline mask never saw. The
   review's M5(c) — second-quantised `timestamp()` — shows the class is real. May require the
   mask to be recomputed from the mutant's own two-phase runs when fingerprints differ only
   in suspicious attributes.
4. **Sandbox cost at `..`-closure scale.** M6's fix roots the sandbox at the closure of
   upward module sources, which for a deep monorepo can approach the whole repo per mutant.
   Reflink (`cp --reflink=auto`) and overlayfs need measuring; this interacts with M9's
   revised (3–8× higher) mutant counts.
5. **`-cloud-run`.** Remote execution against HCP Terraform is incompatible with the sandbox
   model entirely. Out of scope; stated rather than left ambiguous.
6. **Format-version compatibility (review m15).** The oracle rests on
   `plan_format_version`/`state_format_version` payloads that carry the same "no compatibility
   promise" caveat the design used to reject `terraform graph`. Posture: pin the accepted
   version range, fail loudly outside it, and treat OpenTofu as a tested matrix — its `test`
   command already diverges — rather than a `--engine` flag that implies parity.
7. **Upstream asks worth filing.** `-filter=file::run`; a `-verbose` mode that omits
   `provider_schemas` from per-run messages (C1 makes this a 26× marginal-cost issue); native
   coverage remains [#37605](https://github.com/hashicorp/terraform/issues/37605).
