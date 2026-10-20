# curate

`tf-mut curate` reports redundancy in a suite's assertions: it draws an authoritative
population, executes it once and attributes each killed mutant to the assertions whose
death it depended on, so duplicate or vacuous assertions can be pruned on evidence.

## Sub-features

- `curate-subsumed` names an assertion whose kill set is contained in another's.
- `curate-empty` names an assertion no mutant's death depended on.
- `curate-cross` names two scenarios whose assertions catch exactly the same mutants.
- `curate-exit` exits 1 when the report has findings.

## How to get to it (user POV)

- Run `tf-mut curate [flags] PATH`.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/curate` is a fixture copy.

- **Run the report.** Run `$BIN curate --no-cache "$VERIFY_ROOT/modules/curate"`. Exit code
  `1` — redundancy is a finding; the stdout carries the plan header `granularity  outputs`
  and three finding rows whose statuses are `subsumed`, `empty-kill-set` and
  `cross-scenario-redundant`, each with a `cur-` identifier and a one-line reason.
- **Persist the report.** Run
  `$BIN curate --no-cache "$VERIFY_ROOT/modules/curate" > $EVIDENCE/curate.txt 2>&1`. The
  artifact contains the same three statuses.

## Gotchas

- The verdict cache keys on the environment: an ambient `TRACEPARENT` or a changed OTEL
  variable turns into a one-time `Cache miss:` line. Harmless — the report is unchanged —
  but `--no-cache` is the deterministic choice for verification.
- `curate` refuses an effective configuration that narrows the population (tier, operator
  filters, exclusions) at configuration time: a finding drawn from a partial population
  would be a false one. A refusal exit with that reason is the feature working.
- Assertion and finding identifiers are content-derived; assert the statuses, never the
  hex IDs.
