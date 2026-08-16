# Harness spike — measured results

Everything here was executed locally against `Terraform v1.15.8 on linux_amd64`, WSL2, Go
1.26.3. Fixtures live in `research/spikes/`. The purpose of the spike was to answer four
questions before committing to a design:

1. How do you isolate a mutant without touching the source tree?
2. Can `.terraform` be shared, and does sharing break mutations in child modules?
3. Can equivalent (unobservable) mutants be detected automatically?
4. How fast is this, actually?

## Fixtures

| Fixture | Shape | Purpose |
| --- | --- | --- |
| `fixture-a` | No provider at all (`terraform_data`), `count`, conditionals, `validation`, `expect_failures` | Baseline timing with zero provider cost |
| `fixture-b` | `hashicorp/null` behind `mock_provider`, plan **and** apply run blocks | The target use case: fully-mocked unit tests |
| `fixture-c` | Root module calling a local child module, plus a run block targeting the child directly | Child-module mutation propagation |

## Q1 — Sandbox isolation

Four strategies were tried against `fixture-b`:

| Strategy | Result | Wall time |
| --- | --- | --- |
| A. `cp -a` the whole directory including `.terraform` | pass | 0.152 s |
| B. Copy sources only, **symlink** a shared `.terraform` | pass | 0.142 s |
| C. Copy sources only, set `TF_DATA_DIR` to a shared `.terraform` | pass | 0.156 s |
| D. `terraform -chdir=<mutant dir> test` from anywhere | pass | 0.144 s |

All four work. **Strategy B is the design choice**: the mutant directory contains only the
module's own files, so the copy is small, and `.terraform` — which holds the provider binaries
and is by far the largest artefact — is shared rather than duplicated.

For context on why this matters:

```
terraform init  (cold, network)   5.179 s
terraform test  (warm)            0.167 s
```

Initialisation is ~31× the cost of a test run. A harness that re-inits per mutant is
approximately 31× slower than one that does not. Sharing `.terraform` is not an optimisation,
it is the whole design.

Notably, none of these strategies require git. The source tree is never written to, so there
is no restore step that can fail and no drift risk on crash — a structural improvement over
in-place mutation with `git checkout` rollback.

## Q2 — Shared `.terraform` and child modules

The correctness risk: if `.terraform/modules/modules.json` recorded **absolute** paths, a
shared `.terraform` would make every mutant silently execute the *original* child module, and
every child-module mutant would be falsely reported as survived. That failure would be silent
and would corrupt every result.

Verified explicitly. `modules.json` records relative directories, and the propagation test
confirms the behaviour:

```
control  (unmutated copy, shared .terraform)   Success! 2 passed, 0 failed
mutate modules/net/main.tf:
  cidrsubnet(var.cidr, 8, i) -> cidrsubnet(var.cidr, 4, i)
                                              Failure! 1 passed, 1 failed
original fixture still contains the unmutated text: yes
```

Child-module mutations propagate correctly, and the source tree is untouched.

*Scope correction (adversarial review M6, 2026-08-15):* this experiment covered a
**downward** source (`./modules/net`) only. Upward sources (`source = "../shared"` — the
standard monorepo layout) record `Dir` values that escape the sandbox root and fail with
"Module not installed". The design now roots sandboxes at the `..`-closure of local module
sources.

Caveat carried into the design: registry and remote modules are vendored *inside*
`.terraform/modules/`, so they are shared and cannot be mutated per-sandbox. They are third
party code and out of scope for mutation anyway, but the tool must exclude them explicitly
rather than by accident.

## Q3 — Automatic equivalence detection

The idea: run `terraform test -verbose -json`, extract the `test_plan` / `test_state` payload
for every run block, strip `provider_schemas` (large and invariant), and hash the result. If a
mutant's fingerprint equals the baseline's, the mutation cannot be observed by *any* assertion
over plan or state — so it is not a test-suite weakness.

Verified on `fixture-b`:

| Configuration | Fingerprint |
| --- | --- |
| Baseline | `997496b529dac9f6` |
| Baseline + an unused `local` (config changed, behaviour not) | `997496b529dac9f6` |
| `var.enable_backup && var.env == "prod"` → `\|\|` | `2a17189e2b4f60e0` |

The fingerprint is stable under an unobservable change and moves under a behavioural one.

This is a **sound but incomplete** oracle, and the design must state so honestly:

- **Sound direction:** identical fingerprints across all run blocks ⇒ no assertion over plan
  or state could distinguish the mutant. Reporting it as "survived" would be a false alarm.
- **Incompleteness:** the fingerprint only covers the variable assignments the existing run
  blocks happen to exercise. A mutant that is unobservable under those inputs may well be
  observable under others — that is a *coverage* finding, not an equivalence finding, and must
  be reported as such.

So the correct classification is not "equivalent" but **`unobservable-under-current-inputs`**,
which is simultaneously an equivalence signal and a coverage signal. That distinction is the
tool's most important piece of honesty.

*Correction (adversarial review C2, 2026-08-15):* this section originally claimed the
`test_plan` payload's `relevant_attributes` was "a coverage map… exactly what test selection
needs". Re-examination showed it is the refresh/targeting dependency set — identical across
all three `fixture-b` run blocks, containing only the cross-resource `id` reference and not
the `triggers.tier` attribute the assertions actually read. It cannot support test selection
or coverage reporting; the assertion inventory parsed from the test-file AST can.

### The fingerprint has to be normalised first

Fingerprinting only works if the baseline is stable. It is — but only where mock values are
pinned. Three consecutive runs of `fixture-b` with an explicit `mock_resource` default:

```
run1 997496b529dac9f6
run2 997496b529dac9f6
run3 997496b529dac9f6
```

Remove the default so Terraform auto-generates the computed `id`, and the same three runs give:

```
auto-generated id = n9npppvh
auto-generated id = edkiu3tk
auto-generated id = 48flsx2q
```

**Auto-generated mock values are not deterministic across runs.** Any computed attribute left
unmocked will change on every execution, so a naive fingerprint would differ from the baseline
every time and *every* mutant would be misclassified as behavioural. This is not a minor
detail — it would silently break the entire equivalence oracle.

The fix is general and cheap: **run the baseline twice and diff the two plan payloads.** Any
attribute whose value differs between two identical runs is volatile by definition, and is
masked out of every subsequent fingerprint. No knowledge of provider schemas or mock internals
is required.

The same volatile-attribute set is independently useful: it is exactly the set of attributes
the mock generated rather than the configuration determined. A mutant whose only plan
difference falls inside that set cannot be asserted on, which is precisely the `MockMasked`
diagnosis — obtained for free from a second baseline run.

## Q4 — Invalid versus errored mutants

Mutation testing convention discards mutants that do not compile, because a human would never
have written them. Terraform gives us two distinct signals, and they can be separated cleanly:

| Case | `terraform validate` | `test_summary` | Correct classification |
| --- | --- | --- | --- |
| Reference to a non-existent resource | exit **1** | `{"status":"error","errored":1,"skipped":3}` | **Invalid** — discard, not a kill |
| `cidrsubnet(var.cidr, 200, i)` (valid syntax, fails at plan) | exit **0** | `{"status":"error","errored":1,"skipped":1}` | **Killed** — a genuine dynamic fault the suite detected |

The rule that falls out:

> `terraform validate` fails ⇒ the mutant is statically invalid; discard it.
> `validate` passes but the run errors ⇒ the mutant is killed.

Both were verified. `validate` is cheap and runs without executing tests, so it is the correct
pre-filter for the entire mutant population.

One propagation hazard, also verified: when a run block errors, **every subsequent run block in
that file is skipped**. The kill verdict is still correct, but kill *attribution* — which run
block caught it — is unreliable after an error, and no conclusion may be drawn about the
skipped runs.

## Q5 — Throughput

100 mutant sandboxes of `fixture-b` (four run blocks each, three plan-mode plus one
apply-mode, all mocked), 8-way parallelism, shared read-only `.terraform` by symlink:

```
real    0m2.309s
user    0m13.834s
sys     0m5.663s
        34 exit 0   (survived)
        66 exit 1   (killed)
```

**~43 mutants/second on 8 workers**, or ~5.4 mutants/second/worker. The run is CPU-bound
(`user` ≈ 6× `real`), so it scales with cores.

*Scope correction (M1 exit-gate measurement, 2026-08-16):* the scaling claim holds only for
small-schema providers. Measured with the implemented engine, 40 mutants of a fully-mocked
`hashicorp/aws` module took 125.9 s at `--jobs 1` and 116.4 s at `--jobs 8` — a **1.08×**
speedup — while an equivalent provider-free population scaled 3.0× over the same range. The
marginal cost of a mutant against a large provider is starting the plugin process, not the
plan, and that does not parallelise on one machine. See `06-m1-exit-gate.md`.

An earlier 8-way parallel run of the same sandbox layout completed in 0.239 s with all eight
passing, confirming that concurrent readers of a shared `.terraform` do not contend or
corrupt.

### What this means

*Caveat (adversarial review C1, 2026-08-15):* the throughput above is real but
**provider-schema-specific**. `hashicorp/null`'s schema is 3.4 KB; `hashicorp/aws` 6.20.0 is
14.5 MB, and `-verbose` re-serialises the full schema into every per-run-block message. The
review measured a 10-resource fully-mocked AWS module at ~0.36 mutants/s plain and ~0.10
mutants/s with per-mutant `validate` + `-verbose` — roughly 100–400× below this fixture. The
per-mutant sequence in the product design is two-phase for exactly this reason, and no number
in this section should be extrapolated beyond small-schema providers. A real-provider
re-measurement is the M1 exit gate.

For fully-mocked plan-mode unit tests against small-schema providers, mutation testing is
effectively free — well inside a pre-commit hook. For real-provider modules it is not, and
the product design's stance (interactive inner loop via selection and two-phase execution;
scheduled full sweeps) reflects the corrected numbers.

The cost model is entirely different for apply-mode tests against real providers, where a
single run block can take minutes and cost money. The design must make that distinction loud
and must refuse to run against real infrastructure without an explicit opt-in.

## Summary of design constraints established

| Finding | Design consequence |
| --- | --- |
| `init` is 31× a test run | Init once; share `.terraform` read-only across sandboxes |
| Local module dirs in `modules.json` are relative | Symlinked `.terraform` is safe; child-module mutants propagate |
| Registry modules live inside `.terraform` | Must be explicitly excluded from mutation |
| `-filter` is file-scoped and exits 0 on no match | Test selection is per-file; always assert executed-test count > 0 |
| A run error skips later runs in the file | Kill attribution unreliable after an error |
| `validate` separates static from dynamic failure | Clean invalid-vs-killed-by-error rule (cheap only for small-schema providers — review M11) |
| Plan fingerprints are stable and cheap on this fixture | Automatic unobservability detection (verbose cost scales with provider schema — review C1) |
| Auto-generated mock values are non-deterministic | Volatile mask = static impure-function scan ∪ two-run diff (run-diff alone insufficient — review M5) |
| ~~`relevant_attributes` is a coverage map~~ | **Withdrawn** (review C2): it is the refresh dependency set; selection uses the assertion inventory |
| ~43 mutants/s on 8 cores, null provider | Interactive for small-schema providers; measured at 0.34 mutants/s on a mocked-AWS module with the implemented engine (`06-m1-exit-gate.md`) — two-phase execution and run-block selection are viability requirements |
| Parallelism scales 3.0× provider-free and 1.08× against `hashicorp/aws` | `--jobs` is a small-schema lever only; test selection is the lever that matters on real modules |
| Mocks work with `command = apply` | Apply-mode mocked runs widen the killable surface |
