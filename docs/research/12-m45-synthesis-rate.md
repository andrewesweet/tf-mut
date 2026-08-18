# M4.5-0 — the synthesis-rate measurement

The measurement that gates M4.5b's start, run on 18 August 2026 against Terraform v1.15.8.

It replaces revision 1's form census, which the M4.5 spec review's M5 refuted as a proxy that
could not fail its own gate: it had no threshold, no decision, and left the contract unchanged
under every possible result. What runs here is the shipped preference pipeline itself —
defaults, then validation mining, then typed synthesis, every candidate checked statically
against the module's own validation conditions — over a pinned corpus of public modules,
driven through the engine seam by `tf-mut todos`.

- Corpus manifest and digests: [`research/corpus/m45-synthesis.json`](../../research/corpus/m45-synthesis.json)
- Harness: `internal/engine/corpus_integration_test.go`, network-gated
- Recipe: `just measure-synthesis` (requires `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`)
- Published output: `.artifacts/measurement/m45-synthesis.json`

## The decision rule, stated before the run

> If the median corpus module yields **no** executable default scenario without a TODO answer,
> the `--answer` batch path becomes a mandatory M4.5b deliverable and the product claim is
> reworded from the measured rate; otherwise the spec ships as written.

## What was measured

| Module | Tag | Inputs | From default | Mined | Typed | Open TODOs | Executable |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| terraform-aws-modules/vpc | v5.13.0 | 230 | 230 | 0 | 0 | 0 | yes |
| terraform-aws-modules/s3-bucket | v4.1.2 | 53 | 53 | 0 | 0 | 0 | yes |
| terraform-aws-modules/security-group | v5.1.2 | 56 | 56 | 0 | 0 | 0 | yes |
| terraform-aws-modules/rds | v6.7.0 | 102 | 101 | 0 | 1 | 0 | yes |
| terraform-aws-modules/lambda | v7.7.0 | 128 | 128 | 0 | 0 | 0 | yes |
| terraform-aws-modules/eks | v20.8.5 | — | — | — | — | — | **refused** |
| terraform-aws-modules/iam | v5.39.0 | 0 | 0 | 0 | 0 | 0 | yes (vacuously) |
| terraform-google-modules/network | v9.1.0 | 17 | 14 | 0 | 3 | 0 | yes |
| Azure/naming | 0.4.1 | 5 | 5 | 0 | 0 | 0 | yes |
| cloudposse/null-label | 0.25.0 | 18 | 18 | 0 | 0 | 0 | yes |

Measured: 9 modules. Executable default scenario with no answer: **9 of 9**. Refused: 1.
**Median open judgement points per module: 0.**

## The decision, applied

The median corpus module yields an executable default scenario with no TODO answer, so the
milestone **ships as specified**: `--answer` stays a per-identifier flag (repeatable, so a
scripted caller can still supply several in one invocation) rather than gaining a mandatory
batch path, and the product claim is not reworded.

## Three findings the number carries with it

**1. Defaults do nearly all the work, and that is the design's own claim.** 605 of the 609
resolved inputs came from the module's own declared default; typed synthesis supplied 4;
validation mining supplied **none at all**. The design's honest caveat — that the minable
share was unquantified and probably small — is confirmed, and the preference order that puts
defaults first is what makes the rate what it is. Mining is worth keeping (it costs little and
it is the only rung that reads a constraint as a *value*), but nothing in the product claim
may rest on it.

**2. The refusal of `moved` has a measured cost: one corpus module in ten.**
`terraform-aws-modules/eks` v20.8.5 declares `moved` blocks, and the M4.5 spec review's C4
disposition refuses them by name in the native syntax. The module is therefore not
characterisable at all — not one input resolved, not one scenario planned. The refusal is
the disposition of record and is not reversed here (a repair to a reviewed finding is
unreviewed design until it has itself survived review), but the cost is now measured rather
than assumed, and it is flagged for the next review under standing rule 2. Worth the
reviewer's attention: `import` genuinely names a provider configuration and reads a real
resource at plan time, which is the R2-10 fail-open shape; `moved` is state bookkeeping with
no provider, no effect and no evaluation, and the two were disposed of as one construct.

**3. A zero-input module reports "executable" vacuously.** `terraform-aws-modules/iam`
declares no root inputs at all — it is a wrapper over submodules — so its default scenario is
executable because there was nothing to synthesise. The row is kept in the table rather than
dropped, because a rate computed over rows like it would flatter the pipeline. The nine-of-
nine result holds without it: eight of eight.

## What the measurement does not say

It measures whether a scenario is **executable**, not whether the plan it produces is
**meaningful** — failure mode B4 in `docs/design/agent-integration.md` is not mechanically
detectable, by construction. A module whose inputs all default is characterised under its
default configuration, which is the intended first suite and not a claim about coverage.
Mutation testing measures the gap; this measurement sizes the entry cost.
