# Gateway V2 Architecture

> This document describes the currently implemented Gateway V2. The target catalog, credential, async execution, and billing architecture is defined by `docs/specs/2026-09-04-unified-gateway-catalog-billing-architecture.md`; where they differ, the target specification is authoritative.

## Goal

Gateway V2 has one execution engine for every conversational protocol. Protocol-specific
code lives only at HTTP boundaries.

```
HTTP -> downstream codec -> canonical request -> engine -> router -> transport -> upstream
HTTP <- downstream codec <- canonical response/event <- engine <- transport <- upstream
```

## Modules

- `internal/gateway/canonical`: protocol-neutral request, response, event, usage, errors,
  semantic features, provider options, tool namespaces, and opaque provider-issued continuation proofs.
- `internal/gateway/codec`: OpenAI Chat, OpenAI Responses, and Anthropic Messages wire codecs.
- `internal/gateway/transport`: OpenAI Chat, OpenAI Responses, Anthropic Messages, Google
  GenerateContent, and Volcengine Responses v3 upstream transports.
- `internal/gateway/engine`: execution plans, retries, billing reservation/settlement, call lifecycle,
  delivery state, and streaming.
- `internal/gateway/routing`: selects an ability plus a transport. It never decides an HTTP path.

## Persistence

- `gw_ability_transports` stores each ability's enabled upstream transports and probe status.
- `gw_route_states` stores circuit state by key, public model, and transport.
- Responses, conversations, and request logs store the selected upstream transport.
- Terminal Chat, Responses, and Messages calls are projected into ordered canonical conversation
  turns, including `store=false` Responses. `call_id` is the idempotency key, while explicit Prism
  conversation IDs, `previous_response_id`, and unique completed-history prefixes provide
  continuation hints.
- All three downstream endpoints accept `X-Prism-Conversation-ID` for explicit continuation; Chat
  also accepts the `conversation_id` body field. A valid explicit ID is echoed in the response
  header; implicit matches and newly created conversations do not emit this header.
- `api_calls` stores one downstream invocation; `api_call_attempts` stores every concrete upstream
  execution. `api_call_payloads` optionally stores bounded, redacted, expiring raw downstream and
  upstream HTTP payloads; this capture is independent of the canonical conversation projection.
- `ai_response_idempotency_cache` provides a 24-hour replay window independent of `store=true`.
- `balance_entries`, `api_access_logs`, and `audit_events` separate financial, HTTP access, and
  state-change histories from model execution records.
- Existing Responses JSON remains in its current OpenAI Responses wire format during migration.

Foreground and background executions use leases. Background Responses persist a result checkpoint
before finalization, so recovery can finish accounting without repeating a successful upstream call.
Calls become successful only after the downstream codec and server-side response write complete.

## Selection Rules

1. A codec emits semantic request requirements.
2. A transport returns `exact`, `converted`, or `unsupported` for that canonical request.
3. The router selects an enabled ability/transport that supports every required feature.
4. Exact paths rank above converted paths. Lossy conversion is never allowed.
5. Provider state is reused only when key and transport both match.
6. Provider proofs retain their issuing provider. A transport may replay only its own proof;
   Prism does not synthesize signatures for another provider.
7. Provider state crosses the Responses boundary in standard reasoning items with versioned
   `encrypted_content` envelopes. A Gemini function-call carrier is restored by a globally unique
   `call_id`; carrier position and adjacency are not identity signals.
8. A Google Part carrier uses a unique `TargetID` to restore a proof to its original single-Part
   message. The same envelope family preserves opaque Anthropic `redacted_thinking` data. Each
   proof is validated and replayed only to its issuing provider; bypass signatures are never injected.
9. The Google transport currently requires a Responses downstream for every request because Chat
   and Messages have no portable field for signatures attached to reasoning, function calls, text,
   or media Parts. Responses reasoning controls are not mapped to Google generation controls.

## Migration

Existing `gw_channels.protocol` is migration metadata only. New runtime code reads
`gw_ability_transports`. Existing capability JSON is read compatibly, then materialized by a
data migration. The release removes the legacy Chat and Responses execution pipelines rather
than leaving two active engines.
