# 0001. Scope is a budget, not a ban

Date: 2026-08-15
Status: accepted

## Context

The v1 spec ruled out multi-repo orchestration, agent fleets, and inter-agent
protocols as "the C1 version", with the stated reason that the personal one
"must stay cheap enough to actually use". That was written when the flywheel
had one instance; it now has two templates and three instances, and the same
reasoning that once argued against orchestration now argues for it — five
repos are no longer tractable by hand. The non-goal named a category but
justified a cost.

## Decision

Replace the category ban with an explicit cost test. Every stage and every
epic declares a standing cost in minutes and dollars per month. Anything over
its declared budget for two consecutive retros gets deleted or automated —
the same rule already applied to bypassed gates, applied to scope.

Fleets and inter-agent protocols are therefore in scope, priced rather than
prohibited.

## Consequences

The flywheel can grow with the number of projects instead of capping at one.
Harder: "cheap enough" is a judgement where "never" was a rule, so the budget
must be written down and checked in the retro or it decays into permission to
build anything. The binding constraint is stated up front: review capacity,
not compute. A fleet that outruns one human's review morning has failed its
budget regardless of what it cost to run.
