# M4 exit gate

The record of what closing milestone M4 (issue #58, revision 2) proved, measured and
deferred. Companion to `docs/reviews/2026-08-18-m4-implementation-review.md`, which carries
the decisions; this document carries the gates and the measurements.

## The gates, audited by name

`just gate-m4` runs the M4 offline gates: the JSON safety floor set, the M4c re-proof, the
three adapter matrices, the suggestion-soundness gate, the apply-protocol cases and the
skill contract — 57 named cases across `internal/engine`, `internal/suggest`,
`internal/skill` and `cmd/tf-mut`. Two tests keep the recipe honest exactly as the M2 and
M3 gates are kept honest: `TestTheM4GateNamesOnlyTestsThatExist` fails on any name that
resolves to nothing, and `TestTheM4GateCoversEveryNamedRequirement` maps each behaviour the
spec's exit gates require to the test that proves it, so the gate cannot go green by
naming nothing.

### Gate 1 — the M4.0 floor (the milestone entry gate)

All seven C1 cases hold, plus both #57 reproductions, in `jsonfloor_test.go`. The entry-gate
ordering held structurally: the floor is decided inside `checkSafety` before any Terraform
execution, and the floor tests run under the `DisableJSONReading` seam control so they keep
proving the unread half of the contract now that the M4c slice reads the content.

- JSON-only unmocked provider, JSON test mock status: `--allow-real-infrastructure` fails
  closed, naming the unread file and the flag.
- JSON-only provisioner: `--allow-unsandboxed-effects` fails closed **independently** —
  authorising one gate never lifts the other.
- JSON auto-var changing `count`: no refusal (the variables class informs neither gate) and
  no static pre-classification either — the floor's static half withdraws the shortcuts.
- Malformed, partially-decoded and well-formed-but-unmodelled JSON all retain the floor.
- Configured exclusions hide none of it; zero Terraform runs precede a refusal (proved with
  a recording wrapper binary — only `version` executes).

### Gate 2 — the suggestion-soundness gate (M4a + M4b exit)

Both legs, both seeds, and attribution, in `verify_test.go`:

- The seeded wrong-value assertion is refuted through the **baseline leg** (full suite, once
  per target-file batch, green required).
- The seeded vacuous assertion is refuted through the **isolated mutant leg** — beside real
  suggestions that kill its mutant, so only per-suggestion isolation can catch it. One
  measured correction to the seed itself: Terraform v1.15.8 refuses a constant `condition`
  ("must refer to at least one object"), so the vacuous seed is the tautology `x == x`
  rather than `true`.
- Verification evidence carries both legs with run references; verification runs are never
  cached, and a cache-served population still verifies afresh.

### Gate 3 — both #57 reproductions, floor and slice

Red-first under `DisableJSONReading` (the floor refuses/withdraws), then re-proved under the
slice in `jsonslice_test.go`: the JSON-declared provisioner trips the effects gate **from
the inventory**, and the mixed-module false proof is repaired — `local.json_only`'s cone
reaches the JSON-declared reader, and the mutant is Killed, not statically Unobservable.

### Gate 4 — the skill, installed and self-consistent

`skill install` places the mutation-loop skill at the documented per-agent paths; a user
edit survives a same-version and a cross-version reinstall unless `--force`; a cross-version
upgrade replaces only unmodified files and reports the outcome per file. The suite asserts
the installed skill references only commands and flags this binary's usage text documents
(`cmd/tf-mut/cli_test.go`), and that the content carries the agent-integration rule verbatim.

### Gate 5 — the measurements, under their pinned protocols

Below.

## Measurement 1 — cache over-invalidation (M5 disposition protocol)

Protocol, pinned in `internal/engine/cachemeasure_integration_test.go` (integration tag;
offline fixtures, so the gating is caution rather than need):

- **Fixture** `internal/engine/testdata/cache-measure`, digests recorded by the run below:
  `a.tf=ebba88846fae b.tf=367039c51c21 tests/unit.tftest.hcl=a8362b51f691`.
- **Population**: full, standard tier, no exclusions — 11 mutants per state.
- **Environment**: the suite's own hermetic Terraform environment (Terraform v1.15.8,
  `CHECKPOINT_DISABLE`, per-test plugin cache).
- **Simulated key**: per-mutant, over the mutant identifier and the content digest of the
  mutant's **own file only** — the obvious finer candidate.
- **Edit sequence**: E1 a comment-only edit to `a.tf`; E2 a value edit in `a.tf` no
  assertion pins; E3 removal of `b.tf`'s reader of `local.orphan` — the seeded
  verdict-changing dependency the per-file key cannot see.

Results (2026-08-18):

| Edit | Population | Coarse invalidated | Simulated invalidated | Simulated reused | False reuse |
| --- | --- | --- | --- | --- | --- |
| E1 comment-only | 11 | 11 | 8 | 3 | 0 |
| E2 value edit | 11 | 11 | 8 | 3 | 0 |
| E3 cross-file dependency | 11 | 11 | 3 | 8 | **3** |

The three E3 false reuses are the `local.orphan` mutants: `Survived/no-assertion` before the
edit, statically `Unobservable` after it, with `a.tf` byte-identical throughout. The
per-file key would have replayed a stale verdict for 3 of its 8 claimed reuses.

**Recommendation for M5, with the rejection rule applied**: the per-file simulated key is
**rejected** — a non-zero false-reuse count rejects the key regardless of hit rate, and this
one lies on the first cross-file dependency it meets. The coarse key over-invalidates
(8 of 11 verdicts discarded on a comment edit), but over-invalidation costs seconds and
false reuse costs the invariance law. Any M5 candidate key must be at least
graph-dependency-aware, and must rerun this protocol with its own algorithm pinned before
anything is built. **No finer key was built in M4, and none may be built under any result of
this measurement** (settled).

## Measurement 2 — the platform facts of the Action change

The PR comment step, its `comment`/`comment-outcome` surface, and the
`pull-requests: write` permission are removed everywhere they were documented (`action.yml`,
`ACTION.md`, the workflow test). The markdown summary was already written to the job step
summary by `scripts/action-run` on every run — green, red, and degraded alike — and the step
summary needs no token, so the fork-degradation matrix loses its only degrading writer on
pull-request events. The workflow test now asserts the summary's presence in
`$GITHUB_STEP_SUMMARY` for the green, red and degraded paths; the assertions run in CI, not
locally, per the existing pattern.

## Standing facts carried forward

- The ~1.6 s per-mutant floor, the gate truth table, verdict invariance, and the withdrawn
  `mock-masked` diagnosis: unchanged, untouched.
- The M3-era conservatisms revisited exactly as specified: the evaluator's
  fail-closed-on-JSON-auto-vars lifts only for files the slice actually read; a changed
  `.tf.json` still forces the full `--since` population; the cache key hashes all JSON
  classes (now including read configuration and test files, additively).
- Schema `report-2.2.0` published and validated; 2.1.0 and earlier remain published; the
  deprecated 2.1.0 vocabulary carries forward.
