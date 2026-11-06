# Gateway Infrastructure

This package contains concrete adapters for persistence, upstream providers, Redis, workers, and notification transports.

Chat -> Anthropic preserves native `thinking` / `output_config` settings and
converts signed and redacted response blocks into `reasoning_content` plus the
`anthropic_thinking` history extension. See the [gateway protocol guide](../interfaces/README.md)
for the JSON/SSE client contract and tool-continuation requirements. History
restoration accepts only thinking blocks and retains normal tool validation.

Messages requests routed to an OpenAI Chat provider support `output_config.format`
with `type: "json_schema"` and an object-valued `schema`. The adapter preserves the
schema and sends it as `response_format.json_schema` with `name: "response"` and
`strict: true`. A null format is treated as absent. Schema support is determined by
the upstream; unsupported schemas return an upstream error without retrying as
unconstrained text. This applies to both JSON and streaming responses. Other
cross-protocol targets retain their existing feature boundaries.
