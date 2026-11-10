# Gateway Interfaces

HTTP adapters belong in this package. The current gateway keeps its compatibility HTTP adapter in the parent package while the migration is performed incrementally; new endpoints should be added here rather than to the domain or infrastructure packages.

### Request diagnostics

Search container logs or persisted request logs by `request_id`. The
`gateway.request.completed` event includes `outcome=success|error` and the final
error code. `status` remains the HTTP status actually sent; `result_status`
matches the effective failure status recorded in usage. A stream can therefore
have `status=200 result_status=502 outcome=error`, or `result_status=504` on a
read timeout. Do not use HTTP status alone to count successful streaming turns.
The request trace and failure detail panels prioritize the explicit outcome and
show both statuses, the failed field and the generated reason.

Conversion, request validation, provider policy/configuration, quota settlement
and response delivery failures record generated diagnostics in both a failure
event and the completion event: `failure_stage`, `error_param`, and
`error_reason`. Conversion diagnostics identify the exact unsupported field;
provider policy diagnostics distinguish missing/inactive bindings, allowlist
denials and database lookup failures. These details use the shared logging sink,
so the request log viewer receives them without a schema migration.

For example, a missing model produces
`failure_stage=request_conversion error_param="model" error_reason="expected a non-empty string"`.
The failure event also includes the selected provider name and target protocol.
Generated diagnostics exclude body/header values and raw parser errors, redact
caller/provider credentials, bound field lengths and remove control characters.
Cross-protocol stream diagnostics also retain `stream_frame`, `stream_event`,
and a valid `stream_block_index`/recognized `stream_block_type` when present.
Unsupported delta fields report their exact paths. Transport interruptions use
`failure_stage=response_read`, upstream SSE error envelopes use `upstream_stream`,
and client write failures use `response_write`; these are not conversion errors.
The first terminal failure remains the completion cause if delivering its error
also fails. Rejected provider candidates are cleared when a later policy check
permits a route.

Upstream transport events retain the failure phase (DNS, connect, TLS, waiting
for headers), while completion summaries include `upstream_failure_reason`
(such as timeout or cancellation), safe upstream error details, HTTP status and
upstream request ID when available, including HTTP 200 responses whose bodies
fail later. Successful retries replace earlier upstream evidence. Earlier requests cannot recover conversion
details that were never logged.

Gateway-generated 429 responses expose `error.code`, `error.reason`, and
`error.retryable`. `X-RateLimit-Reason` mirrors the reason; `X-RateLimit-Limit`
and `X-RateLimit-Remaining: 0` describe the exhausted policy when its limit is known.

- Concurrent session caps: `rate_limited`, with `global_concurrency`, `organization_concurrency`, or `key_concurrency`. Session leases are released when the request ends and expire after Redis failures/crashes.
- Configured windows: `quota_exceeded`, with `quota_5h`, `quota_1d`, or `quota_7d`.
- Key budgets: `quota_exceeded`, with `total_tokens`, `total_amount`,
  `daily_tokens`, `daily_amount`, `weekly_tokens`, `weekly_amount`,
  `monthly_tokens`, or `monthly_amount`.
- Upstream rejections: `rate_limited` with `upstream_rate_limit` by default;
  explicit provider budget errors (`insufficient_quota`, `quota_exceeded`,
  `billing_hard_limit_reached`) return `quota_exceeded` with `upstream_quota`.

Automatically resetting policies return `Retry-After` as remaining seconds,
rounded up to at least one second. Redis windows use the exhausted counter's
TTL; calendar budgets use UTC+8 midnight, Monday, or the first of the month.
If multiple policies in one check are exhausted, the longest blocker is reported.
Total and zero budgets require a budget change and return `retryable: false`
without `Retry-After`, because waiting alone cannot restore them.
Upstream `Retry-After` values (seconds or HTTP dates) are normalized to remaining
seconds. Upstream rate limits without a valid delay use one second as the minimum
backoff; explicit upstream quota errors without a reset delay are not retryable.

API-key inference dispatches by `api_format`. `openai_chat` and
`openai_responses` retain the existing OpenAI forwarding behavior.
`anthropic_messages` forwards POST `/v1/messages` and
`/v1/messages/count_tokens` directly with provider `x-api-key`,
`anthropic-version` (default `2023-06-01`) and client `anthropic-beta`.
`gemini` accepts POST `/v1beta/models/{model}:{action}` for `generateContent`,
`streamGenerateContent`, `countTokens`, `embedContent`, and `batchEmbedContents`;
stream generation requests use `alt=sse` upstream. Gemini clients may authenticate
with `x-goog-api-key` containing their BOS key. Upstream credentials always come
from the selected provider. Client `key` query parameters are never forwarded.

Native inference bodies retain unknown fields. Explicit model aliases rewrite
only the model (the URL model for Gemini); security policies still apply.
OpenAI Chat streaming requests always send `stream_options.include_usage=true`
upstream, including when the caller omits or disables it. Other stream options
and request fields are retained. The returned usage chunk is forwarded and its
input/output counts settle usage and token quotas. SSE response headers also
mark the usage record as streaming even when the caller omits an SSE `Accept`
header. This cannot reconstruct counts missing from historical responses.
For the resolved upstream model `Qwen3.8-27B`, HTTP `/v1/responses` maps
`reasoning.effort: "high"` to `"xhigh"`, and `/v1/chat/completions` applies the
same mapping to `reasoning_effort`. This accommodates clients such as Codex:
that model accepts `xhigh`, `medium` and `low`, but rejects `high`. Other effort
values and models pass through unchanged, including when model aliases are used.
Cross-protocol inference uses only the explicit conversion paths and capability
boundaries listed below. OpenAI model catalog endpoints retain the BOS catalog format; Gemini discovery uses native metadata.

Chat requests routed to an Anthropic Messages provider also accept native
`thinking` and `output_config` objects. BOS preserves the caller's mode, token
budget, effort, and display settings; the upstream validates model-specific
options. BOS never turns thinking off to make a conversion succeed. These
extensions apply only to Chat -> Anthropic, not to Gemini conversion.

Anthropic thinking text is returned separately as Chat `reasoning_content` (or
`delta.reasoning_content` in SSE). Signed and redacted thinking blocks are also
returned in an `anthropic_thinking` array on the assistant message. Each item is
`{"block":{"type":"thinking","thinking":"...","signature":"..."},"text_offset":0,"tool_offset":0}`;
redacted items instead contain `{"type":"redacted_thinking","data":"..."}`
inside `block`. Offsets count UTF-8 bytes of normal answer text and the number of
tool calls preceding the block. They preserve thinking placement when restoring
Chat's aggregated text/tool history. This extension contains no tool arguments.

For SSE, thinking text is delivered incrementally, and one complete
`anthropic_thinking` item is emitted when its upstream block finishes. Clients
must append these arrays across chunks, keep them on the assistant turn, and
return them unchanged with subsequent messages/tool results. BOS restores the
original provider signatures and opaque data; it never fabricates signatures.
If `reasoning_content` is returned too, it must match the preserved thinking text.
Clients that discard the extension cannot preserve signed thinking history;
nonempty reasoning text without its blocks is rejected with an actionable 400.
Incomplete/invalid thinking streams report a conversion error and do not emit
`[DONE]`. Usage continues to come from upstream token metrics.

Gemini and malformed upstream failures use protocol-specific error envelopes
with safe BOS messages. Valid Anthropic errors are preserved as described below;
429 classification and retry headers follow the rules above. Earlier
middleware failures retain the common BOS error envelope.

Anthropic `message_start`/`message_delta` usage is merged across SSE events.
Gemini usage includes `promptTokenCount`, `candidatesTokenCount`, and
`thoughtsTokenCount`; cache-read/write tokens use their configured model prices.
Cache tokens are included in input totals and are not counted twice for pricing.

Claude Code uses the native Anthropic API-key provider (`api_format`:
`anthropic_messages`). Set `ANTHROPIC_BASE_URL` to the BOS root (without `/v1`),
`ANTHROPIC_AUTH_TOKEN` to the BOS gateway key, and `ANTHROPIC_MODEL` to an
authorized Claude model. Both `/v1/messages?beta=true` and
`/v1/messages/count_tokens` retain system block ordering, signed/redacted thinking,
tool invocations/results, cache-control TTLs and future body fields. Tool arguments
still pass the existing authorization/security checks before delivery.

All `anthropic-*` request capability headers are forwarded, including version,
beta and workspace headers. Anthropic response headers, `request-id` and
`x-should-retry` are returned. Valid native Anthropic error bodies/statuses are
preserved for client capability recovery, except literal upstream/BOS credentials
are redacted. Malformed/oversized errors still use safe BOS error envelopes.
HTTP-date Retry-After headers are converted to seconds and 429 responses retain
BOS quota classification/backoff headers.

Anthropic streams preserve thinking/signature deltas and terminal events.
Validated tool blocks are delivered at `content_block_stop`; SSE pings pass even
while incomplete tool arguments await validation (keepalives may overtake held
tool events). Stream responses disable proxy buffering. The upstream client timeout
bounds the wait for response headers, not the full streaming turn; cancellation
of the downstream request closes the upstream. Streaming requests are marked as
streams in usage records even when the client omits an SSE Accept header.

Reference: https://code.claude.com/docs/en/llm-gateway-protocol

Gemini native inference uses the explicit `/v1beta/models/*modelAction` route.
The `{model}:{operation}` URL is parsed once before quota/security processing;
body/query `model` values cannot override it. Invalid model paths, operation
suffixes and mapped targets are rejected before reaching the upstream. Native
requests select only `gemini` API-format providers within existing organization
bindings and model allowlists, including when other protocols advertise the same
public model name.

Permissions, security-inspection metadata, rate-limit metadata and request logs
use the public URL model and operation. Rate-limit keys expose `Model` and
`Operation`; configured key/organization/global quotas still share their existing
buckets across models and operations. Upstream model aliases affect the URL only;
usage/audit records retain the public model, while pricing falls back to the mapped
upstream model's configured prices.

Only successful GET model-catalog requests are exempt from usage recording.
Gemini POST inference requests are recorded and settle token/amount quotas.
`countTokens` responses without usage metadata record the request without charging
generation tokens. Streaming is determined by `streamGenerateContent`, independent
of any body `stream` field, and uses the same header timeout/cancellation behavior
as native Anthropic streams.

GET `/v1beta/models` and `/v1beta/models/{model}` return native Gemini model
metadata from the authorized Gemini API-key upstreams. Aliases rewrite only the
resource name. Unsupported generation methods are omitted. Lists support
`pageSize` (1–1000, default 50) and `pageToken`; each listed model's metadata is
fetched from its upstream, so discovery requires upstream availability.

Native Gemini `functionCall` and `functionResponse` histories participate in
request inspection and correlation. JSON and SSE function calls use declared
`functionDeclarations`, `functionCallingConfig`, and existing tool policy and
safe audit metadata. Allowed native calls retain their bytes and thought
signatures. Blocked calls are replaced by non-executable text without parameters
or signatures. Malformed/partial tool arguments fail closed in blocking mode;
observe-only mode forwards original bytes and records inspection failures.

### Cross-protocol conversion

Messages → OpenAI Chat is supported for `openai_chat` API-key providers (including
legacy providers with an empty format). BOS converts `/v1/messages` into the
provider's `/chat/completions` endpoint and returns Messages JSON or SSE. A GLM
provider using an OpenAI-compatible API should keep `api_format=openai_chat`;
configure its Chat base URL, provider credential, allowed models and optional
model mapping normally. No upstream `/messages` endpoint is required.

API-key providers expose these explicitly bounded conversion paths:

| Client endpoint | Provider `api_format` | Upstream operation | JSON / SSE |
| --- | --- | --- | --- |
| `/v1/chat/completions` | `anthropic_messages` | `/v1/messages` | Both |
| `/v1/chat/completions` | `gemini` | `generateContent` / `streamGenerateContent` | Both |
| `/v1/messages` | `gemini` | `generateContent` / `streamGenerateContent` | Both |
| `/v1/messages` | `openai_chat` | `/chat/completions` | Both |

The public model is authorized and retained in responses/audit records; aliases
select the upstream model and its configured pricing. Transport, provider API-key
authentication, stream header timeouts and cancellation use the native relay.
Same-protocol requests retain native pass-through behavior.

The caller's Messages body, beta/version headers, model and historical tool
correlation are untrusted input. Conversion validates these before crossing the
provider boundary. Only the selected provider credential is sent upstream;
caller credentials and arbitrary query parameters are excluded. Converted
executable content uses the existing source-protocol tool authorization and
audit pipeline, and usage accounting reads validated upstream metrics.

Supported request semantics:

- Text strings and text block arrays; a prefix of `system` **or** `developer`
  messages maps to ordered system instruction blocks. Mixing instruction roles
  or inserting instructions after conversation turns is rejected.
- `max_tokens`, `temperature`, `top_p`, stop strings/sequences, and Messages
  `top_k`; only a single candidate (`n=1`). A Chat request without `max_tokens`
  uses 4096 on Anthropic, whose API requires an output limit.
- Custom function declarations, opaque JSON schemas, named/auto/none/required
  choices, invocation IDs, object arguments and multi-turn textual tool results.
  Gemini uses `parametersJsonSchema`, `functionCall` and `functionResponse`;
  Messages `is_error` results map to the Gemini response `error` field. Adjacent
  Gemini turns of the same role are merged with part order preserved, so parallel
  tool results remain in one user turn; incomplete historical calls are rejected.
- JSON replies and incremental SSE text/tool events. Chat `stream_options` supports
  only `include_usage`; Gemini function calls must arrive as complete objects.
  Anthropic serial tool selection is supported; Gemini serial-call guarantees are
  rejected. Converted tools still pass source-protocol authorization/security
  checks before executable content is delivered.

Messages → Chat additionally preserves system instructions, standard sampling
controls, stop sequences, custom tool declarations/choices, parallel invocation
IDs and textual results across turns. `disable_parallel_tool_use` maps to
`parallel_tool_calls=false`; failed tool results receive a `Tool error:` prefix.
Tool results must immediately follow their assistant invocation and precede any
new user text. Assistant prefill and `top_k` are rejected because Chat cannot
preserve them. Text streams immediately; tool ID/name/argument fragments are
assembled by index and validated before tool blocks are delivered. A final
`finish_reason`, real usage and `[DONE]` are required before `message_stop`.
Current Claude Code can append text-only `system`/`developer` session instruction
turns inside `messages`; the Chat bridge preserves their role and exact position.
This extension is rejected for other conversion targets and cannot interrupt
pending tool results.

For this Chat bridge only, validated ephemeral `cache_control` hints (including
`5m`/`1h` TTL hints) are advisory and omitted; upstream caching remains under the
provider's control. Text, schemas and tool arguments are preserved. Validated
`metadata.user_id` is omitted to avoid forwarding local client identity.
Caller-supplied `thinking`, `reasoning_effort`, and `enable_thinking` fields are
forwarded unchanged to Chat, including null values and provider-specific fields
inside `thinking`. BOS does not validate their modes, types, or budget ranges,
remove them, or supply defaults. The caller chooses these controls and the
upstream decides whether to accept them and how to reason. Upstream rejection
uses the existing upstream error handling.
`output_config.effort` remains a locally consumed compatibility hint accepting
`low`, `medium`, `high`, `xhigh`, or `max`; it is not mapped to a thinking control.
`context_management` accepts an empty `edits` list or
`clear_thinking_20251015` with `keep: "all"`, which leaves history unchanged.
Actual history deletion, tool clearing, and task budgets remain unsupported
and return explicit errors.

The Chat bridge accepts optional `safeguards` checks with
`type: "dangerous_tool_use"` and an optional object-valued `classifier_context`.
Null/empty safeguards and a null context are treated as absent. Unknown check
types, extra check fields and malformed values remain explicit errors. BOS
consumes this capability request locally and discards the private classifier
context; neither field is forwarded to Chat. BOS does not run Anthropic's
server-side classifier and does not synthesize `safeguard_results` or a tool
approval. Claude Code 2.1.299 recognizes a complete JSON/SSE response without
these results as unavailable server classification and, for third-party
endpoints, uses its own permission/classifier fallback. Other clients retain
responsibility for their permission handling. BOS tool policies still run on
every converted tool call. Native Anthropic pass-through remains unchanged.

`output_config.format` supports `type: "json_schema"` with an object-valued
`schema` for Messages → Chat requests. BOS maps this to Chat `response_format`
with `json_schema: {name: "response", strict: true, schema: ...}`. A null format
is treated as absent. Schema constraints, references, extensions and numeric
values are preserved without rewriting; support for the schema itself depends
on the upstream provider. Upstream rejections remain errors without retrying as
unconstrained text. Both JSON and SSE replies retain the structured JSON as
Messages text content. Other cross-protocol targets do not gain this support.

The Chat bridge ignores all `anthropic-beta` header values without forwarding
them, including unknown values introduced by new client releases. No beta
allowlist update is required when clients add or version these hints.
Each body capability is still validated independently; beta headers do not
enable unsupported controls or tools, and their requested semantics are not
guaranteed by the Chat bridge.
The SDK's `anthropic-dangerous-direct-browser-access: true` CORS hint is accepted
locally; BOS retains its own browser access policy. Other Anthropic capability
headers remain rejected. Native Anthropic pass-through and Gemini conversion
are unchanged.
Null legacy `function_call` metadata is harmless; populated legacy function
calls are still rejected. Chat `reasoning` or `reasoning_content` text is rendered
as Messages thinking blocks, separately from answer text and tool arguments.
Continuous thinking/text deltas share a block until the content kind changes.
Thinking history carries a `bos-chat-reasoning-v1:<field>` round-trip marker so
the next assistant turn restores the original Chat field. This marker is not a
cryptographic signature; native Anthropic signatures remain unsupported by Chat.

For Claude Code, set `ANTHROPIC_BASE_URL` to the BOS root, use a BOS key in
`ANTHROPIC_AUTH_TOKEN`, and select an authorized model/alias in `ANTHROPIC_MODEL`.
Use text/custom-tool requests and the supported beta flags.
`/v1/messages/count_tokens` remains unsupported for Chat providers, as do
Messages requests to `openai_responses`, `custom` and `realtime` providers.
Use a native `anthropic_messages` provider for full Claude capability support.

Claude Code 2.1.299's default thinking/effort and retain-all-context hints work
without per-client beta/thinking disable switches. Its optional
`dangerous_tool_use` safeguard envelope no longer needs a rejected request and
retry; missing server classification results let the CLI use its own fallback.
This is a bounded Chat bridge, not full native Claude capability support.
Model authorization and tool permissions are unchanged. To exercise an
installed Claude CLI against a real Chat upstream with its default capabilities,
set `BOS_CHAT_LIVE_URL`, `BOS_CHAT_LIVE_KEY` and `BOS_CHAT_LIVE_MODEL`, then run
`go test ./internal/gateway/interfaces -run '^TestMessagesChatLiveClaudeCode$'`.
The opt-in test uses isolated temporary directories and does not modify user
settings or run user hooks. It checks a plain reply and a two-turn Read call
against a generated probe file; Read is the only enabled local tool.

Unsupported request fields return HTTP 400 `unsupported_conversion` with
`error.param` identifying the field. This includes multimodal content,
Chat-source `response_format`, strict tool schemas, logprobs, penalties, seeds,
`max_completion_tokens`, legacy `functions`/`function_call`, multiple candidates,
unknown fields, non-advisory thinking/reasoning controls, compaction,
server-hosted tools, OpenAI beta capabilities, and Anthropic capability/workspace
headers on a Gemini provider (only the standard Messages version header is
accepted). Cache-control hints are rejected outside the Chat bridge. Gemini
assistant prefill is also rejected. Conversion of Responses, OpenAI embeddings
and Messages count_tokens to these upstream protocols is not implemented; use
native endpoints for those operations.

Unsupported upstream content returns HTTP 502 `unsupported_upstream_feature`.
Anthropic text blocks with absent, null or empty `citations`, and tool blocks
with absent/null `caller` or `caller: {"type":"direct"}`, convert to Chat JSON
and SSE normally. These optional fields carry no additional Chat semantics.
Populated citations, programmatic callers and unknown block/caller fields still
fail explicitly, with the field path in the response and request diagnostics.
Examples include thought/signature blocks, Gemini `thoughtSignature`, media,
provider-hosted tool output, citations/grounding metadata, unknown finish reasons,
and malformed/incomplete tool arguments. Gemini thinking signatures cannot be
silently removed because the next tool turn may require them; use native Gemini
for models returning those signatures. This is a common-subset Messages bridge,
not full Claude Code feature compatibility on Gemini.

On a stream that has already started, conversion failures are SSE error events;
no `[DONE]` or `message_stop` success marker is synthesized. Request audit records
mark these turns failed. Valid usage already reported by the upstream still
settles incurred token/cost quotas, including cache pricing and hidden Gemini
thought tokens. Gemini prompt/cache counts may first become available in the final
frame; Messages streams publish cumulative counts in `message_delta.usage`.
Missing required usage metadata is an explicit error rather than a fabricated
successful final total. Tool arguments are bounded to 8 MiB per stream and SSE
frames to 1 MiB.

Protocol references:
- https://developers.openai.com/api/reference/resources/chat
- https://platform.claude.com/docs/en/build-with-claude/streaming
- https://ai.google.dev/api/generate-content
- https://ai.google.dev/gemini-api/docs/function-calling
- https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/lib/streaming/_messages.py

### Gemini agent authentication and base URL compatibility

Gemini native catalog and inference endpoints accept the BOS gateway key in
`?key=...` as a fallback when no supported credential header is provided.
`Authorization`, `x-api-key`, and `x-goog-api-key` retain their existing precedence.
Duplicate `key` parameters and malformed queries are rejected. Query-key
fallback does not apply to OpenAI endpoints.

Agents that append `/v1beta` to a Base URL ending in `/v1` may use
`/v1/v1beta/models` and `/v1/v1beta/models/{model}:{action}`. These are handled as
native Gemini requests without redirects, including URL model permissions,
aliases, rate limits, audit and billing. Only the provider's credential is sent
upstream; the client query key is never forwarded or recorded in gateway logs.

Gemini model resource names returned by Google (`models/{id}`) remain valid
stored catalog entries. The gateway normalizes these to `{id}` for routing,
model mappings, tenant allowlists and pricing, and returns native discovery names
with exactly one `models/` prefix. Existing catalog rows, prefixed bindings and
price configurations are supported without resynchronization or database edits.
The normalization is limited to Gemini; slash-containing OpenAI IDs are unchanged.

Some agents discover Gemini models at `/v1/v1beta/models` but invoke them at
`POST /v1/models/{model}:{action}`. These native POST actions are also supported,
including query-key auth, URL model authorization, mapping, SSE, audit and usage.
OpenAI `GET /v1/models` retains its existing format and header authentication.
