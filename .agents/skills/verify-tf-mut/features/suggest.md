# suggest

`tf-mut suggest` generates the assertion that kills a survivor, verifies it against both
legs — the baseline and the mutant — and renders the patch it would write. Without
apply flags it writes no test files. `--dry-run` renders candidates without verifying them.

## Sub-features

- `suggest-render` renders the candidate patch for a surviving mutant.
- `suggest-verify` proves the rendered assertion kills the mutant and holds on the baseline.
- `suggest-diagnose` reports why each survivor survived (`no-assertion`).
- `suggest-refuse-dry` refuses a contradictory apply selection under `--dry-run` with exit 2
  and writes nothing.

## How to get to it (user POV)

- Run `tf-mut suggest [flags] PATH` to generate and verify without applying.
- Add `--dry-run` only to render candidates without verification.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/suggest-basic` is a fixture copy with survivors (five, per `run`).

- **Render and verify.** Run `mise exec -- "$BIN" suggest "$VERIFY_ROOT/modules/suggest-basic"`.
  Exit code `0`; stdout contains a `SUGGESTED ASSERTIONS` section with status `verified`
  (the assertion held on the baseline and killed the isolated mutant), the rendered patch
  `condition     = output.ignored == "nobody-checks-me"` with an
  `error_message = "tf-mut suggestion ... catches mutant ..."` line, and the summary
  reports `Mutation score` `50.0%` and `Survivor diagnoses:   no-assertion 5`. The warning
  `verification executed 1 full-suite run(s) plus 5 isolated mutant run(s)` proves both
  verification legs executed.
- **Persist the proof.** Run
  `mise exec -- "$BIN" suggest "$VERIFY_ROOT/modules/suggest-basic" > "$EVIDENCE/suggest-basic.txt" 2>&1`.
  Assert the same `SUGGESTED ASSERTIONS` section, `verified` status, condition line and
  verification-cost warning in the artifact. After each verifying command, run
  `diff -r "$FIXTURES/suggest-basic/tests" "$VERIFY_ROOT/modules/suggest-basic/tests"`;
  exit `0` proves the original test files are unchanged and no test files were added.
- **Refuse contradictory flags.** Run
  `mise exec -- "$BIN" suggest --dry-run --all-verified "$VERIFY_ROOT/modules/suggest-basic"`.
  The exit code is `2` — the contradictory selection is refused before verification or apply.
  Repeat the test-directory `diff` and require exit `0`: nothing was written.

## Gotchas

- Default verification executes real Terraform legs; a few seconds per survivor is
  normal. `--dry-run` skips these legs and cannot prove verification works.
- Assert the `condition` line, never the suggestion or mutant hex IDs: they are
  content-derived and move when the fixture bytes move.
- `suggest` without `--dry-run` writes test files when `--apply` or `--all-verified` is
  given. The only permitted apply-selection flag is `--all-verified` paired with
  `--dry-run` in the preflight refusal check; it must exit `2` and leave tests unchanged.
