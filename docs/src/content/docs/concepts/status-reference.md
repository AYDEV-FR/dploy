---
title: Status & Conditions Reference
description: Every status field, phase and condition the three Dploy CRDs publish, who writes them, and where the contract is thin.
---

What a controller writes is the contract everything else reads — the web UI, the CTFd plugin,
`kubectl`, your alerts. This page enumerates all of it, and is deliberately explicit about the
places where a combination is reachable but unspecified.

![Status and condition map across the three CRDs: a DployTemplate publishes SourceReady, which gates whether instances may apply a HelmRelease; a DployInstance moves through Pending, Provisioning, Ready or Available and Claimed, Failed, and Expiring, carrying a Ready condition and a health string; a DployInstanceClaim moves Pending to Bound, Rejected or Expired and mirrors the bound instance. Three gaps are marked: health is not reset on Failed or Expiring, and a Bound claim has no exit when its instance fails.](/diagrams/dploy-status-map.svg)

## DployTemplate

No phase. A template is configuration, and the only thing the controller decides about it is
whether its chart resolves.

| Field | Written by | Meaning |
|-------|-----------|---------|
| `poolAvailable` | template controller | warm, unclaimed members that are `Available` |
| `poolClaimed` | template controller | pooled instances currently claimed |
| `poolTotal` | template controller | **every** instance of the template, pooled or not |
| `observedGeneration` | template controller | last spec generation reconciled |
| `conditions` | template controller | see below |

| Condition | Status | Reasons |
|-----------|--------|---------|
| `SourceReady` | `True` | `ChartResolved`, or the reason source-controller reports |
| | `False` | `ChartNotResolved`, `ChartNotConfigured`, `ChartProbePending`, `SourcePending`, `FluxUnavailable` |
| | *absent* | the template has not been reconciled yet |

`SourceReady` gates every `HelmRelease`: no instance applies one until it is `True`. A *known*
failure also stops a pool filling; an unknown verdict does not.

:::caution[`poolTotal` counts more than a pool]
On an `on-demand` template the three `pool*` counters are still written, and `poolTotal` counts
the on-demand instances. The name says pool; the number does not.
:::

## DployInstance

| Field | Written by | Meaning |
|-------|-----------|---------|
| `phase` | instance controller | see the phase table |
| `uuid` | instance controller | short identifier used in names and URLs |
| `namespace` | instance controller | the workload namespace |
| `url`, `connectionType`, `connectionMessage` | instance controller | rendered connection target |
| `engine`, `engineRef` | instance controller | always `Flux`, and the `HelmRelease` name |
| `health` | instance controller | `Healthy`, `Progressing`, `Degraded` — a free-form string, not an enum |
| `expiresAt` | instance controller | TTL deadline, absent while an unclaimed pool member waits |
| `conditions` | instance controller | a single `Ready` condition |
| `extendCount` | **nobody** | declared in the API, never written |

`status.sync` used to sit in this table for the same reason and has been removed from the API.

| Phase | Reached when |
|-------|--------------|
| `Pending` | accepted, first reconcile has not finished |
| `Provisioning` | Flux is installing — **or** the template has not yet proven its chart resolves |
| `Ready` | on-demand instance, release healthy |
| `Available` | pooled member, unclaimed |
| `Claimed` | pooled member bound to a claim |
| `Failed` | a reconcile gave up |
| `Expiring` | past TTL or being deleted, teardown under way |

`Ready` condition reasons come straight from the `HelmRelease` when one exists, and otherwise from
the operator: `TemplateNotFound`, `SourceError`, `HelmReleaseError`, `NamespaceError`,
`ValuesTemplateError`, `ValuesYAMLError`, `ConnectionURLTemplateError`,
`ConnectionMessageTemplateError`, or the template's `SourceReady` reason while the gate holds.

:::note[`health` follows the phase]
`Failed` sets `health` to `Degraded` and `Expiring` sets it to `Progressing`. This was not always
true: both transitions used to write `phase` and leave `health` behind, so a dead environment kept
reading `Healthy` and the claim mirrored that. `phase` and the `Ready` condition remain the
authoritative pair; `health` is a summary for display.
:::

## DployInstanceClaim

| Field | Written by | Meaning |
|-------|-----------|---------|
| `phase` | claim controller | see the phase table |
| `instanceRef`, `uuid` | claim controller | the bound instance |
| `connectionURL`, `connectionType`, `connectionMessage` | claim controller | mirrored from the instance |
| `instancePhase`, `health` | claim controller | mirrored from the instance |
| `boundAt`, `expiresAt` | claim controller | the TTL clock, anchored at the binding |
| `ttlSeconds`, `maxTTLSeconds` | claim controller | granted lifetime and the ceiling extensions may reach |
| `conditions` | claim controller | `Bound` and `Ready` |

| Phase | Reached when | Terminal? |
|-------|--------------|-----------|
| `Pending` | accepted, holding nothing — waiting on a warm member (`PoolExhausted`) or on the template to free a slot (`TemplateAtCapacity`) | no |
| `Bound` | an instance is bound and owned | no |
| `Rejected` | unsatisfiable as written, or the bound environment stayed `Failed` | until the spec changes |
| `Expired` | the TTL ran out and the environment was torn down | yes, fully |

| Condition | Status | Reasons |
|-----------|--------|---------|
| `Bound` | `True` | `Bound` |
| | `False` | waiting: `PoolExhausted`, `TemplateAtCapacity` · refused: `QuotaExceeded`, `TemplateNotFound`, `TemplateDisabled`, `InstanceFailed`, `Expired` |
| `Ready` | mirrors the bound instance's `Ready` | `Provisioning` until the instance reports otherwise; `Expired` at the end |

:::note[At capacity a claim waits; over quota it is refused]
The difference is what the owner can act on. A quota is theirs to free by releasing something, so
`QuotaExceeded` is a refusal. `maxSize` is a property of the template that no single claimant
controls, so `TemplateAtCapacity` parks the claim as `Pending` and it binds by itself once a slot
opens. Rejecting there would leave the claim dead after the template emptied.
:::

:::note[A failed environment releases its claim]
When the bound instance reaches `Failed`, the claim is `Rejected` with reason `InstanceFailed` and
the instance is released — which frees the owner's quota immediately. The owner re-requests when
they are ready. Previously the claim stayed `Bound` on the dead instance and held the quota until
its TTL ran out, with the `Ready` condition as the only signal.
:::
