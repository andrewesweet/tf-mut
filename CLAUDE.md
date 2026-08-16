# tf-mut — agent instructions

Mutation testing and characterisation-test scaffolding for `terraform test`, optimised for
fully-mocked unit tests. **Design phase**: no implementation exists yet. Implementation work
is specified per milestone as GitHub issues labelled `ready-for-agent`.

## Reading order

1. `README.md` — problem, verified findings, document map
2. `docs/design/product-design.md` — architecture, mutant states, metrics, CLI, roadmap
3. `docs/reviews/` — **both adversarial reviews and their dispositions. Read before changing
   any design decision**: many decisions exist specifically because a review refuted the
   obvious alternative, with experiments
4. `docs/design/mutation-operators.md`, `characterisation.md`, `agent-integration.md`
5. `docs/research/01–04` — the verified factual base ([verified] = established by running
   Terraform v1.15.8, not by reading documentation)

## Precedence on conflict

Review dispositions (`docs/reviews/`) > the milestone spec (GitHub issue) > design prose.
If you find a genuine conflict, fix the losing document in the same change.

## Standing process rules (earned, not aspirational)

1. **No performance or feasibility claim enters a design document until measured against a
   realistically-sized provider schema.** Round-one review: all four criticals traced to
   measuring against a 3.4 KB provider schema when `hashicorp/aws` is 14.5 MB.
2. **A repair to a reviewed finding is unreviewed design until it has itself survived review
   or reproduction.** Round-two review: three of five criticals were introduced by round-one
   repairs.
3. **Never trust a plausible reading of Terraform behaviour — run it.** This project's
   research corrected four widely-repeated false claims (`-filter` scope, mock+apply,
   `relevant_attributes`, mock determinism). Fixtures in `research/spikes/` are cheap to
   extend; extend them.

## Testing seam (fixed decision — do not reopen per milestone)

One seam, at the top: the engine entry point — configuration in, report out — exercised
end-to-end against the **real Terraform binary** on fixture modules. No fake Terraform
runner: both reviews proved the correctness risk lives in real Terraform behaviour (inode
sharing, implicit run-block state, evaluation-time errors, identical static/dynamic
diagnostics). Tests assert external behaviour only (report states, findings, metrics, exit
codes), never internals. Fixtures are null-provider/`terraform_data`-based, offline and
millisecond-fast; the one real-provider performance measurement is network-gated and kept
separate. The R1/R2 reproduction cases (recipes in review docs and issue #1) are mandatory
fixtures.

## Conventions

- Implementation language is Go with `github.com/hashicorp/hcl/v2` — settled, see
  `docs/research/03-hcl2-tooling.md`.
- The source tree of a module under test is never written to. Sandboxes only.
- New milestone specs: run `/to-spec` against the design docs; one milestone per spec;
  absorb the previous milestone's implementation learnings before writing the next spec.
- Safety gates (`--allow-real-infrastructure`, `--allow-unsandboxed-effects`) are
  load-bearing product decisions, not defaults to soften.
