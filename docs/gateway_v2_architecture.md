# Gateway V2 Architecture

## Goal

Gateway V2 has one execution engine for every conversational protocol. Protocol-specific
code lives only at HTTP boundaries.

```
HTTP -> downstream codec -> canonical request -> engine -> router -> transport -> upstream
HTTP <- downstream codec <- canonical response/event <- engine <- transport <- upstream
```

## Modules

- `internal/gateway/canonical`: protocol-neutral request, response, event, usage, errors,
  semantic features, and provider options.
- `internal/gateway/codec`: OpenAI Chat, OpenAI Responses, and Anthropic Messages wire codecs.
- `internal/gateway/transport`: OpenAI Chat, OpenAI Responses, Anthropic Messages, Google
  GenerateContent, and Volcengine Responses v3 upstream transports.
- `internal/gateway/engine`: routing, retries, billing, persistence, files, and stream lifecycle.
- `internal/gateway/routing`: selects an ability plus a transport. It never decides an HTTP path.

## Persistence

- `gw_ability_transports` stores each ability's enabled upstream transports and probe status.
- `gw_route_states` stores circuit state by key, public model, and transport.
- Responses, conversations, and request logs store the selected upstream transport.
- Existing Responses JSON remains in its current OpenAI Responses wire format during migration.

## Selection Rules

1. A codec emits semantic request requirements.
2. A transport returns `exact`, `converted`, or `unsupported` for that canonical request.
3. The router selects an enabled ability/transport that supports every required feature.
4. Exact paths rank above converted paths. Lossy conversion is never allowed.
5. Provider state is reused only when key and transport both match.

## Migration

Existing `gw_channels.protocol` is migration metadata only. New runtime code reads
`gw_ability_transports`. Existing capability JSON is read compatibly, then materialized by a
data migration. The release removes the legacy Chat and Responses execution pipelines rather
than leaving two active engines.
