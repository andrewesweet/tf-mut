# run

`tf-mut run` mutates a module's configuration, executes its suite against every mutant and
reports what the suite actually detects: the mutation score, the per-outcome counts and —
when mutants survive — the findings that name what no assertion observes.

## Sub-features

- `run-clean` scores a fully-asserted module at 100% and exits 0.
- `run-findings` reports surviving mutants with their diagnoses and exits 1.
- `run-gate` fails a `--min-score` gate on a module whose score is below it.

## How to get to it (user POV)

- Run `tf-mut run [flags] PATH` with PATH at a Terraform module root.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/all-killed` and `$VERIFY_ROOT/modules/suggest-basic` are fixture
  copies.

- **Clean module scores 100%.** Run `$BIN run --no-cache "$VERIFY_ROOT/modules/all-killed"`.
  Exit code `0`; stdout carries `Mutation score` `100.0%` with
  `(3 of 3 scored mutants detected)`, and the counts line
  `Killed 3   KilledByError 0   Survived 0   Unassertable 0   NoCoverage 0`.
- **Write the JSON proof.** Run
  `$BIN run --no-cache --output json=$EVIDENCE/run-clean.json "$VERIFY_ROOT/modules/all-killed"`.
  Exit code `0`; the artifact has `.metrics.mutation_score == 1`, `.metrics.scored == 3`,
  `.metrics.counts.Killed == 3`.
- **Untested constructs survive.** Run
  `$BIN run --no-cache "$VERIFY_ROOT/modules/suggest-basic"`. Exit code `1`; the summary
  shows `Mutation score` `50.0%` with `(5 of 10 scored mutants detected)`, `Survived 5`, a
  `SURVIVED (5)` section whose mutants carry `Diagnosis: no-assertion`, and a
  `PSEUDO-TESTED RESOURCES (1)` section naming `terraform_data.ignored`.
- **Gate on the score.** Run
  `$BIN run --no-cache --min-score 100 "$VERIFY_ROOT/modules/suggest-basic"`. Exit code `1`:
  the score gate fails the run even though the run itself completed.

## Gotchas

- `run` writes `.tf-mut-cache/` into the module it drives — drive copies only.
- An exit code of `1` is the feature working when the fixture has findings; only `2` or a
  stack trace is a harness failure.
- The verdict cache is a speed feature, not a correctness one. The recipes use `--no-cache`
  so a verdict never depends on what a previous run stored.
- Survivor and suggestion identifiers are content-derived; assert the quoted substrings
  above, never a hex ID.
