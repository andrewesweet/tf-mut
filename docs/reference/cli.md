# Command-line reference

Generated from the binary's help table: `tf-mut --help` and every `tf-mut <command> -h` render from the same source, and a golden test fails when this page drifts from it. Regenerate with `just reference`.

## run

`tf-mut run [flags] [PATH]`

Mutate the module at PATH and report which resources are pseudo-tested

Runs the full mutation loop: generate the mutant population, execute the test suite against each mutant in an isolated sandbox, and score what the suite observes. Findings fail the run, and --min-score turns the score into a gate.

Arguments: `PATH  The module directory to mutate (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--min-score N` | Fail below this mutation score percentage | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--allow-incomplete-score` | Let an untrustworthy score satisfy --min-score (timeout, nothing scored, Invalid-dominated) | — |
| `--allow-sampled-gate` | Let a sampled run satisfy --min-score (unsafe) | — |
| `--no-cache` | Disable the project-local verdict cache | — |
| `--fail-on-new` | Fail on findings the baseline does not accept | — |
| `--write-baseline` | Accept the current findings as the baseline | — |
| `--baseline PATH` | Baseline file | `".tf-mut-baseline.json"` |
| `--tier LEVEL` | Operator breadth: smoke, standard or deep | `standard` |
| `--since REF` | Run only mutants in configuration changed since REF, including staged, unstaged and untracked changes | — |
| `--sample N` | Run a deterministic N% sample (non-authoritative) | — |
| `--seed N` | Seed for --sample | `0` |
| `--generated-functions` | Opt in to the generated function-family operators | — |
| `--pack NAME[,NAME]` | Enable these domain packs by name: a shipped pack such as security-aws, or one registered in .tf-mut.hcl; merged as a union with the configured packs list | — |
| `--operator ID[,ID]` | Restrict generation to these operators | — |
| `--exclude-operator ID[,ID]` | Remove operators from the population | — |
| `--exclude-path GLOB[,GLOB]` | Remove sites in matching files | — |
| `--exclude-resource ADDR[,..]` | Remove sites in matching resources | — |

Example:
```bash
$ tf-mut run --min-score 80 ./modules/vpc
```

## preview

`tf-mut preview [flags] [PATH]`

List the mutants that would be generated, as diffs, executing nothing

Prints the mutant population as unified diffs against the module source. No test run blocks execute.

Arguments: `PATH  The module directory to preview (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--tier LEVEL` | Operator breadth: smoke, standard or deep | `standard` |
| `--since REF` | Run only mutants in configuration changed since REF, including staged, unstaged and untracked changes | — |
| `--sample N` | Run a deterministic N% sample (non-authoritative) | — |
| `--seed N` | Seed for --sample | `0` |
| `--generated-functions` | Opt in to the generated function-family operators | — |
| `--pack NAME[,NAME]` | Enable these domain packs by name: a shipped pack such as security-aws, or one registered in .tf-mut.hcl; merged as a union with the configured packs list | — |
| `--operator ID[,ID]` | Restrict generation to these operators | — |
| `--exclude-operator ID[,ID]` | Remove operators from the population | — |
| `--exclude-path GLOB[,GLOB]` | Remove sites in matching files | — |
| `--exclude-resource ADDR[,..]` | Remove sites in matching resources | — |

Example:
```bash
$ tf-mut preview ./modules/vpc
```

## suggest

`tf-mut suggest [flags] [PATH]`

Generate, verify and optionally apply the assertions that kill the survivors

Diffs each survivor against the baseline, renders the candidate assertion, and verifies it by re-running the suite. --dry-run prints candidates and verifies nothing; --apply and --all-verified write verified assertions to the test files.

Arguments: `PATH  The module directory to suggest for (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--min-score N` | Fail below this mutation score percentage | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--allow-incomplete-score` | Let an untrustworthy score satisfy --min-score (timeout, nothing scored, Invalid-dominated) | — |
| `--allow-sampled-gate` | Let a sampled run satisfy --min-score (unsafe) | — |
| `--no-cache` | Disable the project-local verdict cache | — |
| `--fail-on-new` | Fail on findings the baseline does not accept | — |
| `--write-baseline` | Accept the current findings as the baseline | — |
| `--baseline PATH` | Baseline file | `".tf-mut-baseline.json"` |
| `--tier LEVEL` | Operator breadth: smoke, standard or deep | `standard` |
| `--since REF` | Run only mutants in configuration changed since REF, including staged, unstaged and untracked changes | — |
| `--sample N` | Run a deterministic N% sample (non-authoritative) | — |
| `--seed N` | Seed for --sample | `0` |
| `--generated-functions` | Opt in to the generated function-family operators | — |
| `--pack NAME[,NAME]` | Enable these domain packs by name: a shipped pack such as security-aws, or one registered in .tf-mut.hcl; merged as a union with the configured packs list | — |
| `--operator ID[,ID]` | Restrict generation to these operators | — |
| `--exclude-operator ID[,ID]` | Remove operators from the population | — |
| `--exclude-path GLOB[,GLOB]` | Remove sites in matching files | — |
| `--exclude-resource ADDR[,..]` | Remove sites in matching resources | — |
| `--apply ID[,ID]` | Apply these verified suggestions to the test files | — |
| `--all-verified` | Apply every verified suggestion | — |
| `--survivor ID[,ID]` | Suggest only for these survivor identifiers | — |
| `--dry-run` | Print the candidate patches and verify nothing | — |

Example:
```bash
$ tf-mut suggest --dry-run ./modules/vpc
```

## characterise

`tf-mut characterise [flags] [PATH]`

Scaffold, harvest and pin a first test suite for a module that has none

Plans a scaffold suite, runs it against the module to harvest the observed values, and pins them as assertions. Values no deterministic pipeline can synthesise become judgement points for --answer instead of guesses.

Arguments: `PATH  The module directory to characterise (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--pin LEVEL` | Pinning granularity: outputs, counts or configured | `outputs; a module with no outputs escalates` |
| `--write` | Place the verified suite in the test directory | — |
| `--force` | Replace generated files nobody has edited | — |
| `--until-dry` | Iterate scaffold, mutate and pin until the survivors stop yielding new assertions | — |
| `--answer todo-ID=VALUE` | Answer one judgement point; repeatable | — |
| `--resume` | Read answered judgement points from the edited artefact, re-synthesise, verify and promote | — |

Example:
```bash
$ tf-mut characterise ./modules/vpc
```

## todos

`tf-mut todos [flags] [PATH]`

List the open judgement points characterisation refuses to guess at

Reports every open judgement point with the identifier --answer needs to resolve it.

Arguments: `PATH  The module directory to list judgement points for (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--pin LEVEL` | Pinning granularity: outputs, counts or configured | `outputs; a module with no outputs escalates` |
| `--answer todo-ID=VALUE` | Answer one judgement point; repeatable | — |
| `--resume` | Read answered judgement points from the edited artefact, re-synthesise, verify and promote | — |

Example:
```bash
$ tf-mut todos ./modules/vpc
```

## curate

`tf-mut curate [flags] [PATH]`

Report redundant assertions from a full, authoritative population

Re-checks redundancy against a full, unsampled population: a narrowed population is refused rather than reported as the whole.

Arguments: `PATH  The module directory to curate (default ".")`

| Flag | Description | Default |
| --- | --- | --- |
| `--test-directory PATH` | Test directory relative to the module | `"tests"` |
| `--jobs N` | Mutants to execute concurrently | `three quarters of the CPUs, at least 1` |
| `--timeout-factor F` | Multiple of the baseline run time | `10` |
| `--preflight-timeout D` | Absolute cap on preflight Terraform calls: version, init, providers schema, fmt, validate and the baseline runs | `10m for init, 1m for the rest` |
| `--allow-real-infrastructure` | Permit execution against unmocked providers | — |
| `--allow-unsandboxed-effects` | Permit apply-mode provisioners and unsevered data sources | — |
| `--reporter FORMAT` | terminal\|json\|sarif\|mte\|html\|junit\|markdown | `terminal` |
| `--output FORMAT=PATH` | Write an additional reporter from the same run; repeatable — every output derives from one report value | — |
| `--sarif-path PATH` | Where to write the SARIF document | — |
| `--cpu-profile PATH` | Write a CPU profile of tf-mut itself | — |
| `--otlp-endpoint URL` | Export run, mutant and phase spans over OTLP/HTTP (OTEL_EXPORTER_OTLP_ENDPOINT is honoured when unset) | — |
| `--min-score N` | Fail below this mutation score percentage | — |
| `--failure-trip N` | Stop the run after this many consecutive operational failures; the report names the trip and the first failure, and the score is marked incomplete | `5` |
| `--allow-incomplete-score` | Let an untrustworthy score satisfy --min-score (timeout, nothing scored, Invalid-dominated) | — |
| `--allow-sampled-gate` | Let a sampled run satisfy --min-score (unsafe) | — |
| `--no-cache` | Disable the project-local verdict cache | — |
| `--fail-on-new` | Fail on findings the baseline does not accept | — |
| `--write-baseline` | Accept the current findings as the baseline | — |
| `--baseline PATH` | Baseline file | `".tf-mut-baseline.json"` |

Example:
```bash
$ tf-mut curate ./modules/vpc
```

## skill

`tf-mut skill install [flags]`

Install the shipped agent skills (skill install [--agent claude|generic] [--path .])

Places the mutation-loop and characterisation-loop skills for the chosen agent, preserving user-edited files unless --force is given.

Arguments: `install  The only skill subcommand: install the skills into the project at --path`

| Flag | Description | Default |
| --- | --- | --- |
| `--agent claude|generic` | Target agent for the installed skills | `"claude"` |
| `--path DIR` | The project root to install into | `"."` |
| `--force` | Overwrite user-edited skill files | — |

Example:
```bash
$ tf-mut skill install --agent generic --path .
```

## version

`tf-mut version`

Print the build version

Prints the linked build version, or "dev" when none was supplied.

Arguments: `(none — version takes no path)`

Takes no flags.

Example:
```bash
$ tf-mut version
```

## Exit codes

`0` — the command completed with nothing to report (explicit `--help` exits `0`).

`1` — the command completed and found something worth failing on.

`2` — the command could not produce a trustworthy result, or the invocation was wrong: a bad flag, an unknown command, or a flag after the module path.
