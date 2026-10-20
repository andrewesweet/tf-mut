# tf-mut verification map

This directory is the maintained source for verifying the user-facing behavior of the
tf-mut CLI. Read this index before driving, then use the matching feature file as the
recipe.

## Baseline preconditions

- Launch ran: `.artifacts/bin/tf-mut` exists and `version` prints a non-empty version.
- Doctor is green: terraform is the pinned v1.15.8, the binary is fresh, and this is the
  checkout that was built.
- The scratch root `$VERIFY_ROOT` exists outside the checkout with an `evidence/`
  subdirectory; `$EVIDENCE` points at it; `$BIN` points at `.artifacts/bin/tf-mut`.
- The four fixture copies exist under `$VERIFY_ROOT/modules/`: `all-killed`,
  `suggest-basic`, `untested-branches`, `curate`.
- The run is offline: no provider mirror, no network, no credentials.

## Driving conventions

- Start every recipe from the baseline state unless its preconditions say otherwise.
- Treat every command as literal. Keep quoted names and flags unchanged.
- Drive only fixture copies. The engine writes `.tf-mut-cache/` into the module it drives;
  the checkout's `internal/engine/testdata/` is never a drive target.
- The safety flags `--allow-real-infrastructure` and `--allow-unsandboxed-effects` and the
  write flags `--write`, `--force`, `--apply`, `--all-verified`, `--write-baseline` appear
  in no verification drive.
- Exit codes are part of the behavior: `0` nothing to report, `1` findings worth failing
  on, `2` an untrustworthy result or a wrong invocation.

## Proof and skip reporting

- Capture the command and its observable result — terminal substrings and exit code — not
  a final impression. Report-shaped assertions go through
  `--output json=$EVIDENCE/<name>.json`.
- Record the feature ID with every artifact; artifacts land in `$EVIDENCE` and survive
  Cleanup.
- Report an unreachable step with the attempted command and the unmet precondition.
- Do not report a skipped entry point as verified through a different path.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the user-visible
behavior. It then uses exactly four H2 sections in this order.

1. `Sub-features` lists short IDs with one line for each behavior.
2. `How to get to it (user POV)` lists every user entry point.
3. `Driving it with tf-mut` starts with `Preconditions:` and uses labeled bullets that pair
   each user action with an exact command and observable result.
4. `Gotchas` lists traps that can waste or invalidate a verification run.

Keep implementation details out of the map. Name only user paths, stable handles, required
state, commands, and observable proof.

## Features

- [run](./run.md) scores a module's suite by mutating it and reporting what the suite detects.
- [preview](./preview.md) lists the mutants a run would generate without executing anything.
- [suggest](./suggest.md) generates, verifies and renders the assertion that kills a survivor.
- [characterise](./characterise.md) scaffolds a first test suite for a module without one.
- [todos](./todos.md) lists the judgement points characterisation refuses to guess at.
- [curate](./curate.md) reports redundant assertions from an authoritative population.
- [json-report](./json-report.md) is the machine-readable report contract shared by all reporters.
