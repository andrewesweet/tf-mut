# Harness spikes

Throwaway Terraform fixtures used to verify the harness design before committing to it. Results
and analysis are in [`../../docs/research/04-harness-spike.md`](../../docs/research/04-harness-spike.md).

Verified against `Terraform v1.15.8 on linux_amd64`.

| Fixture | Shape | Verifies |
| --- | --- | --- |
| `fixture-a` | No provider (`terraform_data`), `count`, conditionals, `validation`, `expect_failures` | Baseline timing with zero provider cost; `-filter` semantics; exit codes; JSON event types |
| `fixture-b` | `hashicorp/null` behind `mock_provider`, plan **and** apply run blocks | The target use case; sandbox isolation strategies; plan fingerprinting; mock determinism; throughput at 100 mutants |
| `fixture-c` | Root module calling a local child module, plus a run block targeting the child directly | Child-module mutation propagation under a shared `.terraform`; `modules.json` path resolution |
| [`m5-lifecycle/ignore-drop`](m5-lifecycle/ignore-drop/) | `terraform_data` only, `original/` and `mutant/` variants; apply run, then plan run sharing a `state_key`, input changed; assertion `input == "old"` | M5-0.1 kill witness for `LC-IGNORE-DROP`; original green, mutant killed |
| [`m5-lifecycle/ignore-all`](m5-lifecycle/ignore-all/) | `terraform_data` only, `original/` and `mutant/` variants; apply run, then plan run sharing a `state_key`, input changed; assertion `input == "new"` | M5-0.1 kill witness for `LC-IGNORE-ALL`; original green, mutant killed |
| [`m5-lifecycle/replace-trigger-drop`](m5-lifecycle/replace-trigger-drop/) | `terraform_data` only, `original/` and `mutant/` variants; apply run, then apply run sharing a `state_key`, revision changed; assertion `id != run.first.subject_id` | M5-0.1 kill witness for `LC-REPLACE-TRIGGER-DROP`; original green, mutant killed |

## Running them

```bash
cd fixture-b
terraform init
terraform test
```

Only `fixture-b` uses `hashicorp/null`; bootstrap installs its exact locked package into the
repository-local filesystem mirror. Fixtures A and C use only built-in Terraform resources.

Generated `.terraform/` directories are ignored. Provider-bearing fixtures commit dependency
locks and run offline after `just tools-install` populates the mirror.
