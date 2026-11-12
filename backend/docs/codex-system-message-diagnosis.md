# Diagnosing Codex OAuth compatibility failures

The November 11–12, 2026 fixes address request and response conversion failures
seen after updating the declared Codex client version. Updating the version
header alone does not update the gateway's protocol conversion capabilities.
The historical upstream observations below describe specific accounts, models,
and requests; they do not establish availability for every upstream capability.

## System instructions and metadata

An upstream HTTP 400 with `System messages are not allowed` can occur when a
Chat request's leading `system` messages are forwarded as Responses input items.
A native Responses request using `instructions` can succeed for the same account
and model while the Chat bridge fails.

The bridge promotes leading text-only system messages into `instructions` and
removes them from `input`. Developer messages retain their original role. Native
OAuth Responses requests use the same promotion, placing any existing
`instructions` after the promoted text. Remaining history items, including
opaque reasoning and exact tool arguments, retain their order and values.
System messages with extra metadata, unsupported content, or a position after
other input items return explicit field errors rather than losing information.

The response converter accepts auxiliary `response.metadata` events without
emitting them as answer content. Metadata alone cannot complete a request. A
valid terminal event remains required before recording terminal usage.

```text
Chat -> Codex:
  instructions = join(prefix_system_text, "\n\n")
  input = convert(conversation_without_prefix_system)
  send(instructions, input, stream=true, store=false)

native OAuth Responses -> Codex:
  validate all system input before mutating request
  require system items to be a text-only prefix without extra metadata
  instructions = join(promoted_text + existing_instructions, "\n\n")
  preserve every remaining input item

Codex SSE -> Chat:
  response.metadata: continue
  response.output_text.delta: emit/collect text
  response.completed: validate final output, record usage, finish
  unknown event / EOF without completion: explicit conversion error
```

Regression coverage includes streaming and non-streaming Chat and native
Responses requests, single promotion of system instructions, developer and
reasoning history, large integer tool arguments, lossy-conversion rejection,
metadata event ordering, usage, and stream termination.

## Token limit aliases and incomplete responses

Historical native Responses checks accepted `max_output_tokens` and returned
`response.incomplete` with reason `max_output_tokens` when the budget was
exhausted. The same upstream did not accept `max_completion_tokens` directly.

OAuth Chat conversion validates positive integer limits and alias conflicts,
then maps `max_tokens` or `max_completion_tokens` to `max_output_tokens`. This
budget includes reasoning tokens; it does not promise a separate visible-text
limit. Only an incomplete terminal event explicitly caused by
`max_output_tokens` becomes Chat `finish_reason: "length"`. The converter retains
representable output and usage. Other incomplete reasons and invalid truncated
tool arguments still return errors.

```text
limit = validated_alias(max_tokens, max_completion_tokens)
if limit exists: codex.max_output_tokens = limit
if terminal.type == response.incomplete:
  require terminal.status == incomplete
  require terminal.incomplete_details.reason == max_output_tokens
  validate and return available output + usage
  chat.finish_reason = length
  finish SSE with [DONE]
```

These mappings follow observed upstream behavior. Other proxies may remove token
limits for their own OAuth compatibility; removing a limit would change the
caller's request semantics here.

## Upstream HTTP 200 followed by Chat HTTP 502

An upstream success can still fail during response conversion:

```text
upstream_status=200
failure_stage=response_conversion
error_param=response.content-type
error_reason=expected an SSE stream
```

Some observed Codex responses contained valid SSE without declaring the expected
Content-Type. A native forwarding path may set its own response headers, so its
outward Content-Type does not establish the original upstream header value.

The Chat bridge keeps the normal parsing path when the upstream declares SSE.
Otherwise, it reads only a bounded prefix and recognizes an SSE field such as
`event:`, `data:`, a comment, `id:`, or `retry:`. It replays every buffered byte
through the same parser and first-token observer. JSON, HTML, and arbitrary plain
text fail format recognition. Unknown events, invalid JSON, and missing terminal
events still fail. Diagnostics record a sanitized `upstream_content_type` without
logging bodies or unrelated headers.

```text
if upstream Content-Type declares SSE:
  parse SSE as before
else if bounded first bytes identify an SSE field:
  replay buffered bytes + remaining body through first-token observer
  parse known events and require a valid terminal event
else:
  return explicit response format error
```

Tests cover missing or incorrect Content-Type, fragmented reads, JSON and SSE
Chat output, usage, completion markers, error-page rejection, unknown events,
truncated streams, first-token timing, and closing the upstream body. Local
regression success does not establish that a deployed instance has been updated.

## Structured output and reasoning history

Two further conversion errors can remain after SSE recognition is fixed:

| Stage | Field | Failure |
| --- | --- | --- |
| Request conversion | `response_format` | Unsupported Chat field |
| Response conversion after upstream HTTP 200 | `response.output.type` | Output item cannot be represented in Chat |

Native upstream checks accepted JSON object and strict JSON Schema formats.
Higher-reasoning responses also contained reasoning items, summary events, and
encrypted content. Rejecting those items after emitting answer text can cause
clients to retry an otherwise successful upstream request.

The compatibility path handles these cases as follows:

1. Chat `response_format` accepts null, text, JSON object, and JSON Schema forms
   and maps them to Responses `text.format`. Schema, name, description, and
   strictness are preserved. Invalid structures and extra fields are rejected.
2. Known reasoning summary events are accepted. The terminal reasoning item
   determines the summary, delivered once as `reasoning_content`. The answer
   text remains separate.
3. Original reasoning items return through the `codex_reasoning` array extension.
   Each entry includes `item`, a UTF-8 byte `text_offset`, and a `tool_offset`.
   Clients merge streamed arrays and return them in assistant history so the
   gateway can restore the original order. Encrypted content is preserved
   without decryption or fabricated provider signatures.
4. Upstream requests include `reasoning.encrypted_content` for stateless history
   continuation. Reasoning tokens are included in `completion_tokens` and also
   identified in `completion_tokens_details.reasoning_tokens`, without double
   charging.
5. Unknown output types, invalid history offsets, inconsistent summaries, and
   invalid tool arguments remain errors.

```text
Chat.response_format -> Responses.text.format
  json_schema: flatten json_schema envelope, preserve every schema value

Responses.output:
  reasoning -> preserve original item and its position as codex_reasoning
               expose summary text separately as reasoning_content
  message -> answer content
  function_call -> validated Chat tool call

Chat assistant history:
  validate codex_reasoning item types, offsets and summary consistency
  interleave original reasoning items with answer and tool history
  replay unchanged encrypted_content to Codex
```

The sanitized SSE fixture and offline regressions cover streaming and
non-streaming conversion, reasoning replay, exact integers, tool ordering, UTF-8
offsets, rejected parameters, and usage. Historical checks also exercised a
non-streaming text request and a streaming strict-schema request through the new
converter against a real upstream. Those observations do not establish the
current behavior of any deployed Chat endpoint; repeat authorized acceptance
checks after updating an instance.

Official field references:

- [Responses migration: response_format becomes text.format](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Reasoning: stateless encrypted content and history](https://developers.openai.com/api/docs/guides/reasoning)

## Stateless Chat `store` hints

A Chat request can fail before reaching the upstream when a client includes the
Responses-style `store` field:

```text
POST chat/completions
failure_stage=request_conversion
error_param=store
error_reason=unsupported in this protocol conversion
```

OAuth Codex requests already enforce `store:false`. For Chat targeting
`openai_responses`, conversion accepts a boolean false hint and removes it before
parsing the common Chat subset. `store:true` is rejected because this path does
not provide persisted-response semantics. Nulls, strings, and objects are also
rejected.

```text
if target == openai_responses && source == chat && body.store exists:
  require boolean
  require store == false
  delete body.store
```

Tests cover false, null, true, string, and object values and verify that the
converted Chat subset does not retain the hint. This compatibility handling does
not change the final Codex request's mandatory `store:false` setting.
