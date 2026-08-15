# 0002. Operate is a first-class stage

Date: 2026-08-15
Status: accepted

## Context

The flywheel ends at "binary on a host". Nothing observes the running
artifact, so change-failure rate and MTTR — half of the DORA set the Learn
stage publishes — are fed by remembering to label a GitHub issue `incident`.
By this project's own rule, a process that depends on unsustained discipline
should be deleted or automated. At Capital One the gap was invisible because
tenants ran the artifact and their telemetry was someone else's job. Solo,
nobody is watching unless something watches.

## Decision

Operate becomes the sixth stage, between Release and Learn. It owns SLO
definitions (`docs/slo.yml`), a prober, incident open/close lifecycle, and
post-deploy smoke with rollback. Incidents are filed and closed by machine;
`tools/dora` reads them exactly as it does today.

## Consequences

CFR and MTTR become measured rather than remembered, which makes every
published Learn number defensible. `/healthz` reporting a version lets Learn
attribute a failure to a release. Harder: a sixth stage is a sixth thing to
keep alive, and it is the first stage that runs continuously rather than on
an event — so it declares a standing cost under ADR 0001 and gets cut if it
exceeds it. Explicitly not in scope: paging, on-call, dashboards as a product.
