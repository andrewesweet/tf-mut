# M5-0.5b — the bounded repair prototype

The throwaway repair-table prototype over the M5-0.5a census population, run on
17 September 2026 with Terraform v1.15.8. The census found 14 unresolved inputs
across 8 benchmark modules. This second stage asked whether the pinned table below
could produce a green staged module scenario under the shipped characterisation
safety posture.

- Input census: [`19-m5-opportunity-census.md`](19-m5-opportunity-census.md)
- Harness: `internal/engine/repairprototype_integration_test.go`
  (`TestTheRepairPrototypeOverThePinnedOpportunities`, integration-tagged)
- Offline table, adapter and acceptance pairs:
  `internal/engine/repairprototype_test.go`
- Recipe: `just measure-repair` (requires
  `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`; this permits archive fetching, not either
  product safety opt-in)
- Published output: `.artifacts/measurement/m5-repair-prototype.json`

## The pinned table

The table was committed before the measurement and written to the output artefact
before the harness fetched an archive, probed Terraform or staged a scenario. Its
canonical JSON SHA-256 is
`6a24c4f659f8e9cbaeb41469f6d5e33e270b16123400bca3adc72303d6cc9fbf`.
`TestTheRepairCandidateTableIsPinnedAndUsesTodoConstraints` fails if either the
bytes or the digest move.

Initial lookup reads only the judgement point that `tf-mut todos` publishes: the
input name, the constraint expression and its module-source range. Function names
are parsed from that expression. Runtime diagnostic summaries, details and ranges
are not lookup keys.

| Constraint-expression key | First candidate | One possible retry |
| --- | --- | --- |
| Any of `cidrnetmask`, `cidrsubnet`, `cidrhost`, `cidrsubnets` | `"10.0.0.0/16"` | `"192.168.0.0/24"` |
| Any of `regex`, `regexall`, `startswith`, with an expression literal containing the `arn:` prefix | `"arn:aws:s3:::tf-mut"` | `"arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000"` |
| No constraint and no default, declared `string` | `"tfmut-repair"` | `"tfmut-repair-next"` |
| No constraint and no default, declared `number` | `2` | `3` |
| No constraint and no default, declared `bool` | `false` | `true` |
| Any other constraint or type | no candidate | — |

A constraint matching more than one row is also no-candidate. The sequence is
finite and pinned: the first value is the initial candidate and only the second
can be used for a retry.

## Diagnostic mapping and retry bound

A red initial attempt can identify one input from structured fields only:

1. `snippet.values[].traversal` whose traversal starts with `var.<name>`; or
2. a structured diagnostic range wholly inside one of that module's validation
   condition ranges.

All structured evidence across all diagnostics is unioned. Exactly one distinct
input permits the retry. Zero inputs, two traversals naming different inputs, or a
traversal and range naming different inputs are unmappable. Summary, detail and
snippet statement prose are retained by the parser only long enough to prove that
changing them has no effect; the mapper never reads them.

`TestTheRepairPrototypeMapsOnlyOneStructuredInputAndRetriesOnce` exercises zero,
exactly one, ambiguous and conflicting mappings. Only the exactly-one cases return
a retry input, and the adapter returns no retry after one has already occurred.
`TestTheRepairPrototypeUsesTodosForLookupAndStructuredFieldsForMapping` promotes
the review probe as an executable fixture: `todos` publishes
`can(cidrnetmask(var.cidr))` and its `main.tf` range, while Terraform's failed
attempt points its runtime range at the generated `.tftest.hcl` literal and carries
`snippet.values[].traversal = var.cidr`.

## Execution posture

Each module with a candidate for every unresolved input was submitted as one
`CharacteriseRequest`, with all candidates supplied as answers. This drives the
existing characterise pipeline rather than a second runner: one planned mock per
provider configuration; the JSON floor and both safety gates after the single
`version -json` probe and before `init`, schema or test; the normal warm-up;
sandbox materialisation of the effective staged suite; and the normal controlled
Terraform environment. Both `AllowRealInfrastructure` and
`AllowUnsandboxedEffects` remained false. The recipe's environment variable was
not copied into either setting.

A failed answer remains inside the existing characterisation rejection and M4.5
M8 redaction path. The prototype publishes only an outcome, structured input name
where mapping proved one, sibling statuses and costs. It publishes no candidate,
raw diagnostic, diagnostic prose or snippet. The two acceptance pairs are direct:

- `TestARepairPrototypeGateRefusalInvokesOnlyVersion` records exactly one
  `version` invocation and no other Terraform command when the effects gate
  refuses the staged suite.
- `TestASecretOnlyInARepairFailedAttemptReachesNoPublishedArtefact` puts a planted
  secret into a sensitive failed answer and checks the prototype JSON, the engine
  JSON and terminal reports, generated artefacts and invocation log.

The corpus itself supplied the first pair again: `genai-idp-terraform` was refused
with exactly one `version -json` invocation.

## Results

Wall time starts after the digest-verified archive is available, so it measures
static selection and the staged characterise path rather than network transfer.
The final run used an already extracted archive cache on Linux
6.6.87.2-microsoft-standard-WSL2, x86-64, with 12 logical CPUs and 11 GiB RAM.
The figures are published measurements on that host, not portable assertions.
Invocation count is the number of Terraform processes, including the version
probe.

| Module | Opportunities | Outcome | Terraform invocations | Wall time |
| --- | ---: | --- | ---: | ---: |
| `aws-platform-starter` | 4 | no-candidate | 0 | 12 ms |
| `aws-terraform-infrastructure` | 1 | no-candidate | 0 | 24 ms |
| `eks-vulnerable-infra` | 1 | no-candidate | 0 | 3 ms |
| `genai-idp-terraform` | 4 | refused | **1** | 592 ms |
| `psoxy` | 1 | no-candidate | 0 | 8 ms |
| `server-terraform` | 1 | no-candidate | 0 | 17 ms |
| `serverless-architecture-patterns` | 1 | no-candidate | 0 | 67 ms |
| `terraform-datadog-users` | 1 | no-candidate | 0 | 4 ms |

Seven modules had at least one constraint naming no table function. This includes
non-ARN regular expressions: the ARN row does not turn every `regex` constraint
into permission to guess. `genai-idp-terraform` was the one module for which all
four unresolved inputs matched the ARN row. Its staged suite was refused before
execution because the closure contains two `external` data sources and 27
provisioners. Mock providers do not sever those effects, and the measurement did
not grant `--allow-unsandboxed-effects`.

| Population or outcome | Count |
| --- | ---: |
| opportunities | 14 |
| modules with opportunities | 8 |
| **measured cohort (verified + refuted)** | **0** |
| verified | 0 |
| refuted | 0 |
| refused | 1 |
| no-candidate | 7 |
| unmeasured | 0 |
| verified rate | not defined for an empty cohort |

Refused, no-candidate and unmeasured module scenarios are outside the cohort. No
scenario reached a plan verdict, so this result is not a zero-percent repair rate.
It is an empty measured cohort.

## Decision

The total rule's **insufficient** branch fired: the cohort is empty. The counts and
costs above are published; the repair-yield decision stays open; issue #82 stays
open; and no build issue is created. The reopen condition is a pinned corpus
revision whose opportunities produce a non-empty cohort under the same full safety
posture. `TestTheRepairDecisionRuleIsTotalOverTheCohort` holds all three branches
at their exact boundary.

The positive and amendment branches did not fire. In particular,
`docs/design/characterisation.md` section 3.2 and issue #75's design record remain
unchanged. The product still implements the same three rungs and stops at a
judgement point where the specified fourth rung would begin.

## Scope of the prototype

All executable prototype code is in `_test.go` files. The only fixture is the
review's diagnostic-shape probe, and the only public control-plane addition is the
network-gated measurement recipe. No candidate table, diagnostic mapper, retry,
status or report field is linked into the product binary. The shipped preference
order and characterise behaviour are unchanged.
