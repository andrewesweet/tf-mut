# characterise

`tf-mut characterise` scaffolds a first test suite for a module that has none: it plans
scenarios, harvests what the module actually does and pins the observed outputs as
assertions. By default it writes nothing — the rendered suite goes to stdout.

## Sub-features

- `char-scaffold` renders scenario run blocks with distinct state keys.
- `char-branch` expands a variable-decided condition and pins the non-default side too.
- `char-dry` writes nothing without `--write`.

## How to get to it (user POV)

- Run `tf-mut characterise [flags] PATH` on a module without a test directory.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/untested-branches` is a fixture copy (a module with no `tests/`).

- **Scaffold the suite.** Run
  `$BIN characterise "$VERIFY_ROOT/modules/untested-branches"`. Exit code `0`; stdout
  carries the plan header `granularity  outputs` and `pinned` with a nonzero count, then
  rendered `==> tests/characterise_defaults.tftest.hcl <==` sections whose scenario blocks
  named `run "characterise_flip_...` carry a `state_key` line, a `variables` block setting
  `env = "prod"`, and assertions over `output.tier` and `output.units`.
- **Pin the flipped branch.** The `characterise_flip_` scenario asserts
  `output.tier == "critical"` — the `prod` side the module's default (`dev`) never takes.
- **Prove the dry default.** After the run,
  `test ! -e "$VERIFY_ROOT/modules/untested-branches/tests"` succeeds: the module copy has
  no test directory, because characterisation wrote nothing.
- **Persist the scaffold.** Run
  `$BIN characterise "$VERIFY_ROOT/modules/untested-branches" > $EVIDENCE/characterise.txt`.
  The artifact contains the same rendered sections.

## Gotchas

- The scenario-name suffix (`flip_env_<hex>`) is content-derived; assert the prefix and the
  structure, not the hash.
- The default pinning level is `outputs`; a module with no outputs escalates and says so in
  the report.
- `--write` places a verified suite into the module — never part of a verification drive.
