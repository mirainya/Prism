# Gateway V2 Architecture

This document describes the implemented Prism v2.0.0 runtime. The complete
domain invariants and database contract are defined in
[`specs/2026-09-04-unified-gateway-catalog-billing-architecture.md`](specs/2026-09-04-unified-gateway-catalog-billing-architecture.md).
Current configuration behavior is defined by
[`specs/2026-09-15-prism-configuration-simplification.md`](specs/2026-09-15-prism-configuration-simplification.md).

## Runtime shape

Prism is a modular monolith. Protocol boundaries, catalog selection, upstream
execution, accounting, and delivery are separate Go packages, while operations
that must be atomic share a MySQL transaction.

Synchronous requests use this path:

```text
HTTP handler
  -> downstream codec or capability adapter
  -> canonical request
  -> catalog and SKU selection
  -> route, offering, transport, and credential selection
  -> Call and Attempt lifecycle
  -> upstream transport
  -> settlement and canonical response
```

Background Responses and video requests persist their intent before any
external side effect:

```text
HTTP handler
  -> Call + Attempt + AsyncExecution + Reservation + Outbox transaction
  -> SQL worker lease
  -> submit / query / recover
  -> terminal state + settlement + result delivery transaction
```

Workers use the database as the durable queue. Redis is not an execution queue.
An uncertain submission remains recoverable or requires manual review; it is
not silently submitted again.

## Module ownership

- `internal/gateway/canonical`: protocol-neutral requests, responses, events,
  usage, errors, and provider state.
- `internal/gateway/codec`: downstream Chat, Responses, and Messages codecs.
- `internal/gateway/adapter`: image and video operation adapters.
- `internal/gateway/catalog`: active catalog and selector rules.
- `internal/gateway/routing`: route, offering, credential, and slot selection.
- `internal/gateway/transport`: conversational upstream protocols.
- `internal/gateway/engine`: synchronous execution, Attempt lifecycle, and
  accounting orchestration.
- `internal/gateway/responses`: Responses resources and background execution.
- `internal/gateway/runtime`: Outbox consumers, callbacks, recovery, retention,
  and media workers.
- `internal/gateway/repository`: typed SQL operations and transaction boundaries.
- `internal/gateway/security`: Payload keyrings, HMAC identities, encrypted
  blobs, and legacy credential decryption for compatibility.
- `internal/gateway/delivery`: result reference and managed-copy policies.
- `internal/gateway/catalogsource`: catalog discovery and import workers.

## Persistent facts

The unified runtime writes only the normalized `gw_*` model:

- catalog: releases, models, operation contracts, SKUs, products, transports,
  offerings, routes, sell rates, and cost plans;
- credentials: channels, pools, plaintext API Keys, purpose grants, and
  request/task slots; legacy encrypted versions remain readable during migration;
- execution: API Calls, Attempts, request logs, encrypted payloads, resources,
  async executions, and Outbox events;
- billing: accounts, budget windows, reservations, ledger transactions,
  settlement events, and upstream cost events;
- delivery: media assets, result deliveries and sources, callback deliveries,
  and immutable state events;
- control plane: catalog discovery, evidence review, runtime state, and
  readiness facts; old deployment records remain only for compatibility.

Large request bodies, prompts, callback payloads, signed URLs, and upstream IDs
are not stored in list projections. Sensitive recoverable values are held in
bounded encrypted blobs; list queries read only metadata and safe summaries.

Legacy `api_calls`, `tasks`, `ai_responses`, `gw_channels`, `gw_abilities`, and
related tables are migration sources only. New requests do not write them.

## Catalog and routing rules

1. The active catalog release resolves the public operation and model.
2. The normalized request selects exactly one public SKU and user price.
3. Routing selects an eligible offering without changing the public SKU price.
4. The Attempt fixes the request's SKU, product transport, offering, cost plan,
   and credential configuration before dispatch.
5. A credential slot is acquired for each external request; task-scoped slots
   remain held until the upstream task is known to have ended.
6. Provider state is reused only within the scope declared by its product
   transport. Prism does not synthesize another provider's proof.
7. Cancellation is exposed only when the selected product transport declares a
   supported upstream cancellation action.

Admin edits update the active catalog in one transaction with `config_version`
concurrency checks. A successful edit affects the next request; requests that
already started keep their fixed configuration.

## Runtime readiness

The control plane can start without an active catalog, but the unified data
plane remains unavailable until migrations, Payload keys, commercial facts,
and the active catalog are valid. Credential KEK/HMAC are required by the data
plane only while an active or draining credential has no plaintext Key and
still depends on a legacy encrypted blob. Deployment generations and member
proofs are not runtime prerequisites.

Startup never applies migrations automatically. Operators use `prism migrate`
commands during a stopped, backed-up deployment. The detailed sequence and
verification commands are in
[`unified_gateway_operations.md`](unified_gateway_operations.md).
