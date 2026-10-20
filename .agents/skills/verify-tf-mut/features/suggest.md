# suggest

`tf-mut suggest` generates the assertion that kills a survivor, verifies it against both
legs — the baseline and the mutant — and renders the patch it would write. With
`--dry-run` it verifies everything and writes nothing.

## Sub-features

- `suggest-render` renders the candidate patch for a surviving mutant.
- `suggest-verify` proves the rendered assertion kills the mutant and holds on the baseline.
- `suggest-diagnose` reports why each survivor survived (`no-assertion`).
- `suggest-refuse-dry` refuses a contradictory apply selection under `--dry-run` with exit 2
  and writes nothing.

## How to get to it (user POV)

- Run `tf-mut suggest --dry-run [flags] PATH`.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/suggest-basic` is a fixture copy with survivors (five, per `run`).

- **Render and verify.** Run `$BIN suggest --dry-run "$VERIFY_ROOT/modules/suggest-basic"`.
  Exit code `0`; stdout contains the rendered patch block
  `condition     = output.ignored == "nobody-checks-me"` with an
  `error_message = "tf-mut suggestion ... catches mutant ..."` line, and the summary
  reports `Mutation score` `50.0%` and `Survivor diagnoses:   no-assertion 5`.
- **Persist the proof.** Run
  `$BIN suggest --dry-run "$VERIFY_ROOT/modules/suggest-basic" > $EVIDENCE/suggest-basic.txt`.
  The artifact contains the same patch block.
- **Dry means dry.** Run
  `$BIN suggest --dry-run --all-verified "$VERIFY_ROOT/modules/suggest-basic"`; note the
  exit code is `2` — the contradictory selection is refused. Afterwards
  `cat "$VERIFY_ROOT/modules/suggest-basic/tests/unit.tftest.hcl"` still shows only the
  original `run "applied"` block: nothing was written.

## Gotchas

- Verification executes real Terraform legs; a few seconds per survivor is normal.
- Assert the `condition` line, never the suggestion or mutant hex IDs: they are
  content-derived and move when the fixture bytes move.
- `suggest` without `--dry-run` writes test files when `--apply` or `--all-verified` is
  given; a verification drive never passes them.
