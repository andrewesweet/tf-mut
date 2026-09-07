# ADR-0003: Synchronous immutable result facts

Status: accepted  
Date: 2026-09-07

## Decision

Cross-context handoffs return immutable, context-owned result facts synchronously.
The CLI and engine are synchronous, so a classified population, verified or
refuted suggestion, or characterised suite is returned as a value and projected
into the publication DTO. Facts are not persisted, dispatched, subscribed to, or
replayed.

An event bus, dispatcher, or event store becomes worth reconsidering only when an
actual independent asynchronous consumer exists. Until then, that infrastructure
would add delivery, ordering, and failure modes without a consumer that needs it.

## Alternatives considered

An event bus, dispatcher, or event store was rejected for the current synchronous
CLI because it would be infrastructure without an independent asynchronous
consumer. Shared mutable report values were also rejected as the handoff contract:
named immutable result facts make each context's conclusion explicit before
publication.

## Evidence

This records the DDD-5 decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following the event-infrastructure recommendation in the [review #85](https://github.com/andrewesweet/tf-mut/issues/85). The consumer condition is explicit so this decision reopens on evidence rather than fashion.

