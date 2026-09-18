# ADR 0007: Immutable evidence remains the authority boundary for research improvement

## Status

Accepted, 2026-09-18.

## Decision

The research-improvement loop is governed as one chain: a point-in-time
dataset snapshot feeds a candidate experiment; sandboxed fold-local fitting
and tuning produce frozen out-of-sample evidence; multiple-testing, capacity,
risk, and baseline gates remain immutable; and a human may then authorize
shadow and paper transitions. Neither a proposal artifact, an experiment job
summary, nor a successful metric can advance authority on its own. Direct live
submission remains separately fenced and requires a later explicitly approved
limited-live transition.

Every Stage 08 parity-acceptance transition stores the exact immutable parity
population ID as well as the policy ID. The population includes the bounded
decision contexts, point-in-time dataset and universe versions, and its source
cutover attempt. This makes the dual-run denominator reproducible after the
system moves beyond the shadow stage.

Operational status reads and verifies immutable Stage 07 manifests/evidence,
then reports named failed validation gates rather than trusting a mutable job
summary. Rule-only experiments explicitly record model version `none` in their
Stage 08 observation context; learned models remain optional and quarantined.

## Consequences

An old parity-acceptance transition without a population binding is not
sufficient evidence for future authority initialization. It must be rolled
back to a safe stage and repeated under the bound-population contract. This is
a deliberate fail-closed migration boundary, not a claim that past parity was
invalidated by new metrics.

The architecture does not establish real historical coverage, exchange fill
behavior, backup restore equivalence, elapsed shadow/paper stability, or
profitability. Those require independently collected operational evidence.
