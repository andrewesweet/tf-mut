# The cost model behind M2's speed levers — measured

Issue [#19](https://github.com/andrewesweet/tf-mut/issues/19), the pre-work M2 was blocked on.
Three experiments, run against `internal/engine/testdata/aws-mocked` through the engine's own
execution path, to establish what the planned speed levers would buy before either is
designed.

On this fixture, **run-block file splitting adds cost, and the milestone as scoped has no
measured speed lever for real-provider modules.** The evidence is below; the consequences for
the roadmap are in §5. [§4](#4-where-the-time-goes) also records the later real-suite
measurement that corrected the startup attribution and limits these conclusions to this fixture.
The later [L2 measurement](23-l2-single-verbose-phase.md) supersedes this note's recommendation
to keep two-phase execution; the [product design](../design/product-design.md#execute) owns
the current schedule. The streaming-memory requirement remains.

## Method

Hardware: 13th Gen Intel Core i5-13420H, 12 logical cores, WSL2 on Linux 6.6. Terraform
v1.15.8. Fixture: ten AWS resources behind `mock_provider "aws"`, `hashicorp/aws` 6.x at
roughly 840 MB installed.

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 go test -tags=integration,research ./internal/engine/ \
  -run TestCostModelForTheM2SpeedLevers -count=1 -timeout 60m
```

Network-gated, outside the offline suite, numbers written to
`.artifacts/performance/m2-cost-model.json`. Every configuration is preceded by an untimed
warm-up run, measured three times, and swept in both ascending and descending order.

> **A first pass without those controls reported the marginal cost of a run block as
> *negative*.** Measuring in ascending order alone let the page cache warm as the sweep
> progressed, and the artefact was large enough to invert the sign of the quantity being
> measured. It is recorded here because it is exactly the failure mode standing process rule 1
> exists to catch, and because the corrected numbers below are only trustworthy on account of
> the controls that removed it.

---

## 1. Splitting adds work. At every useful *k*.

One `terraform test` invocation covering *n* run blocks, against *n* invocations covering one
each — the transformation run-block file splitting performs.

Two independent runs, both after the controls above. The second is on the final build and is
the one plotted; the first is given alongside it because two runs agreeing is worth more than
one run stated precisely.

| Run blocks | One invocation | *n* invocations | Splitting costs |
| --- | --- | --- | --- |
| 1 | 1.61 s (1.66) | 1.62 s (1.66) | — |
| 2 | 1.63 s (1.69) | 3.31 s (3.28) | 2.0× |
| 4 | 1.68 s (1.67) | 6.53 s (6.47) | 3.9× |
| 8 | 1.70 s (1.74) | 13.20 s (13.25) | **7.8×** |

Least squares over the single-invocation sweep gives the cost model:

> **Fixed cost per invocation: 1.61 s (1.65). Marginal cost per run block: 0.012 s (0.010).**
>
> **A run block costs between one-hundred-and-thirty-fourth and one-hundred-and-sixty-second
> of an invocation.**

Wall time for one invocation rises by 5% from one run block to eight. Splitting the same eight
run blocks costs 678%. On this fixture, the fixed invocation term dominates.

### What that does to the crossover

Splitting is only worth its own cost if selection then executes *k* of the *n* files. Splitting
wins iff

```
(k − 1) · F  <  (n − k) · M          F ≈ 1.6 s,  M ≈ 0.012 s
```

At **k = 1** — perfect selection, one run block of eight — the saving is `7 × 0.012 = 0.08 s`
against a 1.70 s baseline: **5%**. At **k = 2** splitting already loses, by 1.5 s.
For the measured suite sizes, the fixed cost outweighs the removed run-block work whenever
selection keeps more than one block.

**Upper bound on splitting's total benefit for the M1 population:** 40 mutants × 8 run blocks ×
12 ms = **3.8 s out of 128 s, or 3%** — and only if selection were perfect, which §3 shows it
is not.

These wall-time results apply to `aws-mocked`'s near-empty mocked plan runs. A suite with
more expensive run blocks can have a different crossover, even in mocked plan mode.
Use the inequality with that suite's measured invocation and run-block costs; the
[real-suite correction](#4-where-the-time-goes) explains why this fixture does not bound real modules.

## 2. `-verbose` costs 1.7× in time and 20,000× in volume

Four run blocks, same fixture:

| | Wall time | Bytes on stdout |
| --- | --- | --- |
| `terraform test -json` | 1.64 s | 3,848 |
| `terraform test -json -verbose` | 2.77 s | **78,068,964** |

19.5 MB per run block. Round one's C1 identified the mechanism — the full provider schema is
re-serialised into every `test_plan` message — and measured a 26× marginal cost. Measured
through this harness the picture is sharper and different in kind:

- **The time cost is modest: 1.7×.** Not 26×.
- **The volume cost is enormous: 20,288×.**

So two-phase execution stays, but its justification changes. It is not primarily a wall-time
optimisation; it is what stops a full run moving **3.1 GB of JSON** (40 mutants × 78 MB)
through the harness. Two consequences for M2's design:

1. **The `test -json` decoder must stream.** The current `tfexec.Runner` buffers stdout into
   memory. At 78 MB per mutant and eight workers that is over half a gigabyte resident before
   fingerprinting has parsed anything. This is a hard requirement on the M2 implementation, not
   a nicety.
2. **The fingerprint must be computed incrementally**, discarding `provider_schemas` as the
   stream is read rather than after.

## 3. Module-level selection removes nothing on a single-module suite

For every mutation site, how many run blocks could possibly instantiate it? The bound below is
the module-level one — a run block can only instantiate a mutated block if it targets that
block's module or a module that calls it — which is what M1 already decides statically.

| Fixture | Shape | Run-block executions removed |
| --- | --- | --- |
| `aws-mocked` | one root module, 3 runs | **0%** |
| `upward` | root plus one `../` child, 1 run | 0% |
| `discriminate` | root plus one child, 1 run | 0% |
| `nocoverage` | root plus child, every run retargets the child | **66.7%** |

The pattern is not subtle. **Module-level instantiation reachability removes work only when a
suite retargets modules, and nothing at all when every run block plans the whole root module**
— which is the ordinary shape of a module's test suite, and the shape of the fixture the
milestone's exit gate uses.

Selection that helps a single-module suite has to be finer than the module: it has to know that
a resource with `count = var.enable ? 1 : 0` is not instantiated by a run that passes
`enable = false`. That is the attribute-level reference graph, scheduled M3, and it is the only
form of selection with anything to offer the common case.

Two honest limits on this experiment. It measures an upper bound at module granularity, so
finer analysis can only remove more; and no fixture in the corpus has conditional instantiation
within a module, so it cannot say how much more.

---

## 4. Where the time goes

Putting the three experiments together, the per-mutant wall time on `aws-mocked` is:

```
cost(mutant) ≈ 1.6 s  +  0.012 s × run blocks  +  sandbox materialisation
```

The first term dominates **on this fixture**, a single test file of trivial run blocks.
Its 1.6 s is an invocation cost, and 0.012 s is the marginal wall time of a near-empty run
block. The attribution of that constant to provider startup is withdrawn.

For this fixture, the full-run floor is `mutants × 1.6 s ÷ effective parallelism`.
M1 measured 128 s for 40 mutants. These measurements found no factor of two within M2's
scope; the selected roadmap levers reduce the mutant count: `--since`, the incremental
cache, sampling and tier selection, all scheduled for M3.

The 2026-10-12 cost-model report (`tfmut-perf-costmodel`) fitted a model on the real suites
physical-ai and utl-regions.
Section 10 of that report records its reproduction on host `bravo`, under
`~/scratch/tfmut-perf-costmodel`, driven by `measure.sh` and `measure2.sh`.
In CPU-seconds, each mutant's phase-one suite invocation costs:

```
T1 = c0 + Σ_files (p + r × runs)
```

Here, `c0` is the fixed invocation cost, `p` the per-file cost, `r` the per-run-block cost,
and `runs` the number of run blocks in that file. The fitted `p` is about 0.4–0.9 CPU-s per
file; `r` is about 0.19–0.45 CPU-s per run block. These CPU measurements must not be
substituted for the fixture's wall-time coefficients.

`strace` launch counts show provider plugins start once per test file, not once per run
block. Provider startup costs about 0.1 CPU-s per provider, under 3% of an invocation on
these suites. Terraform core graph building and expression evaluation per run block account
for 76–90% of invocation CPU. Provider-process reuse across run blocks therefore does not
address the dominant measured cost.

The earlier memory-bandwidth explanation for M1's weak scaling is also withdrawn:
physical-ai sustained 335% CPU at `--jobs 3` on a four-core machine. M1's measured 1.08×
wall-time improvement stands, but it does not establish a universal limit on parallelism.

tf-mut cannot reduce Terraform core's evaluation cost for a run block it executes.
Selection that safely omits files or run blocks can reduce invocation cost, so the
fixture's 3% selection ceiling is not a bound for real suites. This correction does not
reintroduce file splitting to the roadmap; the state and dependency constraints in §1 still apply.

## 5. Consequences for the M2 spec

Stated as recommendations. The spec author disposes of them.

1. **Drop run-block file splitting.** On this fixture, its ceiling is 3% under perfect selection
   and its cost is 7.8× when applied naively. It was adopted from round-one M7 as "the primary
   viability lever on real modules"; that claim is withdrawn and this document is the reason. Keeping it
   would also drag R2-4's unreproduced state-identity closure into the milestone for no
   measured return.
2. **Keep two-phase execution, and specify streaming with it.** §2. The requirement is memory,
   not time.
3. **Decide what to do about selection.** Module-level reachability is sound and nearly
   worthless on the common suite shape. Either pull the attribute-level graph forward from M3,
   accepting that it is the real deliverable, or ship selection as a correctness feature
   (`NoCoverage` without execution) and stop calling it a speed lever.
4. **Re-scope the milestone.** M2 as written is "Honesty and the speed levers". The honesty
   half — Tiers 1–3, fingerprinting with the volatile mask, the full state model, survivor
   diagnosis, reporters, `.tf-mut.hcl` — is unaffected by any of this and is worth doing. The
   speed half has no measured lever left in it. Renaming the milestone to match what it can
   deliver would be more honest than keeping a name the evidence does not support.
5. **Fix the exit gate.** It reads "a `--since`-scoped run … inside the stated time envelope",
   and `--since` is scheduled M3. Given §4, a speed-based exit gate for M2 cannot be met at
   all. Replace it with an honesty gate — for example, that the fingerprint oracle's soundness
   claims survive the R2-2 refinement reproduction — and move the inner-loop demonstration to
   the milestone that contains the levers that could achieve it.
6. **File the upstream ask.** Reduce Terraform core's per-run-block evaluation cost and offer
   a `-verbose` variant that omits `provider_schemas`. The earlier provider-process-reuse
   request is withdrawn under the corrected model in §4. The open asks are tracked in
   [the product design](../design/product-design.md#13-open-questions).
