# Harness spikes

Throwaway Terraform fixtures used to verify the harness design before committing to it. Results
and analysis are in [`../../docs/research/04-harness-spike.md`](../../docs/research/04-harness-spike.md).

Verified against `Terraform v1.15.8 on linux_amd64`.

| Fixture | Shape | Verifies |
| --- | --- | --- |
| `fixture-a` | No provider (`terraform_data`), `count`, conditionals, `validation`, `expect_failures` | Baseline timing with zero provider cost; `-filter` semantics; exit codes; JSON event types |
| `fixture-b` | `hashicorp/null` behind `mock_provider`, plan **and** apply run blocks | The target use case; sandbox isolation strategies; plan fingerprinting; mock determinism; throughput at 100 mutants |
| `fixture-c` | Root module calling a local child module, plus a run block targeting the child directly | Child-module mutation propagation under a shared `.terraform`; `modules.json` path resolution |

## Running them

```bash
cd fixture-b
terraform init
terraform test
```

`fixture-b` and `fixture-c` need network access on first `init` to fetch `hashicorp/null`.
`fixture-a` needs none — it uses only the built-in `terraform_data` resource.

`.terraform/` and lock files are gitignored; re-run `terraform init` after cloning.
