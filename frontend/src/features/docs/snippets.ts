export const environment = `export BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"
export BOS_BASE_URL="{{baseUrl}}"
export BOS_MODEL="{{model}}"`;

export const chatCurl = `curl --fail-with-body "{{baseUrl}}/chat/completions" \\
  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "{{model}}",
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }'`;

export const chatPython = `# Install: python -m pip install openai
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["BOS_GATEWAY_KEY"],
    base_url="{{baseUrl}}",
)

response = client.chat.completions.create(
    model="{{model}}",
    messages=[{"role": "user", "content": "Say hello in one sentence."}],
)
print(response.choices[0].message.content)`;

export const chatNode = `// Install: npm install openai
// Save as hello.mjs, then run: node hello.mjs
import OpenAI from 'openai';

const client = new OpenAI({
  apiKey: process.env.BOS_GATEWAY_KEY,
  baseURL: '{{baseUrl}}',
});

const response = await client.chat.completions.create({
  model: '{{model}}',
  messages: [{ role: 'user', content: 'Say hello in one sentence.' }],
});
console.log(response.choices[0].message.content);`;

export const streamCurl = `curl --no-buffer --fail-with-body "{{baseUrl}}/chat/completions" \\
  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "{{model}}",
    "stream": true,
    "messages": [{"role": "user", "content": "Explain a closure in JavaScript."}]
  }'`;

export const streamPython = `import os
from openai import OpenAI

client = OpenAI(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{baseUrl}}")
stream = client.chat.completions.create(
    model="{{model}}",
    messages=[{"role": "user", "content": "Explain a closure in JavaScript."}],
    stream=True,
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="", flush=True)`;

export const responsesCurl = `curl --fail-with-body "{{baseUrl}}/responses" \\
  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "{{model}}", "input": "Say hello in one sentence."}'`;

export const responsesPython = `import os
from openai import OpenAI

client = OpenAI(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{baseUrl}}")
response = client.responses.create(model="{{model}}", input="Say hello in one sentence.")
print(response.output_text)`;

export const messagesCurl = `curl --fail-with-body "{{origin}}/v1/messages" \\
  -H "x-api-key: $BOS_GATEWAY_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "{{model}}",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }'`;

export const messagesPython = `# Install: python -m pip install anthropic
import os
from anthropic import Anthropic

client = Anthropic(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{origin}}")
response = client.messages.create(
    model="{{model}}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Say hello in one sentence."}],
)
for block in response.content:
    if block.type == "text":
        print(block.text)`;

export const modelsCurl = `curl --fail-with-body "{{baseUrl}}/models" \\
  -H "Authorization: Bearer $BOS_GATEWAY_KEY"`;

export const codexConfig = `# Merge into your user-level ~/.codex/config.toml.
# Preserve your existing settings; do not duplicate TOML tables.
model = "{{model}}"
model_provider = "bosapi"

[model_providers.bosapi]
name = "BOS API"
base_url = "{{baseUrl}}"
env_key = "BOS_GATEWAY_KEY"
wire_api = "responses"
requires_openai_auth = false`;

export const claudeEnv = `export ANTHROPIC_BASE_URL="{{origin}}"
export ANTHROPIC_AUTH_TOKEN="$BOS_GATEWAY_KEY"
export ANTHROPIC_MODEL="{{model}}"
claude`;

export const openCodeConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "model": "bosapi/{{model}}",
  "provider": {
    "bosapi": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "BOS API",
      "options": {
        "baseURL": "{{baseUrl}}",
        "apiKey": "{env:BOS_GATEWAY_KEY}"
      },
      "models": {
        "{{model}}": { "name": "{{model}}" }
      }
    }
  }
}`;
