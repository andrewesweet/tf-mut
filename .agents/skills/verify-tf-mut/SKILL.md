---
name: verify-tf-mut
description: End-to-end proof for the tf-mut CLI against real Terraform — build the binary, doctor the toolchain, drive the offline fixture modules through every subcommand, capture report evidence, clean up. Use when a change needs repeatable end-to-end verification, a ship brief asks for proof, or "verify tf-mut".
---

# Verify tf-mut end to end

tf-mut is a CLI. This skill drives the real binary the way a user does — build it, check the
toolchain, run every subcommand against the repository's offline fixture modules, and keep
the evidence — so a later change can cite a repeatable proof instead of an impression. It
writes nothing into the tf-mut source tree: every drive runs against a fixture copy in a
scratch directory outside the checkout.

Read `features/README.md` before driving, then use the matching feature file as the recipe.

## Launch

Launch is a build, not a server: tf-mut is a short-lived CLI, so there is nothing to keep
alive. Build the binary once from the checkout, then each drive is its own fresh invocation.

From a fresh checkout root, bootstrap the pinned tools (see the repository `AGENTS.md`):

    mise trust
    MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise install --yes
    MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just tools-install

A prepared worktree (one with `.tools/` already populated and the mise cache warm) only needs:

    mise exec -- just build

The binary lands at `.artifacts/bin/tf-mut`. Launch is ready when the build exits 0 and
`.artifacts/bin/tf-mut version` prints a non-empty version. The binary is disposable;
the next Launch recreates it.

Create the scratch root every drive writes into — outside the checkout, per run, never
shared between concurrent verifications:

    VERIFY_ROOT="${TMPDIR:-/tmp}/tf-mut-verify-$(date +%s)-$$"
    EVIDENCE="$VERIFY_ROOT/evidence"
    mkdir -p "$EVIDENCE"

## Doctor

One read-only pass that answers "is this checkout worth driving?". Run it first, and again
whenever anything looks off. All commands run from the checkout root.

- **Binary answers.** `.artifacts/bin/tf-mut version` prints a non-empty version (a git
  short SHA, or `dev` for an untagged build). A missing binary means Launch never ran.
- **Binary is fresh.** `find cmd internal -name '*.go' -newer .artifacts/bin/tf-mut` prints
  nothing. Any output means source moved after the build: rebuild before trusting a verdict.
- **Terraform is the pinned one.** `terraform version` prints `Terraform v1.15.8` — the
  version `mise.toml` pins. Fixture verdicts are calibrated to it; another minor can flip
  classifications. A missing terraform means `mise install` did not run.
- **This is the checkout you built.** `git rev-parse --show-toplevel` resolves, and
  `.artifacts/bin/tf-mut` sits inside it. Driving a binary from another checkout proves
  nothing about this one.

The safety flags `--allow-real-infrastructure` and `--allow-unsandboxed-effects` must appear
in no Drive command. This verification is offline by construction; if a drive seems to need
either flag, the drive is wrong.

## Drive

Copy each fixture out of the checkout first — the engine writes a `.tf-mut-cache/` verdict
cache into the module it drives, and the source tree of a module under test is never
written to:

    BIN="$PWD/.artifacts/bin/tf-mut"
    FIXTURES="$PWD/internal/engine/testdata"
    mkdir -p "$VERIFY_ROOT/modules"
    for fixture in all-killed suggest-basic untested-branches curate; do
      cp -r "$FIXTURES/$fixture" "$VERIFY_ROOT/modules/$fixture"
    done

These four fixtures are `terraform_data`-based, offline and millisecond-fast. Other fixtures
under `internal/engine/testdata/` are not: the `mocked-*`, `unmocked-*` and
`untested-aliases` modules need the provider mirror, and `aws-mocked` is network-gated.
Drive only the four.

Every feature file in `features/` pairs each user action with a literal command and the
observable result that proves it. Conventions:

- Run the commands as written from the scratch root, with `$BIN` and `$VERIFY_ROOT` set as
  above.
- Assert the exit code and the quoted output lines. tf-mut exits `0` with nothing to report,
  `1` when it found something worth failing on, `2` when it could not produce a trustworthy
  result or the invocation was wrong — an expected `1` is a pass in the recipes that expect
  findings.
- Never add `--allow-real-infrastructure`, `--allow-unsandboxed-effects`, `--write`,
  `--force`, `--apply`, `--all-verified` or `--write-baseline`.
- No network: if a command hangs or reaches for a provider, stop and diagnose. The fixtures
  cannot need one.

## Evidence

Evidence lands in `$EVIDENCE`, one artifact per feature ID, named `<feature-id>.<ext>`. The
recipes name the exact artifact each step writes. Proof standards:

- Assert the exact terminal substrings and exit codes the recipes quote, not an impression
  of success.
- For report-shaped proof, write the JSON reporter with
  `--output json=$EVIDENCE/<name>.json` and assert the named fields: `.schema_version` is a
  semver string, `.terraform_version` the pinned version, `.metrics.*` the counts the
  terminal summary showed.
- Keep the artifact and the assertion together: a proof a later reader cannot re-check is
  not proof. Cleanup never removes `$EVIDENCE`.
- Record the run's identity once per session:
  `$BIN version > $EVIDENCE/version.txt`.

## Cleanup

Nothing a drive starts outlives its drive. tf-mut runs are short-lived processes — there are
no servers to stop; if a drive hangs, kill the one process you started, never by name.

Remove the fixture copies and everything the runs wrote inside them:

    rm -rf "$VERIFY_ROOT/modules"

This must not remove `$EVIDENCE`. After cleanup, confirm the proof survived:

    ls "$EVIDENCE"

must still list every artifact the features captured — a cleanup that eats the proof fails
the run. When the verification session is over, the built binary may also go
(`rm -rf .artifacts/bin`); Launch recreates it. Never remove `.tools/`, the provider mirror,
or anything the checkout tracks in git: the worktree must stay clean of run residue.

## Helpers

This skill ships no scripts. The harness is the tf-mut binary itself: every drive step is a
literal command in a feature file. If a future maintenance pass needs a helper, it must be
executable, registered in the repository's `tools/shell-files` so the shell gates cover it,
and shown in the body here.

## Feature map

`features/README.md` indexes the mapped CLI surfaces with the baseline preconditions,
driving conventions and proof standards:

- [run](./features/run.md) — score a module's suite by mutating it; report survivors with exit 1.
- [preview](./features/preview.md) — list the mutants a run would generate, executing nothing.
- [suggest](./features/suggest.md) — generate and verify the assertion that kills a survivor.
- [characterise](./features/characterise.md) — scaffold a first suite for an untested module.
- [todos](./features/todos.md) — list the judgement points characterisation refuses to guess at.
- [curate](./features/curate.md) — report redundant assertions from an authoritative population.
- [json-report](./features/json-report.md) — the machine-readable report contract every reporter shares.

Deliberately unmapped: `skill install` writes into agent homes — state the run does not own
— so its proof belongs to the repository's own gates, not to a verification drive, and
`version` is Doctor's first check.
