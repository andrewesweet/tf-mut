# 23 — L2: the single verbose invocation

What building lever L2 measured, decided and deferred. L2 makes phase one run
`terraform test -verbose -json` and judges phase-one survivors' fingerprint evidence from that
same stream; the separate survivor-side phase-two invocation is gone. Trial evidence in
`/home/andre/tools/firstmate/data/tfmut-perf-l2-trial/report.md` (task `tfmut-perf-l2-trial`,
2026-10-19, master `15b79f0` + L1 #267 + L3 #268) gated the build; this note records what the
implementation added. Evidence grades follow the trial's: **Direct** = measured or read here,
**Supported** = inference from agreeing direct observations, **Inferred** = reasoned.

## 1. The measurement that gated the build

The L2 trial ran three real-provider suites, one paired same-seed run per suite, stock against
the prototype (all figures Direct, from the trial report):

| Suite | Σ mutant wall-clock | Whole-run wall | Total process CPU |
| --- | --- | --- | --- |
| openplex | **−39.6%** (6,102 → 3,685 s) | −38.6% | −40.1% |
| utl-regions | **−32.5%** (2,468 → 1,665 s) | −30.0% | −32.2% |
| physical-ai | **−22.5%** (7,204 → 5,581 s) | −22.0% | −24.1% |

No mutant changed state or diagnosis across 569 sampled real-suite mutants (567 byte-identical
verdicts; the two residual differences are mock-generated random strings in delta evidence,
which stock reproduces across runs of itself) and 350 fixture mutants. Peak child RSS rose
1–6%; tf-mut's own high-water mark rose at most 10%. Decoded output rose 15–61% — the verbose
stream embeds the provider schema per run-block message (C1) — and tf-mut pays for the extra
decode at about 12–13 CPU-s per extra GB, an order of magnitude below Terraform's CPU saving.

The trial also corrected the cost model that motivated the lever: it charged every
non-survivor (k−1)×T₁, but KilledByError and Invalid mutants pay 0–8% of that because
Terraform stops early before most of the verbose output (Supported). The saving is therefore
larger than estimated, not smaller.

## 2. What shipped

- **Engine**: `execute.go` and `oracle.go` implement the
  [execution schedule](../design/product-design.md#execute). The `phaseTwo` constant was
  removed; `phaseOne` remains as the marker for the run rows this engine publishes.
- **Tests**: the §7 trial-report tests are rewritten against the single-phase contract —
  `TestEveryMutantIsJudgedFromOneVerboseInvocation` (exactly one `test` child per mutant, no
  phase-two rows, ≥1 fingerprint-dependent survivor),
  `TestAFilteredShortStreamIsFingerprintedFromItsOwnRun`, and
  `TestTheExecutionThatOutlivesTheBudgetLandsInTheDenominator` — each red on
  stock with the right failure first. The M5 invariance strips normalise the run-row schedule
  (dedupe on file/run/status, drop `elapsed_ms`/`phase`) and each diagnostic's `severity`
  (§3), each with a dated reason.
- **Telemetry**: `telemetry_test.go` counts children from the `test` and `volatility-rerun`
  phases only, because the fingerprint phase starts none.
- **Cache**: `cacheFormatVersion` bumped to `tf-mut-cache-9` — a stored verdict records the
  two-phase schedule, so pre-L2 entries replay as misses rather than as stale schedules.

## 3. The one behavioural publish: check-block severity (stop-and-report, resolved)

The whole-milestone invariance test failed on exactly one field:
`$.mutants[219].diagnostics[0].severity` was `error` at base and `warning` at head — mutant
`e886907ba92c`, `CHECK-NEGATE` over the check block `健康` in the operators fixture, killed by
its failing check-block assertion in both engines. Verified against real Terraform v1.15.8: a
failing check-block assertion is graded `error` in a plain `-json` stream and `warning` under
`-verbose` (Direct). The engine lifts diagnostics from the stream the mutant's invocation
produced, so the single-verbose-phase engine faithfully publishes Terraform's own verbose
grade. Blast radius on the matrix fixture: 224 mutants, identical states, diagnoses, counts and
scores — exactly this one field.

Disposition: this is upstream stream behaviour the engine now publishes, not an engine defect.
The 2.17.0 schema documents it in `$defs.mutant.properties.diagnostics.description`; the M5
strips drop `severity` on both legs with the dated reason (summary, detail and location still
fail the comparison); this note records the reproduction. The SARIF level maps from state and
diagnosis, not severity, and is unaffected.

To reproduce the upstream severity difference from the checkout root, stage a copy of the
offline operators fixture and negate only its check condition:

```bash
mkdir -p .artifacts
repro_dir=$(mktemp -d "$PWD/.artifacts/l2-check.XXXXXX")
cp -R internal/engine/testdata/operators/. "$repro_dir/"
sed -i 's/condition     = var.replicas > 0/condition     = !(var.replicas > 0)/' "$repro_dir/main.tf"
mise exec -- just --command terraform -chdir="$repro_dir" init -backend=false
mise exec -- just --command terraform -chdir="$repro_dir" test -json \
  > "$repro_dir/plain.jsonl" || test "$?" -eq 1
mise exec -- just --command terraform -chdir="$repro_dir" test -verbose -json \
  > "$repro_dir/verbose.jsonl" || test "$?" -eq 1
mise exec -- just --command jq -r \
  'select(.type == "diagnostic") | .diagnostic | select(.detail | contains("replicas must be positive")) | .severity' \
  "$repro_dir/plain.jsonl" "$repro_dir/verbose.jsonl"
```

Both test commands exit `1`; the final command prints `error`, then `warning`. The fixture
uses only built-in `terraform_data` resources, and the source fixture remains unchanged.

## 4. The schema decision

**Minor bump 2.17.0, one new file per version.** The field layout and enumerations remain;
changed execution semantics require a versioned consumer contract under the repository's
schema rule. The [published schema](../schema/report-2.18.0.json) owns that contract for
`runs[].phase`, per-mutant `telemetry.phases`, and diagnostic severity. In particular, consumers
that attributed a Terraform child invocation to every fingerprint timing must read the
updated phase description. Run-level `telemetry.phases` retains its meaning.

## 5. Deferred

- **The filtered-run refusal is latent.** A `-filter` selected run whose stream carries no
  payload for a phase-one-passing run block now surfaces at the fingerprint stage as an
  execution error ("the fingerprint run produced no payload for …, which phase one reported as
  passing") instead of reaching a phase-two invocation that would have reported the same gap.
  `TestAFilteredShortStreamIsFingerprintedFromItsOwnRun` pins the contract. Raising it to a
  first-class refusal is follow-up work; the failure is fail-closed today.
- **L4 (per-file verbose payload merging) interacts here**: with verbose output on every
  invocation, L4's savings grow; the L2 trial's §7 recommends settling L4's design before
  further widening the verbose surface.
- **`phase` as a published enum** stays for old-report readability; a future major revision
  may retire the value `2` from new documents.
