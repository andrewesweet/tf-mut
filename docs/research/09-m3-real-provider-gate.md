# M3 real-provider gate — the inner loop, and both debts settled

The M3c gate (issue #50), under the pinned protocol from the M3 spec review's M5 disposition:
"the gate pins fixture commit, exact diff, expected selected IDs and a non-zero minimum,
cache-off state, tier and warm/cold protocol; asserts shared-verdict identity; publishes
hardware and method; the portable regression assertion is the selected count plus the
factor-versus-baseline, with the 60-second figure a published measurement on named hardware,
not a portable assertion."

## Method

- **Fixture**: `internal/engine/testdata/aws-mocked` — six `.tf` files, eleven resources
  against `hashicorp/aws` (pinned by `.terraform.lock.hcl` to v6.60.0), fully mocked, three
  plan-mode runs. The fixture is pinned by the repository commit this document ships in.
- **Diff**: exactly one appended comment line in `observability.tf`, so every mutant
  identifier in the touched file survives unchanged. The expected selection is enumerated in
  `innerloop_integration_test.go`: the four `aws_cloudwatch_log_group.application` sites and
  the twelve mutant identifiers, literally. The fixture itself is pinned by content digest
  (`f60655a523306a94`), asserted before the measurement runs, and the portable factor floor
  is 4x against the published 14.6x.
- **Protocol**: cache off (`--no-cache`), standard tier, eight jobs, warm plugin cache
  (`.artifacts/cache/terraform-plugins` persists between runs); the full run precedes the
  scoped run. Shared-verdict identity is asserted through the M3b invariance harness.
- **Hardware**: 13th Gen Intel Core i5-13420H, 12 logical cores, 12 GB RAM, WSL2 Linux
  (6.6.87), Terraform v1.15.8, local NVMe. Wall-clock figures are measurements on this
  machine, not portable assertions.

## Measurements

| Quantity | Value |
| --- | --- |
| Full population | 219 mutants in 539.2 s (0.41 mutants/s) |
| Pinned-diff selection | 12 mutants (four sites), **40.1 s** |
| Factor, full versus scoped | **13.5×** (portable floor: 4×) |
| M1 full-run baseline (same fixture family, Tier 0) | 40 mutants, 128.4 s, 0.31 mutants/s |
| `DYNAMIC-ZERO` end-to-end | `Killed` |

The numbers above are the re-run after the delivery review's graph repair —
provider-referenced variables now execute rather than classify statically — on the same
hardware with warmer OS caches than the first publication (757.6 s / 51.9 s / 14.6×), which
is why both runs improved while the factor barely moved. The full-run throughput confirms
the settled fact: the
~1.6 s per-mutant provider-startup floor is unreachable from inside the tool, and every
remaining lever reduces how many mutants run. The pinned one-file diff runs **inside 60
seconds on this hardware** — a published measurement, not a portable assertion; the portable
assertions are the enumerated four-site and twelve-identifier selection, the non-zero
minimum, the fixture content digest, and the 4× factor floor. **The product claim, narrowed per the M5 disposition**: the sub-minute figure holds
for a configuration-only diff; a code-plus-test PR changes a test-file class, forces the full
population, and is not sub-minute.

## Debt one: `mock-masked`, withdrawn

The M2 implementation review's open question 1, executed prove-or-withdraw. **The positive
case cannot fire, and the diagnosis is withdrawn**, with the losing documents corrected in
this change.

The refutation, measured against `hashicorp/aws` v6.60.0 in apply mode:

1. **Optional-and-computed attributes** — the only attributes a configuration mutation can
   move that the schema also marks computed — produce *stable, attributable* deltas.
   Deleting `aws_sqs_queue.work.kms_data_key_reuse_period_seconds = 300` yields a delta of
   `300 → 0`: the mock invents `0` deterministically for numbers. An assertion could pin the
   configured value, so blaming the mock would hide a real finding. The M2 implementation
   deliberately restricted its computed test to computed-*only* attributes
   (`Computed && !Optional && !Required`), which is why the diagnosis has never fired.
2. **Computed-only attributes** cannot be moved by any configuration mutation directly, and
   the mock's invented values are either deterministic and identical on both sides (numbers,
   booleans, empty collections) — no delta — or random strings, which the mutant volatility
   re-run masks and the M2-2 rule classifies `indeterminate-volatility`, never a stable
   confined delta.

Both horns are pinned by `TestTheMockMaskedRefutationHolds` on the network-gated
`aws-applied` fixture: the near-miss mutant survives with an *actionable closure diagnosis*,
attributed to the module. The `Schemas.Computed` machinery stays: it feeds the mutant
volatility re-run rule, which is unaffected.

## Debt two: `DYNAMIC-ZERO`, classified end-to-end

The `aws-mocked` fixture's `aws_dynamodb_table.state` now declares its attributes through a
`dynamic "attribute"` block — the aws provider's schema declares `attribute` as a nested
block type, which neither offline provider does. `DYNAMIC-ZERO` empties the `for_each`, the
attribute disappears from the plan, and the suite's length assertion kills it:
**`Killed`, end-to-end**, asserted by the gate. The matrix row leaves preview-only; the
offline `dynamic` fixture remains the operator's generation-site witness.

## The terminal display bound (M2 review open question 5)

Survivor delta sizes measured on the real survivor population (143 survivors, full standard
run after the graph repair): median **3**, 75th percentile 3, 90th percentile 18, maximum 20
— where 20 is the JSON evidence cap, which 6 of 143 survivors (4.2%) saturate; the large deltas are the
whole-resource mutants (`EXT-RESOURCE-DELETE`, `EXT-BODY-BLANK`), whose remaining changes no
reader scrolls a terminal for. **Decision, set from this data**: the terminal keeps three
changes per survivor — the measured median — and the JSON keeps twenty, which loses nothing
for 95.8% of real survivors. Both bounds now rest on a measurement rather than a guess; the
full distribution is in `.artifacts/performance/m3-inner-loop.json`.

## The M3e admission measurement (#53)

The generated function catalogue's admission evidence, measured on the network-gated
`aws-applied` fixture with `--generated-functions`:

| Quantity | Value |
| --- | --- |
| Candidate sites (family-function calls) | 3 |
| Generated mutants | 5 |
| Per-pair invalid rate | 0 of 5, every pair |
| Per-pair error rate | 0 of 5, every pair |
| Pairs | `title->upper` killed, `title->lower` killed, `startswith->strcontains` unobserved-class, `setunion->setintersection` survived, `setunion->setsubtract` survived |

Zero invalid and zero error mutants across every generated pair — the arity guard on
`setsubtract` and the family fault model doing exactly what C7 demanded of them. The
per-pair table and the extended per-operator error counts are in
`.artifacts/performance/m3e-admission.json`. **Admission to `standard` remains a separate,
evidence-carrying change**: this document is the evidence, and the decision is deliberately
not taken here.

## Reproduction

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 mise exec -- \
  go test -tags=integration \
  -run 'TestInnerLoopGate|TestTheMockMaskedRefutation|TestAdmissionMeasurement' \
  -timeout 90m ./internal/engine/
```

The measurements land in `.artifacts/performance/m3-inner-loop.json` and
`.artifacts/performance/m3e-admission.json`.
