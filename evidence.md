# Verdict-cache review evidence

The earlier reduced-fabn and impure-value probes were stable, as recorded in the
review context. This phase adds the reviewer's warm-directory probe. It establishes
a baseline-component failure on a reduced module; it does not establish the cause
of the original large fabn run. Environment keying is unchanged.

## Warm-directory reproduction

Using locked Go 1.26.9 and Terraform 1.15.8, run the same engine request twice
against this offline module, with unchanged files, binary, environment and settings:

```hcl
resource "terraform_data" "app" {
  input = "kept"
}

output "asserted" {
  value = terraform_data.app.input
}

output "value" {
  value = abspath(path.root)
}
```

The test has one run block asserting `output.asserted == "kept"`. The probe runs
both `command = plan` and `command = apply`. Before the fix, each invocation
executes six fresh verdicts and replays none. Comparing all logged components
within each command identifies **baseline** as the only changing component:

| Command | First baseline component digest | Second baseline component digest |
| --- | --- | --- |
| plan | `6f84838bf5ca41a24199b8da211d0c08f6188b547a2f3df263a50942a17f7b6a` | `ed8c54415f06963df530d6e941eb1b98e3493df0538adb71986a977e1d13cfc8` |
| apply | `3ac0ed2f91555ef9fe74e812024ec114cfb1b47d89c8bd6fc56572b3ad41bffe` | `d57b5a95f42bd35ed714ecb66c2b23447c469cb60f37229a779a35e77756ce20` |

Both baseline legs share the same warm directory within one invocation. The
two-run volatility mask therefore retains its absolute path, although the next
invocation materialises a different directory. The masked fingerprint changes.

The cache now replaces the warm closure path with a stable marker after masking,
including canonical scalar values and map-key paths. This replacement applies
only to cache identity. The report and oracle retain their original payloads.
The initial normalization advanced the cache format to version 7. The escaped
address correction below advances it to version 8.
The physical-directory correction uses version 9.
The stable-parent correction uses version 10.

## Regression contracts

`TestWarmDirectoryPathsReuseVerdictsAcrossInvocations` drives real Terraform
through the engine seam. Its independent oracle is the unchanged-suite reuse
contract: the second invocation must replay every verdict executed by the first,
even though the reported baseline fingerprint retains the changed path. It covers
plan and state, scalar and embedded values, map keys, an escaped work-directory
name, and a sibling module above the module directory. Both root and sibling
modules also have resources indexed by their absolute paths, exercising escaped
instance paths and the additionally quoted address scalars. Hashing the
unnormalised masked output fails this contract.

`TestCacheDebugLogsAreReachableThroughTheCLI` starts subprocesses through `main`
for both `run` and `suggest`. Its oracle is the public opt-in contract: unset
`TF_MUT_DEBUG` emits no debug records; `TF_MUT_DEBUG=1` emits every required
component digest on stderr, with no environment value, while stdout remains a
decodable report. A handler enabled only by an engine test fails this contract.

## Focused verification

One focused Go test invocation passed for `internal/engine` and `cmd/tf-mut`,
using the locked tools through `mise exec -- just --command go test`. It selected
the two regression tests above, the existing component-log and volatile-apply
tests, unchanged-run reuse, key-dimension invalidation, the cache-format upgrade,
unsafe-opt-in and no-cache refusals, environment miss reporting, and the generated
reference contracts. All four warm-path fixture/command combinations passed.
Both CLI commands passed with debug logging unset and enabled. The helper-only
test skipped in the parent and executed in its subprocesses.

Full test and lint suites remain the outer executor's phases. The pre-fix probe
and focused verification logs are retained locally under
`.artifacts/cache-review/`.

## Escaped instance-address reproduction

The next review identified an uncovered representation. With `WorkDir` containing
a quote, index the probe's resource by `for_each = toset([abspath(path.root)])`
and read its input through `one(values(terraform_data.app)).input`. Before the
escaped-address correction, both invocations execute nine fresh verdicts and
replay none, in plan and apply. The baseline component digests change:

| Command | First baseline component digest | Second baseline component digest |
| --- | --- | --- |
| plan | `cce5bedb3847f9bd0a789483f3f178d3706cab33c270b7294a630fd800dc462c` | `9028cc912d7407bfbc7d056e881fcf78b5a437f3a9711c058ac3216a2c154322` |
| apply | `0be2db1c9a20815f774033aaec488c6712a7040109dace426543669310284b7d` | `4ec8e355e01680b64179a375cb05b06d89889b284ef34e3f3e71f320d12b991e` |

Terraform escapes the instance key inside each resource address. Canonical
payload paths retain that escaped address; canonical address scalars quote it
again. The cache boundary now normalizes all three forms in both payload paths
and scalar values, matching the most escaped form first. Oracle payloads, report
fingerprints and environment keying retain their previous behavior.

The repeated CLI-debug finding has no residual defect: `main` still installs the
debug-enabled stderr handler before command dispatch when `TF_MUT_DEBUG=1`.
The subprocess regression exercises that entry point for both run and suggest.

The focused rerun passed all four extended fixture/command cases, unchanged-run
reuse, volatile-apply reuse, key-dimension invalidation, the version-7-to-8 cache
upgrade, component logging, and the run/suggest CLI debug tests. Its log is
`.artifacts/cache-review/escaped-verification.log`; the pre-correction probe is
`.artifacts/cache-review/escaped-before.log`. Full test and lint suites were not
run in this review phase.

## Symlinked temporary-parent reproduction

With the same indexed-path probe, replace `WorkDir` with a symlink to the
quoted-name directory. Before the physical-directory correction, both invocations
execute nine fresh verdicts and replay none, in plan and apply:

| Command | First baseline component digest | Second baseline component digest |
| --- | --- | --- |
| plan | `84fcf52624469c1535cf122d18f8cb0a9e8252cd9a1b8646e83ac8039721c405` | `42803924b2e103a15cd24c31058c9e51912a9db2cf17113801fd7a813f1ebde9` |
| apply | `41517d4bb6d36730cb1458abd346db8a35bf05fc4cb029ab479b61591aa1441f` | `27c5af9cc01b689b09b0b9eda3e6ee1764b9f068ae376f13ebc11ead0a6d1732` |

Terraform reports the physical working directory, while the cache normalizer
previously used its symlink spelling. The cache boundary now makes the warm root
absolute and resolves its symlinks before deriving any escaped forms. Resolution
errors disable cache use through the existing cache-key error path. This same
boundary handles explicit `WorkDir` and the default temporary directory selected
through `TMPDIR`. Environment keying remains unchanged.

The regression matrix retains its direct-parent cases and adds symlinked parents
for both fixtures and commands. The physical target retains the quote in its
name, so these cases also retain the escaped instance-address coverage.

The focused rerun passed all eight direct/symlink fixture/command combinations,
unchanged-run and volatile-apply reuse, key-dimension invalidation, the
version-8-to-9 cache upgrade, component logging, and both CLI debug cases.
The logs are `.artifacts/cache-review/symlink-before.log` and
`.artifacts/cache-review/symlink-verification.log`. Full test and lint suites
remain the outer executor's phases.

## Stable-parent false-reuse reproduction

Use outputs `asserted = "kept"` and `path = abspath(path.root)`, with the assertion
`output.asserted == "kept" || strcontains(output.path, "/special/")`. Select
`STR-CASE`, so the only mutant changes `kept` to `KEPT`. Start with a `WorkDir`
symlink targeting a directory named `special`, then retarget it to `ordinary`
without changing the request, sources, tests or environment.

Before the correction, both plan and apply show the same false reuse:

| Invocation | State | Cached | Fresh |
| --- | --- | --- | --- |
| special | Survived | 0 | 1 |
| ordinary, cache enabled | Survived | 1 | 0 |
| ordinary, cache disabled | Killed | 0 | 1 |

The logged baseline component is identical across the first two invocations:
`343c66e3df19f51d1f29375cd548206d4498520c26bdda33f22492e3dc2e841e` for plan and
`6efe3dae3001e1ad4b6085f4ab409b709012003d7c1bd57f75e469b812711349` for apply.
The probe log is `.artifacts/cache-review/parent-before.log`.

The shared baseline key now hashes the resolved stable parent alongside the
normalised masked fingerprint. The invocation's random directory remains ignored;
the parent remains part of the identity for every payload path and scalar
representation. Hashing the parent unconditionally also covers a path-dependent
assertion that publishes no path in the baseline. Oracle payloads, report
fingerprints and environment keying remain unchanged.

`TestWarmParentChangesInvalidateCachedVerdicts` executes real Terraform for plan
and apply, direct parent changes and symlink retargeting, with the path either
published as an output or used only in the assertion. Its independent oracle is
the assertion's truth table: `KEPT` survives under `special` and is killed under
`ordinary`. The return to `special` must reuse its original survivor verdict.
Erasing the parent replays the wrong survivor in the middle leg; disabling cache
reuse entirely fails the return leg. The existing eight-case matrix continues to
check unchanged-parent reuse across escaped and physical path representations.

The focused rerun passed all eight parent-transition cases and all eight existing
unchanged-parent path cases, plus ordinary cache reuse, volatile-apply reuse,
key-dimension invalidation, the version-9-to-10 upgrade, component logging, and
both CLI debug cases. The log is `.artifacts/cache-review/parent-verification.log`.
Full test and lint suites remain the outer executor's phases.
