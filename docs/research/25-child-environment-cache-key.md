# 25 — The child-environment key replay: every recorded transition, false-reuse 0

What building D3 replayed, decided and deferred. The
[spec amendment](../reviews/2026-08-16-m3-spec-review.md#addendum--25-october-2026-child-environment-cache-key)
supersedes the environment clause of M3 spec review C4; this note is the
acceptance record the captain gated the build on: every environment transition
the design review recorded, replayed against the new key, with a published
false-reuse count of 0. Evidence grades follow the design review's: **Direct**
= measured or read here, **Supported** = inference from agreeing direct
observations, **Inferred** = reasoned.

## 1. What was replayed, and how

The design review recorded nine environment transitions with the exact names
that differed on each: six over the `all-killed` fixture across six session
kinds (d-series) and three over the utl-regions suite (U-series). The original
per-variable manifests did not survive the review task, so the replay inputs
are the recorded name-diffs applied to one representative session environment
carrying every stripped name and the forwarded names the MISS rows turn on.
The inputs live in the test, not in a throwaway script:
`internal/tfexec/nonforwarded_test.go` (`replayBase`, `replayTransitions`).

The replay runs at two levels, against the real derivation in both:

- **Key level.** Each transition's before/after environments pass through the
  seam's own stripping step and child assembly — the same two functions the
  runner and the key use — and the replay asserts the HIT/MISS decision equals
  the review's D3 column: HIT exactly where only stripped names differ.
- **Verdict level.** Every transition the key grants a HIT on is re-driven at
  the engine seam over the real fixture with the recorded name-diffs applied
  to the live process environment, and the replayed verdicts are asserted
  byte-identical to the fresh ones (`assertVerdictInvariance`). A false reuse
  is a HIT whose verdict differs from the fresh verdict (M4 M5); each such
  assertion counts them, and the published count is the sum.

## 2. Per-transition results

| Transition (review name) | Recorded diff | New key | Verdicts |
| --- | --- | --- | --- |
| agent to agent (d1→d2) | changed `TRACEPARENT` | HIT | identical — Direct |
| agent to tmux (d2→d3) | added `TMUX`, `TMUX_PANE`; changed `PATH`, `SHLVL`, `TERM`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`, `TRACEPARENT` | MISS (`PATH`, `TERM`) | n/a (miss) |
| tmux to systemd login (d3→d4-1) | added `OTEL_EXPORTER_OTLP_ENDPOINT`; wholesale removal incl. `TMUX*`, `SSH_*` | MISS (endpoint, `PATH`, `SSH_AUTH_SOCK`) | n/a (miss) |
| systemd unit to systemd unit (d4-1→d4-2) | changed `INVOCATION_ID`, `MEMORY_PRESSURE_WATCH`, `SYSTEMD_EXEC_PID` | HIT | identical — Direct |
| systemd login to clean login (d4-2→d5) | added `TERM`; removed 21 incl. `SSH_AUTH_SOCK`, `GIT_CONFIG_*`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `INVOCATION_ID`; changed `OLDPWD`, `PATH`, `XDG_DATA_DIRS` | MISS (`PATH`, removals) | n/a (miss) |
| clean login to agent (d5→d6) | 113 added | MISS (`PATH`, `SSH_AUTH_SOCK`, `TERM`, `GIT_CONFIG_COUNT`) | n/a (miss) |
| agent to agent, debug on (U1→U2) | added `TF_MUT_DEBUG`; changed `TRACEPARENT` | HIT | identical — Direct |
| agent to agent (U3→U4) | changed `OLDPWD`, `TRACEPARENT` | HIT | identical — Direct |
| agent to systemd login (U4→U5) | added `OTEL_EXPORTER_OTLP_ENDPOINT`; wholesale removal | MISS | n/a (miss) |

9 of 9 key decisions agree with the D3 column, and every HIT carries a
verdict-identity proof: **false-reuse count 0** — Direct. The U1→U2 row pins
the review's own correction: the prototype's strip set lacked the `TF_MUT_*`
entry and left `TF_MUT_DEBUG` as a miss; the shipped set grants the HIT.

Beyond the table, one sweep test changes all eighteen session bookkeeping
names at once, to values no verdict could survive seeing, and still replays
every verdict identical — the whole strip set's false-reuse proof in one run.
A mixed transition (stripped change plus one forwarded addition) still misses
and names only the forwarded name. `TF_VAR_names` set in one session and
absent in the next still misses and is named as removed, pinning that the
verdict-relevant in-process read is never stripped. The existing
added/changed/removed explanation tests keep passing: a forwarded name is
still named, with values still withheld.

## 3. What the build changed, and what it did not

Commit 1 moves the key onto the child environment with only the git location
variables stripped and the performance defaults included (`tf-mut-cache-14`).
Commit 2 grows the strip set to the reviewed list at the same seam
(`tf-mut-cache-15`). Both change what the key hashes, so both bump the format
and old entries miss rather than replay. The miss-explanation wire format is
unchanged: `CacheMiss` still carries only names, still bounded at 32 — the
cap the review flagged stays a noted follow-up, out of scope here.

Out of scope by the brief, and still so: an allowlist, a key-side denylist, a
user-declared ignore list, any change to the mutant sandbox or
provider-verification code, and any release or CI configuration change.

## 4. Limits

- The replay environments are representative, not the review's original
  manifests: the per-transition decisions depend only on which names differ,
  which the review recorded exactly, so the substitution changes no decision —
  Inferred.
- The suite-level rows replay their name-diffs over the fixture, not over
  utl-regions: the verdict-identity proof is about the key rule, which is
  suite-independent, and the suite needs network the build gate forbids —
  Supported.
- One machine, one agent harness, as in the review. The SSH-session case
  stays undecided: if a later SSH miss names only SSH bookkeeping, the strip
  set grows by amendment; if it names `SSH_AUTH_SOCK`, the miss stands —
  that socket authenticates subprocesses, and standing rule 5 keeps those.
