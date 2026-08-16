# The cost model behind M2's speed levers — measured

Issue [#19](https://github.com/andrewesweet/tf-mut/issues/19), the pre-work M2 was blocked on.
Three experiments, run against `internal/engine/testdata/aws-mocked` through the engine's own
execution path, to establish what the planned speed levers would actually buy before either is
designed.

The short version: **run-block file splitting is a tax, not a lever, and the milestone as
scoped has no viable speed lever for real-provider modules.** The evidence is below; the
consequences for the roadmap are in §5.

## Method

Hardware: 13th Gen Intel Core i5-13420H, 12 logical cores, WSL2 on Linux 6.6. Terraform
v1.15.8. Fixture: ten AWS resources behind `mock_provider "aws"`, `hashicorp/aws` 6.x at
roughly 840 MB installed.

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 go test -tags=integration ./internal/engine/ \
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
run blocks costs 678%. Everything is the process.

### What that does to the crossover

Splitting is only worth its own cost if selection then executes *k* of the *n* files. Splitting
wins iff

```
(k − 1) · F  <  (n − k) · M          F ≈ 1.6 s,  M ≈ 0.012 s
```

At **k = 1** — perfect selection, one run block of eight — the saving is `7 × 0.012 = 0.08 s`
against a 1.70 s baseline: **5%**. At **k = 2** splitting already loses, by 1.5 s. There is no
*n* at which the arithmetic becomes favourable, because `F/M` is between 134 and 162 and *k* is
bounded below by one.

**Upper bound on splitting's total benefit for the M1 population:** 40 mutants × 8 run blocks ×
12 ms = **3.8 s out of 128 s, or 3%** — and only if selection were perfect, which §3 shows it
is not.

This is measured on mocked plan-mode runs, which is the design's stated target shape. The
result would change if a run block were expensive relative to an invocation: splitting wins
whenever `M > F`, that is, whenever a single run block costs more than a second and a half.
Apply-mode suites against slow providers may reach that. Mocked plan-mode suites against a
large schema do not, and are not close.

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

## 4. Where the time actually goes

Putting the three together, the per-mutant cost of a mutation run against a real provider is:

```
cost(mutant) ≈ 1.6 s  +  0.012 s × run blocks  +  sandbox materialisation
```

The first term dominates and is a constant. It is Terraform starting an 840 MB provider plugin.
That number is not reachable by anything inside this tool: not by splitting, not by selection,
not by parallelism (M1 measured 1.08× from one job to eight), not by two-phase execution.

The floor for a full run is therefore `mutants × 1.6 s ÷ effective parallelism`. M1's measured
128 s for 40 mutants is 2× that floor, so the harness overhead above Terraform is already
small. **There is no factor of two available in M2's scope.** The remaining levers all reduce
the *mutant count* rather than the per-mutant cost — `--since`, the incremental cache,
sampling, tier selection — and every one of them is scheduled M3.

## 5. Consequences for the M2 spec

Stated as recommendations. The spec author disposes of them.

1. **Drop run-block file splitting.** Its ceiling is 3% under perfect selection and its cost
   is 7.8× when applied naively. It was adopted from round-one M7 as "the primary viability
   lever on real modules"; that claim is withdrawn and this document is the reason. Keeping it
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
6. **File the upstream ask.** A `terraform test` that reused one provider process across run
   blocks would be worth more than every lever in this design put together. So would a
   `-verbose` variant that omits `provider_schemas`. Both are already noted in
   `product-design.md` §13 open question 7; this document is the evidence for them.
