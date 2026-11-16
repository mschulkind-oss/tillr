---
title: SQLite contention repair plan
status: owner-approved direction, implemented pending review
---

# SQLite contention repair plan

The owner's recovery authorization and assigned direction bind this repair. Behavior
comes from [the lifecycle invariant](../../../tools/tillr-land/README.md#state-invariants);
the tree decides symbol locations. This is a narrow repair, not a new landing policy.

## Constraints

1. Observe a failing regression before editing production: stall Git preparation and
   attempt an independent heartbeat write. Close must be able to finish while preparation
   remains stalled; its release must refuse the now-closed item without Move.
2. Prepare expensive Git and range work under land.lock, outside SQLite transactions.
   Recheck closed/resolved disposition, relevant history and trunk expectations inside the
   final guarded writer. Keep Move and receipt excluded against Close.
3. Preserve explicit error, cancellation, refusal and retention behavior. Never replay an
   external Move after a receipt failure; never hide a heartbeat failure.
4. If removing redundant migration, prove successful initialization in the same Loop,
   not by shared SchemaVersion. Keep every owner's initialization and legacy copy semantics.
5. Observe remaining writer and Git spans without generating new writes. Do not enlarge
   busy_timeout, alter admission/closed-item policy, manipulate live state or commit before
   independent review.

## Verification order

Run the [named regressions](qa.md#regressions), then full tests and vet for the shared
store, lander, item utility and drain. Mirror pack sources and lint the tillr pack.
[QA](qa.md) records actual commands and outcomes, not an assumed restart cure.
