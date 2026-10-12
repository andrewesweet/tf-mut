# ADR-0005: In-process domain event dispatch

Status: accepted  
Date: 2026-10-11  
Supersedes: [ADR-0003](0003-synchronous-immutable-result-facts.md)

## Decision

Grading and characterisation publish named domain events through an in-process
dispatcher in the application layer (`internal/engine/events.go`). The report
projection and the downstream consumers subscribe to these events.

The engine alone raises these values, whose fields are unexported. Dispatch passes
a value copy to each subscriber, with referenced payload data treated as immutable.
Context-owned payloads pass through unchanged. Two events carry publication DTOs,
as detailed below:

| Event | Payload | Raised by | Subscribers, in delivery order |
| --- | --- | --- | --- |
| `MutantEvaluated` | the row (with its telemetry record), `oracle.Classified`, the operational failure | each execution worker | the per-mutant report projection; the progress line |
| `PopulationClassified` | `oracle.Population` | `finishReport` | the report projection's aggregates; the suggestion leg (suggest only); the acceptance gate |
| `SuggestionProposed` | `suggest.Candidate` | the suggestion leg, dry run | the report projection |
| `SuggestionVerified`, `SuggestionRefuted`, `SuggestionSkipped` | `suggest.Suggestion` | the suggestion leg | the report projection |
| `VerificationCosted` | the verification cost ledger | the suggestion leg, once, last | the report projection |
| `SuiteCharacterised` | the concluded `report.Characterisation` | characterisation, at either conclusion | the report projection |

The events live in the engine because two payloads are publication DTOs: the
per-mutant row and the characterisation block. See the
[dependency policy](../../CONTEXT.md#dependency-direction) for the boundary these
types must respect.

The per-mutant report projection copies the row's provenance into report-owned
storage before baseline labelling mutates it. `TestBaselineLabellingPreservesPublishedEvent`
in `internal/engine/events_internal_test.go` holds the published payload unchanged
while the report receives its baseline label.

ADR-0003 named three costs of a dispatcher. This design settles each one:

- **Delivery.** Dispatch is in-process and synchronous. `publish` calls each
  subscriber on the publisher's goroutine and returns once every subscriber has
  reacted. There is no goroutine-based bus, no queue, no buffer, no persistence or
  replay, and no new dependency. Execution workers publish under the lock that
  already guarded the per-mutant slots, so subscribers see one result at a time.
- **Ordering.** Delivery is in subscription order, and the order is part of the
  contract. A subscriber may publish, and the nested events are delivered in full
  before the outer delivery continues: the suggestion leg's outcomes reach the
  projection in survivor order before the acceptance gate sees the population.
  For `PopulationClassified`, `finishReport` subscribes consumers in this order:
  aggregates, then suggestions, then the baseline gate. So no reviewed verdict and
  no side effect moves.
- **Failure modes.** The first subscriber error stops delivery and is returned to
  the publisher, which fails the command with it unchanged. This is how the
  replaced call sequence failed. A suggestion leg refused for a stale survivor
  still exits 2 before the baseline writer runs, and
  `TestASuggestionLegFailureWritesNoBaseline` holds that ordering through the
  engine seam.

## Alternatives considered

Keeping ADR-0003 kept the handoff as returned values. The DDD review scored
cross-aggregate domain events 0.0 under that decision, because the named result
facts it promised were never minted and the population handoff crossed the
boundary as the publication DTO. The per-mutant telemetry and progress work (#199)
supplied the consumer the review was waiting for: a per-mutant observer that is
independent of the projection. That consumer is synchronous, so the design keeps
ADR-0003's objection to asynchronous infrastructure and drops only its objection
to dispatch.

A goroutine bus or channel queue was rejected. It would make delivery order
depend on scheduling, which would reopen the ordering questions the reviewed
verdict order settles. It would also detach a subscriber's failure from the
command that has to report it.

Exporting the events from each context was rejected, because two payloads are
publication DTOs the contexts may not import.

## Evidence

The review that reopened ADR-0003 scored row 5, "Domain events for cross-aggregate
flow", at 0.0. Its decision 1 asked for the reversal once the per-mutant consumer
existed, and that consumer landed in #199.

The comparison used a tf-mut binary built from master commit `b6dbf16` and this
branch's binary. It covered 27 CLI cases across `run`, `preview`, `suggest`,
`characterise` and `curate`, each on a fresh copy of an engine fixture. It compared
the JSON report, SARIF, mutation-testing-elements and JUnit outputs, exit codes,
the list of files each run wrote in the module directory (excluding `.terraform`),
and the written `.tf-mut-baseline.json`.

Before comparison, it masked only these values:

- In the JSON report, the top-level `telemetry` block, every mutant's `telemetry`
  block and `baseline.duration_ms`.
- In JUnit, every `time` and `timestamp` attribute.
- In mutation-testing-elements, `framework.dependencies`.

The JSON, SARIF, mutation-testing-elements and baseline files were compared with
keys sorted (`jq -S`). After masking, three values differed: the
`characterise --write` case's `input_digest`, a randomly generated value in the
volatile fixture's mutant delta, and a UUID in the suggest-count case's mutant
delta. The same three also differ between two runs of the master binary. All other
compared values matched.
