# ADR-0005: In-process domain event dispatch

Status: accepted  
Date: 2026-10-11  
Supersedes: [ADR-0003](0003-synchronous-immutable-result-facts.md)

## Decision

Each conclusion a context reaches is published as a named domain event to a
small in-process dispatcher in the application layer (`internal/engine/events.go`),
and the code that used to call the next context directly subscribes to it instead.

The events are values with unexported fields, raised only by the engine, and every
subscriber receives its own copy. Their payloads are the contexts' own values, not
new DTOs:

| Event | Payload | Raised by | Subscribers, in delivery order |
| --- | --- | --- | --- |
| `MutantEvaluated` | the row (with its telemetry record), `oracle.Classified`, the operational failure | each execution worker | the per-mutant report projection; the progress line |
| `PopulationClassified` | `oracle.Population` | `finish` | the report projection's aggregates; the suggestion leg (suggest only); the acceptance gate |
| `SuggestionProposed` | `suggest.Candidate` | the suggestion leg, dry run | the report projection |
| `SuggestionVerified`, `SuggestionRefuted`, `SuggestionSkipped` | `suggest.Suggestion` | the suggestion leg | the report projection |
| `VerificationCosted` | the verification cost ledger | the suggestion leg, once, last | the report projection |
| `SuiteCharacterised` | the concluded `report.Characterisation` | characterisation, at either conclusion | the report projection |

The events live in the engine, not in the contexts that reached the conclusions.
Two payloads are publication DTOs (the per-mutant row and the characterisation
block), and the context-boundary lint rule forbids every context from importing
`internal/report`; the application layer raises each event as it receives each
context's conclusion, which keeps the dependency direction in
[CONTEXT.md](../../CONTEXT.md) unchanged.

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
  For `PopulationClassified`, the order is the one `finish` called its steps in:
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
existed, and that consumer landed in #199. The change is behaviour-preserving
across 27 CLI cases covering run, preview, suggest, characterise and curate. On each
case, the JSON, SARIF, mutation-testing-elements and JUnit outputs, the exit code,
the files written and the baseline file are identical to master. The only
differences are the three fields that also differ between two runs of master: a
characterise input digest, a random value in the volatile fixture and a UUID.
