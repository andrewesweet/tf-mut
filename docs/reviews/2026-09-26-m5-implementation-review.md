# M5 implementation review — 26 September 2026

What implementing issue #152 decided, what it measured, and where it landed differently from
the spec's words. The measurements are in `docs/research/16`–`21`; the decision record and
contract sweep are in `docs/research/22-m5-exit-gate.md`; this document is the
implementation-round record, in the shape `2026-08-18-m45-implementation-review.md` set.

## Decisions taken during implementation

**The witnesses are offline fixtures re-executed on every gate run, not a recorded
measurement.** M5-0.1 ran against real Terraform v1.15.8 once and published; the three
admitted pairs were then committed as an offline `terraform_data` fixture whose run-block
sequences stage each witnessed shape (`internal/engine/testdata/lifecycle`), and
`gate-m5` re-executes them through the seam. This is the spec's own requirement — "a Tier 4
site witnessed only by a skipped test is not witnessed" — taken one step further: the
*Kills when* column of each matrix row is derived from a pair the gate can still produce,
so a Terraform upgrade that silently breaks a witness turns the gate red rather than
leaving the matrix row citing a dead measurement.

**Tier 4 landed with no schema change, deliberately out of order with the spec's table.**
The M5a PR (#168) shipped the three operators with the `LC-*` identifiers outside the 2.3.0
report enum on purpose: the spec moved the bump to the schema ticket (#158, landed as #169),
so the enum and the first pack consumer arrived together. The cost was one intermediate
state where a `deep` report could name an operator the published schema did not enumerate;
the invariance gate normalises the stamp across that window, and no published artefact
carries it.

**Terraform's nondeterministic diagnostic order became tf-mut's canonical order.**
Measuring the M5a invariance legs exposed that one plan's diagnostics arrive in a
nondeterministic order between identical invocations — the same binary, twice, three
multi-diagnostic mutants differing in order only, sets identical. Rather than normalise the
comparison, the engine now publishes diagnostics in a canonical total order (test file, run,
severity, summary, detail, range) on the classify and cache-rehydration paths. This is the
milestone's one product behaviour change beyond the spec's list, and it is a change the
comparison demanded: without it the invariance proof could not hold under matched legs.

**Origins are a rewrite-record projection, and the red proof disables the aggregation.**
The pack mechanism (#170) keeps deduplication and mutant identity byte-for-byte as they
were and adds `origins` on the survivor, sorted and deduplicated by `(pack, entry)`,
aggregation independent of which operator owns the row. The gate's red proof is
`TestDisablingOriginAggregationTurnsTheOriginsCaseRed` — not a sort-order flip — because
the spec's own disposition says reversing ownership must lose no contributor, and a sort
flip is exactly the mutation a sort-agnostic aggregation survives.

**The shipped pack is parsed through the user-pack contract at init.** `security-aws`
(#178) is embedded and parsed at init by the same `ParsePack` that judges a user's file,
with `source_rule`/`source_licence` required; reserved names are derived from the embedded
files rather than a hard-coded list, so a pack cannot ship under a name the contract did
not admit. The shipped entry set is pinned by
`TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries` against the admission lists,
deliberately kept as the census's ten plus the widen-cidr admission's own list so the
eight-hour admission measurement never re-runs to add one entry (#183's decision 2).

**The whole-milestone invariance authorises the stamp movement the milestone actually
made.** The spec expected one authorised difference (`schema_version`, `origins` absent
with no pack); the milestone made two bumps, 2.3.0 → 2.4.0 (#169) → 2.5.0 (#183), so
`TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone` pins base
`d57f2cd` and asserts exactly the 2.3.0 → 2.5.0 stamp movement with content compared
everywhere else. The benchmark PR (#184) initially flagged the mismatch with its own base as
a High risk; the resolution widened the authorised difference to the two bumps the record
shows, which is the spec's own "exactly the schema-addition difference" clause read against
the milestone as shipped, not as tabled.

**The benchmark publishes its portable-assertion violations instead of smoothing them.**
The cold/warm legs carry portable assertions (availability, refusal determinism, verdict
identity) that *fail the test* when a live corpus trips them; the measured run tripped 244
(a large module's survivor-boundary moves between legs; a warm-leg provider-cache race), and
#184 published them as a table in the document with the test left red-by-design on
measurement recipes. A measurement recipe is not a CI gate; the alternative — weakening the
assertions until green — would have hidden exactly the instability the document exists to
report.

## What the reviews repaired

Each PR ran the no-mistakes pipeline before landing; the findings worth carrying:

- **The audit map must grow with the recipe.** #174's five leftover findings included
  census cases missing from `m5audit_test.go`'s required map — the recipe named tests the
  audit did not require, so deleting one would not have turned the audit red. The repair
  made the audit map the authority and proved it red by deleting a case.
- **State conservation by construction, not by measurement.** The census document first
  claimed its `inputs = defaults + mined + typed + todos` identity as measured evidence;
  the identity is true by construction and the measurement is the classification. The
  wording was corrected before it could mislead a reader into re-deriving the census from
  the sum.
- **A count-only assertion on a network-gated acceptance test** (#183's residual) — the
  widen-cidr witness test initially asserted the row count grew without asserting which
  rows; the population invariant was sharpened to "grows by exactly the rows the pack
  operators own", which is what caught that the widen bytes are produced by no language
  operator.
- **The gate's own honesty pair had to move twice.** Adding tests to `gate-m5` without
  adding them to `TestTheM5GateNamesOnlyTestsThatExist`'s minimum, or adding names without
  tests, each turns the gate red — which is the pair working, observed from the inside.

## Where the implementation is narrower than the spec

Recorded in full in the exit gate, §5–§6. Three places remain: the schema moved twice
rather than once, the second bump being the convention's price for a closed operator
enum; the witnesses used two of the five enumerated shapes and the matrix rows name only
those two; and the `.tf.json` mining stratum is published *unmeasured* because no pinned
module carries root JSON declarations — the spec's allowed outcome, but the census's JSON
denominator work is therefore evidenced by synthetic fixtures, not by the corpus.

## Flagged for the next review under standing rule 2

1. **The kill-witness admission predicate** — bounded admission policy on one executable
   pair under one enumerated shape. Unreviewed as design; the exit gate §7 names the two
   places it would break.
2. **The two-invocation benchmark protocol** — population and row outcome from separate
   invocations, never inferred. Honest, but the measured cross-run population drift (six
   modules resolved differently between the census run and the benchmark run) is either a
   protocol cost or a corpus fact, and nothing yet distinguishes them.
3. **The canonical diagnostic order** — the one behaviour change; it reorders reported
   diagnostics consumers may have parsed. Closed list of one, recorded so the next
   behaviour change is compared against it.
