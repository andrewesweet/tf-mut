# Verdict-cache debug evidence

The captain's option C decision limits this change to Step 1: cache-key digest
diagnostics. Set `TF_MUT_DEBUG=1` to enable them through the shipped CLI. Logs go
to the run-scoped standard-error writer, with the report's `[run_id]` prefix on
every line; reports remain on standard output. Only digests are logged, never
cache-key input values.

Cache-key computation matches base commit
`a0929f343cdf423afe73bb41ff7f37caf120d723`, including cache format 6, the original
masked baseline fingerprint and environment keying. The path substitutions,
physical-parent hashing, helper extraction, and their tests have been removed.
The cache-miss correction and transformed-path handling are outside this change.

## Reproduction findings

The earlier reduced-fabn and impure-value probes were stable, as recorded in the
review context. They did not establish the cause of the original large fabn run.

Using locked Go 1.26.9 and Terraform 1.15.8, the reduced offline module with an
unasserted `value = abspath(path.root)` output reproduced a changing baseline
component. Both plan and apply executed six fresh verdicts and replayed none on
each unchanged invocation. The other component digests were stable within each
command. The original probe log is `.artifacts/cache-review/before.log`.

| Command | First baseline component digest | Second baseline component digest |
| --- | --- | --- |
| plan | `6f84838bf5ca41a24199b8da211d0c08f6188b547a2f3df263a50942a17f7b6a` | `ed8c54415f06963df530d6e941eb1b98e3493df0538adb71986a977e1d13cfc8` |
| apply | `3ac0ed2f91555ef9fe74e812024ec114cfb1b47d89c8bd6fc56572b3ad41bffe` | `d57b5a95f42bd35ed714ecb66c2b23447c469cb60f37229a779a35e77756ce20` |

Both baseline legs share one warm directory, so their volatility mask retains its
path. The next invocation uses a different directory. The `dirname` probe also
reproduced misses; its log is `.artifacts/cache-review/derived-before.log`.
These findings explain what the diagnostics reveal; they are not claims that
this diagnostics-only change fixes those misses.

## Reason for removing path rewriting

The experimental substitutions could also hide assertion-relevant differences.
With `output.asserted == "kept" || strcontains(output.path, "/special/")`, changing
the physical temporary parent from `special` to `ordinary` replayed a survivor
that a cache-disabled run killed, in both plan and apply. The log is
`.artifacts/cache-review/parent-before.log`. Review also identified invocation-name
length as observable by an assertion and transformations such as `basename` and
`base64encode` that prefix substitutions do not cover.

The captain chose to remove the identity changes entirely. Earlier path-related
repairs and their passing tests describe superseded experiments. The shipped
change preserves the pre-task identity and makes no path-normalization claim.

## Retained verification contracts

`TestCacheKeyComponentsAreLoggedAtDebug` executes real Terraform through the
engine seam. It checks digest output, environment-value withholding, unchanged
suite reuse after enabling diagnostics, and correlation between a logged key and
cached report provenance. It captures the request's output writer without
changing the process-global logger.

`TestCacheDebugLogsAreReachableThroughTheCLI` starts subprocesses through `main`
for both run and suggest. The public opt-in contract supplies its oracle: debug
logging is silent by default, `TF_MUT_DEBUG=1` emits the component digests on
stderr, every line carries the report's run identifier, environment values remain
withheld, and stdout is a decodable report. README's stderr correlation contract
supplies the identifier oracle; an unprefixed component or full-key line fails
the test for both run and suggest.

The focused verification passed in `internal/engine` and `cmd/tf-mut`, using the
locked toolchain through Just. It covered the retained debug tests, unchanged
cache reuse, replay equivalence, key-dimension invalidation, baseline and
environment changes, previous-format misses, cache opt-outs, and the generated
CLI reference. The log is
`.artifacts/cache-review/diagnostics-only-verification.log`. Full test, lint and
CI gates remain with the outer executor.

The R9 focused verification also passed in both packages. Run and suggest were
each exercised with debug disabled, on a debug cache miss, and on a debug cache
hit. Every emitted stderr line matched the report's run identifier, including
component and full-key records; stdout remained a decodable JSON report. The
engine test confirmed that enabling diagnostics alone preserved cache reuse.
Existing tracing-output, short-run silence, rendering-failure and CLI-reference
checks passed in the same invocation. The log is
`.artifacts/cache-review/run-scoped-debug-verification.log`.
