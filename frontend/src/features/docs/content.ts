import type { DocPage } from './types';

export const docPages: DocPage[] = [
  {
    slug: 'quickstart',
    title: 'Get started with BOS API',
    group: 'Getting started',
    icon: 'spark',
    description:
      'Connect an upstream service, configure organization access, and make a gateway request.',
    audience: 'All users',
    protocol: 'chat',
    sections: [
      {
        id: 'first-login',
        title: 'Sign in and enroll MFA',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use the administrator credentials generated in deploy/stack.env. Keep that file private. Administrator MFA is required by default. API Key holders can use the separate Key sign-in flow.',
          },
        ],
      },
      {
        id: 'first-key-setup',
        title: 'Configure your first request',
        blocks: [
          {
            type: 'list',
            items: [
              'A super administrator creates an upstream provider and discovers or enters its model IDs. Supply your own upstream credentials; model access and credits are not included.',
              'Create an organization or use the initial organization. Assign its provider and model access.',
              'Create a gateway API Key for that organization. Save the full secret securely.',
              'Use an allowed model in a request to the gateway. Check Usage and Security Events after the request.',
            ],
            ordered: true,
          },
        ],
      },
      {
        id: 'capacity',
        title: 'Community capacity',
        blocks: [
          {
            type: 'paragraph',
            text: 'This distribution permits at most 2 organizations and 10 API Keys per installation. The initial organization and disabled resources count. Removing an organization disables it and keeps its slot; deleting an API Key releases its slot. Batch creation and imports reject the entire batch if it exceeds capacity. Creation requests over the limit return HTTP 409. Reaching capacity does not delete existing data.',
          },
        ],
      },
      {
        id: 'support',
        title: 'Get help',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use the published repository for community questions. Include the version, request ID, sanitized error code, and a minimal reproduction. Never post credentials or customer prompts.',
          },
        ],
      },
    ],
  },
  {
    slug: 'concepts',
    title: 'How BOS API works',
    group: 'Getting started',
    icon: 'concepts',
    audience: 'All users',
    description: 'Understand providers, organizations, keys, and gateway policies.',
    sections: [
      {
        id: 'architecture',
        title: 'Request flow',
        blocks: [
          {
            type: 'paragraph',
            text: 'Clients authenticate with a gateway API Key. The gateway checks the organization, allowed providers and models, quotas, rate limits, and built-in inspection before forwarding to a configured upstream.',
          },
        ],
      },
      {
        id: 'roles',
        title: 'Administration',
        blocks: [
          {
            type: 'paragraph',
            text: 'Super administrators manage shared providers, organization assignments, and global settings. Organization administrators manage their own organization’s Keys and usage. Assigned providers are read-only for organization administrators.',
          },
        ],
      },
      {
        id: 'credentials',
        title: 'Separate credentials',
        blocks: [
          {
            type: 'paragraph',
            text: 'Upstream credentials connect the gateway to the model service. Gateway Keys authenticate applications to BOS API. A gateway Key does not provide upstream credits.',
          },
        ],
      },
    ],
  },
  {
    slug: 'console-overview',
    title: 'Console overview',
    group: 'Using the console',
    icon: 'grid',
    audience: 'Administrators & Key users',
    description: 'Find access management, usage, diagnostics and security events.',
    sections: [
      {
        id: 'resources',
        title: 'Configure access',
        blocks: [
          {
            type: 'paragraph',
            text: 'Providers & Models contains upstream configuration. Organizations assigns access to each team. API Keys defines application credentials and policy.',
          },
        ],
      },
      {
        id: 'visibility',
        title: 'Inspect activity',
        blocks: [
          {
            type: 'paragraph',
            text: 'Usage shows request and token accounting. Request logs and errors support diagnosis. Security Events shows built-in inspection events and redacted evidence.',
          },
        ],
      },
      {
        id: 'settings',
        title: 'Administration settings',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use Admin Configuration for administrator accounts and authentication delivery settings. API address, announcements and installation telemetry are managed by authorized administrators. Community upgrades follow the manual deployment guide.',
          },
        ],
      },
    ],
  },
  {
    slug: 'organizations',
    title: 'Organizations & access',
    group: 'Using the console',
    icon: 'users',
    audience: 'Administrators',
    description: 'Isolate team access and assign providers and models.',
    sections: [
      {
        id: 'create',
        title: 'Create an organization',
        blocks: [
          {
            type: 'paragraph',
            text: 'A super administrator creates organizations and assigns their provider/model access. The installation supports at most 2 organizations including the initial organization. Removing an organization disables it; disabled organizations still count.',
          },
        ],
      },
      {
        id: 'bindings',
        title: 'Provider bindings',
        blocks: [
          {
            type: 'paragraph',
            text: 'An active binding allows the assigned provider and its permitted models. A paused binding blocks access. An empty model allowlist on an active binding admits all active models on that provider. Pausing every binding does not grant fallback access.',
          },
        ],
      },
      {
        id: 'administrators',
        title: 'Organization administrators',
        blocks: [
          {
            type: 'paragraph',
            text: 'Create an org_admin account for the organization when console access is needed. Creating an organization does not automatically create a login or API Key.',
          },
        ],
      },
    ],
  },
  {
    slug: 'providers-models',
    title: 'Providers & models',
    group: 'Using the console',
    icon: 'provider',
    audience: 'Administrators',
    description: 'Connect model services and verify model configuration.',
    sections: [
      {
        id: 'credentials',
        title: 'Configure a provider',
        blocks: [
          {
            type: 'paragraph',
            text: 'Super administrators configure a supported protocol, upstream URL, and their own credentials. API Key and supported subscription authorization flows are available. Organization administrators can inspect assigned channels but cannot create or change providers.',
          },
        ],
      },
      {
        id: 'models',
        title: 'Discover models',
        blocks: [
          {
            type: 'paragraph',
            text: 'Discover available models or enter explicit IDs, then verify the protocol and model capabilities. Actual availability depends on the selected upstream account.',
          },
        ],
      },
      {
        id: 'pricing',
        title: 'Usage cost metadata',
        blocks: [
          {
            type: 'paragraph',
            text: 'Configure provider price metadata to estimate usage cost. Gateway accounting does not purchase upstream credits or guarantee upstream invoices.',
          },
        ],
      },
      {
        id: 'access',
        title: 'Assign access',
        blocks: [
          {
            type: 'paragraph',
            text: 'Assign the provider and permitted models to an organization, then create a Key. Use a small authorized test request and inspect the outcome.',
          },
        ],
      },
    ],
  },
  {
    slug: 'api-keys',
    title: 'Manage API Keys',
    group: 'Using the console',
    icon: 'key',
    audience: 'Administrators',
    description: 'Create credentials, set access policies, and inspect Key activity.',
    sections: [
      {
        id: 'create',
        title: 'Create and store a Key',
        blocks: [
          {
            type: 'paragraph',
            text: 'Create a Key within an active organization. Store the full secret securely and send it in the protocol’s authentication header. Do not use a provider secret as a gateway Key.',
          },
        ],
      },
      {
        id: 'limits',
        title: 'Capacity and lifecycle',
        blocks: [
          {
            type: 'paragraph',
            text: 'The installation supports at most 10 API Keys across all organizations. Disabled Keys count. Deleted Keys release capacity. Create, duplicate, import and batch-create operations share the same capacity check. Rotating an existing Key does not consume a new slot.',
          },
        ],
      },
      {
        id: 'policy',
        title: 'Key access and budgets',
        blocks: [
          {
            type: 'paragraph',
            text: 'Set allowed providers and models, expiration, token and amount budgets, periodic quotas, rate limits, and IP access policy. Key permissions cannot expand organization permissions.',
          },
        ],
      },
      {
        id: 'inspection',
        title: 'Inspection controls',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use per-Key inspection controls and review detected events with redacted evidence. Validate behavior with representative synthetic requests before enforcing a policy.',
          },
        ],
      },
    ],
  },
  {
    slug: 'routing-limits',
    title: 'Limits & quotas',
    group: 'Using the console',
    icon: 'route',
    audience: 'Administrators',
    description: 'Apply basic access, request-rate and usage budgets.',
    sections: [
      {
        id: 'rate-limits',
        title: 'Rate limits',
        blocks: [
          {
            type: 'paragraph',
            text: 'Configure the basic request limits supported by the console. Distributed rate-limit enforcement uses Redis; local enforcement is used when configured without Redis.',
          },
        ],
      },
      {
        id: 'quotas',
        title: 'Usage budgets',
        blocks: [
          {
            type: 'paragraph',
            text: 'Set total and periodic token or amount budgets on a Key. Usage accounting and policy apply within its organization.',
          },
        ],
      },
      {
        id: 'models',
        title: 'Model selection',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use an exact upstream model ID permitted by both the organization and Key. This community source excludes configurable automatic-routing and aggregate-model rule management.',
          },
        ],
      },
    ],
  },
  {
    slug: 'usage-logs',
    title: 'Usage & request logs',
    group: 'Using the console',
    icon: 'chart',
    audience: 'Administrators & Key users',
    description: 'Review requests, token accounting, errors and sanitized traces.',
    sections: [
      {
        id: 'usage',
        title: 'Usage accounting',
        blocks: [
          {
            type: 'paragraph',
            text: 'Usage shows requests, input/output tokens, cache tokens, and configured cost metadata. Filters are scoped to authorized organizations and Keys.',
          },
        ],
      },
      {
        id: 'logs',
        title: 'Request diagnosis',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use request IDs to correlate gateway activity. Capture the HTTP status and stable error code when reporting a problem. Logs and security evidence must not expose full credentials.',
          },
        ],
      },
      {
        id: 'retention',
        title: 'Retention',
        blocks: [
          {
            type: 'paragraph',
            text: 'Database retention and rollup jobs manage historical usage. Back up data and encryption keys before upgrades. Scheduled report generation and delivery are excluded from this source distribution.',
          },
        ],
      },
    ],
  },
  {
    slug: 'security-admin',
    title: 'Security & administration',
    group: 'Using the console',
    icon: 'shield',
    audience: 'Administrators',
    description: 'Inspect security events and configure core account protection.',
    sections: [
      {
        id: 'events',
        title: 'Security events',
        blocks: [
          {
            type: 'paragraph',
            text: 'Review event type, action, severity, request ID and redacted evidence. A rule match is a detection signal and does not by itself prove a real credential leak or malicious action.',
          },
        ],
      },
      {
        id: 'inspection',
        title: 'Security profile status',
        blocks: [
          {
            type: 'paragraph',
            text: 'Community inspection includes fixed credential checks, current-user-input redaction, model-output code markers, and tool authorization checks. The default tool mode is observe; upstream credential redaction still applies when upstream inspection is enabled. Advanced injection signatures, obfuscation and encoded-fragment detection, custom rule management, and security event Webhooks are excluded. This does not provide complete bidirectional credential leak prevention.',
          },
        ],
      },
      {
        id: 'accounts',
        title: 'Account protection',
        blocks: [
          {
            type: 'paragraph',
            text: 'Keep administrator MFA enabled. Login lockout, role checks, organization isolation and encrypted credential storage remain available. Authentication notification delivery is separate from excluded security-event Webhooks.',
          },
        ],
      },
      {
        id: 'disclosure',
        title: 'Report a vulnerability',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use the private security channel documented on the published repository. Do not disclose exploitable vulnerabilities or real credentials in public issues.',
          },
        ],
      },
    ],
  },
  {
    slug: 'api-integration',
    title: 'API integration',
    group: 'API reference',
    icon: 'code',
    audience: 'Developers',
    protocol: 'chat',
    description:
      'Use your existing SDK with BOS API. Configure a gateway address, authenticate with your Key, and choose the protocol your model supports.',
    sections: [
      {
        id: 'connection',
        title: 'Base URL & authentication',
        blocks: [
          {
            type: 'note',
            title: 'Connect your own upstream first',
            text: 'These are gateway protocol interfaces, not included model services. An administrator must connect an LLM service, configure model access and security policies, and issue your gateway Key before these examples can succeed.',
            tone: 'info',
          },
          {
            type: 'table',
            headers: ['Client or protocol', 'Base URL / endpoint', 'Authentication'],
            rows: [
              ['OpenAI SDK', '`{{baseUrl}}`', '`Authorization: Bearer YOUR_BOS_API_KEY`'],
              ['Chat Completions', '`POST /v1/chat/completions`', 'Bearer token'],
              ['Responses', '`POST /v1/responses`', 'Bearer token'],
              ['Anthropic SDK', '`{{origin}}`', '`x-api-key: YOUR_BOS_API_KEY`'],
              ['Messages', '`POST /v1/messages`', '`x-api-key` and `anthropic-version`'],
            ],
          },
          {
            type: 'note',
            title: 'Use the gateway Key, not a login token',
            text: 'The console login token, Key portal session token, and upstream provider Key are not substitutes for your BOS API Key. Never place credentials in a URL.',
            tone: 'info',
          },
        ],
      },
      {
        id: 'environment',
        title: 'Prepare your environment',
        blocks: [
          {
            type: 'code',
            label: 'macOS / Linux / WSL',
            language: 'bash',
            code: 'export BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"\nexport BOS_BASE_URL="{{baseUrl}}"\nexport BOS_MODEL="{{model}}"',
          },
          {
            type: 'paragraph',
            text: 'The values above are examples. Replace `YOUR_BOS_API_KEY` only in your local environment or secret manager. Do not add a real Key to this page, a source file, or a browser bundle.',
          },
        ],
      },
      {
        id: 'chat-completions',
        title: 'Chat Completions',
        blocks: [
          {
            type: 'paragraph',
            text: 'Start with a model that supports `chat.completions`. All three examples below make the same non-streaming request.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'cURL',
                language: 'bash',
                code: 'curl --fail-with-body "{{baseUrl}}/chat/completions" \\\n  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d \'{\n    "model": "{{model}}",\n    "messages": [{"role": "user", "content": "Say hello in one sentence."}]\n  }\'',
              },
              {
                label: 'Python',
                language: 'python',
                code: '# Install: python -m pip install openai\nimport os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    api_key=os.environ["BOS_GATEWAY_KEY"],\n    base_url="{{baseUrl}}",\n)\n\nresponse = client.chat.completions.create(\n    model="{{model}}",\n    messages=[{"role": "user", "content": "Say hello in one sentence."}],\n)\nprint(response.choices[0].message.content)',
              },
              {
                label: 'JavaScript',
                language: 'javascript',
                code: "// Install: npm install openai\n// Save as hello.mjs, then run: node hello.mjs\nimport OpenAI from 'openai';\n\nconst client = new OpenAI({\n  apiKey: process.env.BOS_GATEWAY_KEY,\n  baseURL: '{{baseUrl}}',\n});\n\nconst response = await client.chat.completions.create({\n  model: '{{model}}',\n  messages: [{ role: 'user', content: 'Say hello in one sentence.' }],\n});\nconsole.log(response.choices[0].message.content);",
              },
            ],
          },
          {
            type: 'paragraph',
            text: 'Expected result: HTTP 200 with a `choices` array. Read `choices[0].message.content` for the text reply. Token usage is available when supplied by the upstream.',
          },
        ],
      },
      {
        id: 'responses',
        title: 'Responses API',
        blocks: [
          {
            type: 'note',
            title: 'Requires a Responses-capable model',
            text: 'Chat Completions support does not imply Responses support. Select the appropriate model and confirm access before using this endpoint, especially for Codex.',
            tone: 'warning',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'cURL',
                language: 'bash',
                code: 'curl --fail-with-body "{{baseUrl}}/responses" \\\n  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d \'{"model": "{{model}}", "input": "Say hello in one sentence."}\'',
              },
              {
                label: 'Python',
                language: 'python',
                code: 'import os\nfrom openai import OpenAI\n\nclient = OpenAI(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{baseUrl}}")\nresponse = client.responses.create(model="{{model}}", input="Say hello in one sentence.")\nprint(response.output_text)',
              },
            ],
          },
        ],
      },
      {
        id: 'messages',
        title: 'Anthropic Messages',
        blocks: [
          {
            type: 'paragraph',
            text: 'Use a model with Messages support. The Anthropic SDK appends `/v1/messages` itself, so its Base URL is the gateway root, without `/v1`.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'cURL',
                language: 'bash',
                code: 'curl --fail-with-body "{{origin}}/v1/messages" \\\n  -H "x-api-key: $BOS_GATEWAY_KEY" \\\n  -H "anthropic-version: 2023-06-01" \\\n  -H "Content-Type: application/json" \\\n  -d \'{\n    "model": "{{model}}",\n    "max_tokens": 1024,\n    "messages": [{"role": "user", "content": "Say hello in one sentence."}]\n  }\'',
              },
              {
                label: 'Python',
                language: 'python',
                code: '# Install: python -m pip install anthropic\nimport os\nfrom anthropic import Anthropic\n\nclient = Anthropic(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{origin}}")\nresponse = client.messages.create(\n    model="{{model}}",\n    max_tokens=1024,\n    messages=[{"role": "user", "content": "Say hello in one sentence."}],\n)\nfor block in response.content:\n    if block.type == "text":\n        print(block.text)',
              },
            ],
          },
        ],
      },
      {
        id: 'next',
        title: 'Build on your first request',
        blocks: [
          {
            type: 'cards',
            items: [
              {
                title: 'Models & capabilities',
                text: 'Find model IDs and check their supported protocols.',
                href: '/docs/models',
              },
              {
                title: 'Streaming & tool calls',
                text: 'Handle incremental output and agent tool cycles.',
                href: '/docs/streaming-tools',
              },
              {
                title: 'Coding agents',
                text: 'Configure your terminal or editor.',
                href: '/docs/coding-agents',
              },
              {
                title: 'Errors & troubleshooting',
                text: 'Understand error codes and Request IDs.',
                href: '/docs/troubleshooting',
              },
            ],
          },
        ],
      },
    ],
  },
  {
    slug: 'models',
    title: 'Model configuration & capabilities',
    group: 'API reference',
    icon: 'models',
    audience: 'Developers',
    description:
      'Learn how to specify your own model ID and verify its protocol and capabilities. BOS API does not include preconfigured models or model services.',
    sections: [
      {
        id: 'catalog',
        title: 'Use your own model configuration',
        blocks: [
          {
            type: 'paragraph',
            text: 'The model field refers to an upstream model configured by you or your organization. Its ID, protocol, and capabilities depend on the LLM service you connect; there is no fixed set of platform-supplied models to choose from.',
          },
          {
            type: 'list',
            items: [
              'Connect your own LLM service in Providers & Models, then discover or configure its model IDs.',
              'Grant the intended organization and gateway Key access to that upstream and model.',
              'Replace `YOUR_MODEL_ID` in the examples with that exact configured ID, or an automatic-route name you configured yourself.',
            ],
            ordered: true,
          },
          {
            type: 'note',
            title: 'Examples are not included services',
            text: 'SDK and protocol names describe integration formats only. They do not mean BOS API supplies those vendors, model accounts, subscriptions, or inference capacity.',
            tone: 'info',
          },
        ],
      },
      {
        id: 'authenticated-list',
        title: 'Query with your Key',
        blocks: [
          {
            type: 'code',
            label: 'List gateway models',
            language: 'bash',
            code: 'curl --fail-with-body "{{baseUrl}}/models" \\\n  -H "Authorization: Bearer $BOS_GATEWAY_KEY"',
          },
          {
            type: 'paragraph',
            text: 'Run this in your terminal with `BOS_GATEWAY_KEY` set. Compare exact IDs, including case, punctuation, and any provider prefix. Then make a small inference request to verify end-to-end access.',
          },
        ],
      },
      {
        id: 'choose',
        title: 'Choose by capability, not by name',
        blocks: [
          {
            type: 'table',
            headers: ['Workflow', 'Required capability'],
            rows: [
              ['Basic OpenAI SDK chat', 'Chat Completions'],
              ['Codex CLI', 'Responses, streaming, and working tool-call flows'],
              [
                'Claude Code',
                'Anthropic Messages, streaming, tools, and compatible model behavior',
              ],
              ['Cline / Roo Code', 'Compatible chat and tool behavior; Roo requires native tools'],
              ['Image inputs', 'Vision support in the selected protocol'],
            ],
          },
          {
            type: 'note',
            title: 'Other modalities',
            text: 'Image generation, embeddings, search, or live features depend on the configured upstream. A text model cannot provide them just because a route or example exists. Confirm the specific model and capability with your administrator.',
            tone: 'info',
          },
        ],
      },
    ],
  },
  {
    slug: 'streaming-tools',
    title: 'Streaming & tool calls',
    group: 'API reference',
    icon: 'bolt',
    audience: 'Developers',
    protocol: 'chat',
    description:
      'Add incremental output and understand the model-to-client tool loop used by coding agents.',
    sections: [
      {
        id: 'streaming',
        title: 'Stream a chat response',
        blocks: [
          {
            type: 'paragraph',
            text: 'Set `stream: true` and consume the response as server-sent events. With cURL, `--no-buffer` displays chunks as they arrive.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'cURL',
                language: 'bash',
                code: 'curl --no-buffer --fail-with-body "{{baseUrl}}/chat/completions" \\\n  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d \'{\n    "model": "{{model}}",\n    "stream": true,\n    "messages": [{"role": "user", "content": "Explain a closure in JavaScript."}]\n  }\'',
              },
              {
                label: 'Python',
                language: 'python',
                code: 'import os\nfrom openai import OpenAI\n\nclient = OpenAI(api_key=os.environ["BOS_GATEWAY_KEY"], base_url="{{baseUrl}}")\nstream = client.chat.completions.create(\n    model="{{model}}",\n    messages=[{"role": "user", "content": "Explain a closure in JavaScript."}],\n    stream=True,\n)\nfor chunk in stream:\n    if chunk.choices:\n        print(chunk.choices[0].delta.content or "", end="", flush=True)',
              },
            ],
          },
        ],
      },
      {
        id: 'stream-errors',
        title: 'Handle stream completion and errors',
        blocks: [
          {
            type: 'list',
            items: [
              'Chat Completions uses incremental chunks; ignore empty choices when processing non-content events.',
              'Messages and Responses use their own event types. Use the corresponding SDK parser rather than the Chat Completions parser.',
              'An error before streaming starts may be a JSON error response. A disconnect after output begins is not a successful completion.',
              'Avoid blindly replaying an agent turn after a network failure: a tool action may already have occurred.',
            ],
          },
        ],
      },
      {
        id: 'tool-cycle',
        title: 'Understand the tool-call cycle',
        blocks: [
          {
            type: 'list',
            items: [
              'Your client sends tool definitions together with the conversation.',
              'The model returns tool names, call IDs, and arguments instead of final text.',
              'Your client validates and approves the requested action, then runs the tool locally or in its own environment.',
              'Your client sends the tool results back with the matching call IDs and continues the conversation.',
            ],
            ordered: true,
          },
          {
            type: 'note',
            title: 'The agent executes the tool',
            text: 'BOS API handles the model request and applicable gateway policy. It does not turn a model tool call into permission to run commands on your computer.',
            tone: 'info',
          },
        ],
      },
      {
        id: 'verify-tools',
        title: 'Verify an agent-ready model',
        blocks: [
          {
            type: 'paragraph',
            text: 'In a disposable project, test a read-only file operation, a small approved edit, and a follow-up question that uses the tool result. Confirm incremental tool arguments are handled and that a blocked operation is surfaced as an error. Plain chat success is not enough to prove agent compatibility.',
          },
        ],
      },
    ],
  },
  {
    slug: 'coding-agents',
    title: 'Connect your tools',
    description:
      'Choose a client, then follow its installation, configuration and verification steps.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'agents',
    sections: [
      {
        id: 'choose-client',
        title: 'Choose your tool',
        blocks: [
          {
            type: 'cards',
            items: [
              {
                title: 'Codex Desktop',
                text: 'Install the desktop app, edit its user configuration, and make the gateway Key available to the app.',
                href: '/docs/codex-desktop',
                icon: 'codex',
              },
              {
                title: 'Claude Code CLI',
                text: 'Install Claude Code, configure the current terminal, then start it inside your project.',
                href: '/docs/claude-code',
                icon: 'claude-code',
              },
              {
                title: 'Claude Desktop',
                text: 'Configure a gateway in Claude’s own third-party inference window, without a separate switching tool.',
                href: '/docs/claude-desktop',
                icon: 'claude-code',
              },
              {
                title: 'Codex CLI',
                text: 'Install the command-line client and connect it with an explicit Responses provider.',
                href: '/docs/codex',
                icon: 'codex',
              },
              {
                title: 'VS Code + Cline / Roo Code',
                text: 'Install the extension, choose OpenAI Compatible, and configure each task mode.',
                href: '/docs/cline-roo',
                icon: 'cline-roo',
              },
              {
                title: 'OpenClaw',
                text: 'Use the onboarding wizard to configure a custom endpoint and model.',
                href: '/docs/openclaw',
                icon: 'openclaw',
              },
              {
                title: 'OpenCode',
                text: 'Install OpenCode, save a user-level provider configuration, and select the model.',
                href: '/docs/opencode',
                icon: 'opencode',
              },
              {
                title: 'Cursor',
                text: 'Use a custom OpenAI Base URL only if your Cursor version exposes that control.',
                href: '/docs/cursor',
                icon: 'cursor',
              },
            ],
          },
        ],
      },
      {
        id: 'requirements',
        title: 'Prepare three values',
        blocks: [
          {
            type: 'paragraph',
            text: 'A BOS API Key does not include a model subscription. Your organization configures and authorizes its own LLM service.',
          },
          {
            type: 'table',
            headers: ['Field', 'Enter this value'],
            rows: [
              ['API Key', 'Your full BOS API Key. [Create a Key](/docs/api-keys).'],
              ['Gateway address', '`{{origin}}`'],
              [
                'Model ID',
                'The exact authorized model ID, supplied by your administrator; not a model included with BOS API.',
              ],
            ],
          },
        ],
      },
      {
        id: 'boundaries',
        title: 'Choose the protocol, not a brand name',
        blocks: [
          {
            type: 'paragraph',
            text: 'Codex needs Responses; Claude Code needs Messages; most custom OpenAI-compatible clients use Chat Completions. Client features and model capabilities still need validation with your own upstream.',
          },
        ],
      },
      {
        id: 'verify',
        title: 'Verify the connection',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
    ],
    sources: [],
  },
  {
    slug: 'codex-desktop',
    title: 'Codex Desktop',
    description:
      'Install the desktop app, edit its user configuration, and make the gateway Key available to the app.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'codex',
    protocol: 'responses',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Responses, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install the desktop app',
        blocks: [
          {
            type: 'paragraph',
            text: 'Download the app for your operating system from the [official download page](https://chatgpt.com/download/). If already installed, skip installation and fully quit it before configuring. This guide is for local desktop tasks, not Codex Cloud.',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Open and edit config.toml',
        blocks: [
          {
            type: 'paragraph',
            text: 'macOS: in Finder, press Cmd+Shift+G and enter `~/.codex`. Windows: press Win+R and enter `%USERPROFILE%\\.codex`. Create the folder and `config.toml` if missing. Open it in a text editor, not a word processor.',
          },
          {
            type: 'paragraph',
            text: 'Back up an existing file first. Merge the values below; do not replace the entire file. Put `model` and `model_provider` before the first `[table]`. Replace an existing key/table instead of defining it twice. Make sure the filename is not `config.toml.txt`.',
          },
          {
            type: 'code',
            label: 'config.toml',
            language: 'toml',
            code: '# Merge into your user-level ~/.codex/config.toml.\n# Preserve your existing settings; do not duplicate TOML tables.\nmodel = "{{model}}"\nmodel_provider = "bosapi"\n\n[model_providers.bosapi]\nname = "BOS API"\nbase_url = "{{baseUrl}}"\nenv_key = "BOS_GATEWAY_KEY"\nwire_api = "responses"\nrequires_openai_auth = false',
          },
          {
            type: 'paragraph',
            text: 'For a custom model alias that Codex does not recognize, ask your administrator for matching model catalog metadata. Do not invent context limits or copy another model’s metadata.',
          },
        ],
      },
      {
        id: 'launch',
        title: '4. Supply the Key and restart the app',
        blocks: [
          {
            type: 'paragraph',
            text: 'Choose your system below and replace the Key placeholder locally. On macOS, launchctl makes the value available to newly launched GUI apps for the current login session; repeat after signing out or restarting the computer. On Windows the User value persists; sign out and back in if the app still cannot read it.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS',
                language: 'bash',
                code: 'launchctl setenv BOS_GATEWAY_KEY "YOUR_BOS_API_KEY"',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: '[Environment]::SetEnvironmentVariable("BOS_GATEWAY_KEY", "YOUR_BOS_API_KEY", "User")',
              },
            ],
          },
          {
            type: 'list',
            ordered: true,
            items: [
              'Fully quit the app (Cmd+Q on macOS; also exit the tray app on Windows), then reopen it from the application launcher.',
              'Open a small local project and create a new task. Confirm the configured model; do not reuse an old conversation as the first test.',
            ],
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'troubleshoot',
        title: '6. Common problems and switching back',
        blocks: [
          {
            type: 'table',
            headers: ['Problem', 'What to do'],
            rows: [
              [
                'Still uses the old provider',
                'Restart and create a new task. Check root-level TOML settings and any selected profile.',
              ],
              [
                'Missing environment variable / 401',
                'The Key must reach the process launching Codex. A shell export does not automatically reach a desktop app.',
              ],
              [
                '404 / unsupported model',
                'Use one /v1 suffix and a Responses-compatible authorized model.',
              ],
              [
                'Switch back',
                'Restore your backed-up model/provider settings, remove only your added BOS API settings, then restart.',
              ],
            ],
          },
          {
            type: 'paragraph',
            text: 'Only when switching back: remove this credential override with the command below and restart the app. Restore a prior value if one existed. Do not run this during setup.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS',
                language: 'bash',
                code: 'launchctl unsetenv BOS_GATEWAY_KEY',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: '[Environment]::SetEnvironmentVariable("BOS_GATEWAY_KEY", $null, "User")',
              },
            ],
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: learn.chatgpt.com',
        url: 'https://learn.chatgpt.com/docs/enterprise/connect-to-a-gateway',
      },
      {
        title: 'Official reference: developers.openai.com',
        url: 'https://developers.openai.com/codex/config-advanced/',
      },
    ],
  },
  {
    slug: 'claude-code',
    title: 'Claude Code CLI',
    description:
      'Install Claude Code, configure the current terminal, then start it inside your project.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'claude-code',
    protocol: 'messages',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Anthropic Messages, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install Claude Code',
        blocks: [
          {
            type: 'paragraph',
            text: 'Run the official installer for your system below. Already installed? Skip it. See the [official setup guide](https://code.claude.com/docs/en/setup) for other installation methods.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS / Linux',
                language: 'bash',
                code: 'curl -fsSL https://claude.ai/install.sh | bash',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: 'irm https://claude.ai/install.ps1 | iex',
              },
            ],
          },
          {
            type: 'paragraph',
            text: 'After installation, open a new terminal and check that a version number is printed.',
          },
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'claude --version',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Set the endpoint, Key and model',
        blocks: [
          {
            type: 'paragraph',
            text: 'Replace both placeholders, then run the block for your system. Keep this terminal open; these settings only apply to this terminal and its child processes.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS / Linux',
                language: 'bash',
                code: 'export ANTHROPIC_BASE_URL="{{origin}}"\nexport ANTHROPIC_AUTH_TOKEN="YOUR_BOS_API_KEY"\nexport ANTHROPIC_MODEL="{{model}}"',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: '$env:ANTHROPIC_BASE_URL="{{origin}}"\n$env:ANTHROPIC_AUTH_TOKEN="YOUR_BOS_API_KEY"\n$env:ANTHROPIC_MODEL="{{model}}"',
              },
            ],
          },
          {
            type: 'paragraph',
            text: 'Use the root address without /v1. Your administrator must configure an Anthropic API-key provider with API format anthropic_messages and authorize the selected Claude model. BOS forwards Messages, token counting, streaming, thinking, cache controls, and tool history in the native format.',
          },
        ],
      },
      {
        id: 'launch',
        title: '4. Open your project and launch',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'In the same terminal, enter `cd ` followed by your actual project path (quote paths containing spaces).',
              'Run `claude`. Review any project trust prompt, then use `/status` to inspect the configuration.',
            ],
          },
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'claude',
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'troubleshoot',
        title: '6. Common problems and switching back',
        blocks: [
          {
            type: 'paragraph',
            text: '401: check for a conflicting saved Anthropic credential. Requests to a helper model fail: ask your administrator to authorize the required model roles. To restore the prior setup, close this terminal and open a fresh one; do not delete your entire Claude settings file.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: code.claude.com',
        url: 'https://code.claude.com/docs/en/setup',
      },
      {
        title: 'Official reference: code.claude.com',
        url: 'https://code.claude.com/docs/en/llm-gateway-connect',
      },
    ],
  },
  {
    slug: 'claude-desktop',
    title: 'Claude Desktop',
    description:
      'Configure a gateway in Claude’s own third-party inference window, without a separate switching tool.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'claude-code',
    protocol: 'messages',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Anthropic Messages, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install and open the configuration window',
        blocks: [
          {
            type: 'paragraph',
            text: 'Install or update [Claude Desktop](https://claude.ai/download). In the macOS menu bar, or the Windows application menu, choose Help > Troubleshooting > Enable Developer Mode. After the app restarts, open Developer > Configure Third-Party Inference.',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Fill in Connection',
        blocks: [
          {
            type: 'table',
            headers: ['Field', 'Enter this value'],
            rows: [
              ['Inference provider', 'Gateway'],
              ['Gateway base URL', '`{{origin}}`'],
              ['Credential kind', 'Static API key'],
              ['Gateway API key', 'Your full BOS API Key.'],
              ['Gateway auth scheme', 'Bearer'],
            ],
          },
          {
            type: 'paragraph',
            text: 'Choose a Claude model authorized by your administrator. If automatic discovery does not populate the model list, ask for a validated inferenceModels configuration; do not invent model-role mappings.',
          },
          {
            type: 'note',
            title: 'Before you continue',
            text: 'A managed organization configuration makes this window read-only. Ask its administrator to change it. Desktop does not read ANTHROPIC_BASE_URL or the CLI settings.json for gateway routing.',
            tone: 'warning',
          },
        ],
      },
      {
        id: 'launch',
        title: '4. Apply locally and start a new task',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Use the window’s connection test, then click Apply Changes to save on this device and relaunch the app. You do not need to export a deployment profile.',
              'Create a local Code task, choose an authorized project folder and the configured model. Hosted cloud environments are not available in this gateway mode.',
            ],
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'undo',
        title: '6. Troubleshooting and switching back',
        blocks: [
          {
            type: 'paragraph',
            text: 'Gateway was unreachable: check the root URL and network access. No Developer menu: update the app and confirm Developer Mode is enabled. To restore, reopen the same configuration window and restore your recorded previous settings; ask the administrator if managed. Do not delete the CLI’s configuration.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: code.claude.com',
        url: 'https://code.claude.com/docs/en/llm-gateway-connect#desktop-app',
      },
      {
        title: 'Official reference: claude.com',
        url: 'https://claude.com/docs/third-party/claude-desktop/gateway',
      },
      {
        title: 'Official reference: claude.com',
        url: 'https://claude.com/docs/third-party/claude-desktop/in-app-configuration',
      },
    ],
  },
  {
    slug: 'codex',
    title: 'Codex CLI',
    description:
      'Install the command-line client and connect it with an explicit Responses provider.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'codex',
    protocol: 'responses',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Responses, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install Codex CLI',
        blocks: [
          {
            type: 'paragraph',
            text: 'For npm commands, install a supported [Node.js LTS](https://nodejs.org/en/download) first. Open Terminal on macOS/Linux or PowerShell on Windows; run commands there, not in the browser.',
          },
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'npm install -g @openai/codex\ncodex --version',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Open and edit config.toml',
        blocks: [
          {
            type: 'paragraph',
            text: 'Linux users: create or open `~/.codex/config.toml` in your text editor. macOS and Windows paths follow below.',
          },
          {
            type: 'paragraph',
            text: 'macOS: in Finder, press Cmd+Shift+G and enter `~/.codex`. Windows: press Win+R and enter `%USERPROFILE%\\.codex`. Create the folder and `config.toml` if missing. Open it in a text editor, not a word processor.',
          },
          {
            type: 'paragraph',
            text: 'Back up an existing file first. Merge the values below; do not replace the entire file. Put `model` and `model_provider` before the first `[table]`. Replace an existing key/table instead of defining it twice. Make sure the filename is not `config.toml.txt`.',
          },
          {
            type: 'code',
            label: 'config.toml',
            language: 'toml',
            code: '# Merge into your user-level ~/.codex/config.toml.\n# Preserve your existing settings; do not duplicate TOML tables.\nmodel = "{{model}}"\nmodel_provider = "bosapi"\n\n[model_providers.bosapi]\nname = "BOS API"\nbase_url = "{{baseUrl}}"\nenv_key = "BOS_GATEWAY_KEY"\nwire_api = "responses"\nrequires_openai_auth = false',
          },
          {
            type: 'paragraph',
            text: 'For a custom model alias that Codex does not recognize, ask your administrator for matching model catalog metadata. Do not invent context limits or copy another model’s metadata.',
          },
        ],
      },
      {
        id: 'launch',
        title: '4. Set the Key and start Codex',
        blocks: [
          {
            type: 'paragraph',
            text: 'Enter your actual project directory first. Replace the Key and run the commands in the same terminal. These values are session-scoped; set them again in a new terminal.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS / Linux',
                language: 'bash',
                code: 'export BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"\ncodex',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: '$env:BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"\ncodex',
              },
            ],
          },
          {
            type: 'paragraph',
            text: 'Inside Codex, run `/status` to check the provider and model before your first task. WSL uses its Linux home directory, not the Windows desktop configuration.',
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'troubleshoot',
        title: '6. Common problems and switching back',
        blocks: [
          {
            type: 'table',
            headers: ['Problem', 'What to do'],
            rows: [
              [
                'Still uses the old provider',
                'Restart and create a new task. Check root-level TOML settings and any selected profile.',
              ],
              [
                'Missing environment variable / 401',
                'The Key must reach the process launching Codex. A shell export does not automatically reach a desktop app.',
              ],
              [
                '404 / unsupported model',
                'Use one /v1 suffix and a Responses-compatible authorized model.',
              ],
              [
                'Switch back',
                'Restore your backed-up model/provider settings, remove only your added BOS API settings, then restart.',
              ],
            ],
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: learn.chatgpt.com',
        url: 'https://learn.chatgpt.com/docs/enterprise/connect-to-a-gateway',
      },
      {
        title: 'Official reference: developers.openai.com',
        url: 'https://developers.openai.com/codex/config-advanced/',
      },
    ],
  },
  {
    slug: 'cline-roo',
    title: 'VS Code + Cline / Roo Code',
    description: 'Install the extension, choose OpenAI Compatible, and configure each task mode.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'cline-roo',
    protocol: 'chat',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Chat Completions, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install the extension',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Install [VS Code](https://code.visualstudio.com/download), open Extensions (Ctrl+Shift+X / Cmd+Shift+X), and install Cline or Roo Code from its official publisher.',
              'Open a small project folder, open the extension from the activity bar, then click its settings gear.',
            ],
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Fill in the provider settings',
        blocks: [
          {
            type: 'paragraph',
            text: 'Choose API Provider > OpenAI Compatible, not OpenAI. Enter the fields below. Set context size, image input and native tool support only to values your model actually supports.',
          },
          {
            type: 'table',
            headers: ['Field', 'Enter this value'],
            rows: [
              ['Provider name', 'BOS API'],
              ['Base URL / API endpoint', '`{{baseUrl}}`'],
              [
                'API Key',
                'Your full BOS API Key, not your account password or upstream credential.',
              ],
              ['Model ID', 'Replace `YOUR_MODEL_ID` with the exact ID authorized for your Key.'],
            ],
          },
        ],
      },
      {
        id: 'select',
        title: '4. Save and choose the active profile',
        blocks: [
          {
            type: 'paragraph',
            text: 'Click Done / Save and start a new task. If Plan and Act modes use separate API settings, select BOS API in both. Roo Code needs native tool calling; a text-only model is not enough.',
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'undo',
        title: '6. Troubleshooting and undo',
        blocks: [
          {
            type: 'paragraph',
            text: '401: verify the full Key. 404: check /v1 appears once. Tool errors: verify native tool capability. To undo, switch to the previous provider profile; do not disable tool approvals.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: docs.cline.bot',
        url: 'https://docs.cline.bot/provider-config/openai-compatible',
      },
      {
        title: 'Official reference: docs.roocode.com',
        url: 'https://docs.roocode.com/providers/openai-compatible',
      },
    ],
  },
  {
    slug: 'openclaw',
    title: 'OpenClaw',
    description: 'Use the onboarding wizard to configure a custom endpoint and model.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'openclaw',
    protocol: 'chat',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Chat Completions, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install and open the wizard',
        blocks: [
          {
            type: 'paragraph',
            text: 'Run the official installer for your system; skip installation if already installed. Check current runtime requirements in the [official guide](https://docs.openclaw.ai/start/getting-started). Back up existing configuration before rerunning onboarding.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS / Linux',
                language: 'bash',
                code: 'curl -fsSL https://openclaw.ai/install.sh | bash',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: 'iwr -useb https://openclaw.ai/install.ps1 | iex',
              },
            ],
          },
          {
            type: 'paragraph',
            text: 'If onboarding opens automatically, choose Custom setup rather than reusing detected account credentials. To open the step-by-step wizard again:',
          },
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'openclaw onboard --classic',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Answer the connection prompts',
        blocks: [
          {
            type: 'paragraph',
            text: 'Read the tool-access notice before accepting it. In Model/Auth choose Custom Provider (under More if necessary). This example uses an upstream authorized for Chat Completions.',
          },
          {
            type: 'table',
            headers: ['Field', 'Enter this value'],
            rows: [
              ['API Base URL', '`{{baseUrl}}`'],
              ['API Key', 'Paste your full BOS API Key.'],
              ['Endpoint compatibility', 'OpenAI-compatible / Chat Completions'],
              ['Model ID', '`{{model}}`'],
            ],
          },
          {
            type: 'paragraph',
            text: 'Wait for the wizard’s verification result. For your first connection you can skip optional messaging channels and skills; do not enable remote access just to test a model.',
          },
        ],
      },
      {
        id: 'launch',
        title: '4. Open the chat interface',
        blocks: [
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'openclaw dashboard',
          },
          {
            type: 'paragraph',
            text: 'Select the model you just configured and start a new conversation. If the local service is not running, follow the startup instruction shown by the wizard first.',
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'undo',
        title: '6. Troubleshooting and undo',
        blocks: [
          {
            type: 'paragraph',
            text: 'Verification failure: check protocol, /v1 suffix, Key and model independently. Rerun onboarding to select your previous provider, or restore your backup. Do not troubleshoot by exposing the dashboard publicly.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: docs.openclaw.ai',
        url: 'https://docs.openclaw.ai/start/wizard',
      },
    ],
  },
  {
    slug: 'opencode',
    title: 'OpenCode',
    description:
      'Install OpenCode, save a user-level provider configuration, and select the model.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'opencode',
    protocol: 'chat',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Chat Completions, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install OpenCode',
        blocks: [
          {
            type: 'paragraph',
            text: 'For npm commands, install a supported [Node.js LTS](https://nodejs.org/en/download) first. Open Terminal on macOS/Linux or PowerShell on Windows; run commands there, not in the browser.',
          },
          {
            type: 'code',
            label: 'Terminal / PowerShell',
            language: 'bash',
            code: 'npm install -g opencode-ai\nopencode --version',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Edit the user configuration',
        blocks: [
          {
            type: 'paragraph',
            text: 'Create or open `~/.config/opencode/opencode.json` (Windows: `%USERPROFILE%\\.config\\opencode\\opencode.json`). Back up existing content, merge the provider entry and default model below, and replace YOUR_MODEL_ID in both places. Do not overwrite other providers.',
          },
          {
            type: 'code',
            label: 'opencode.json',
            language: 'json',
            code: '{\n  "$schema": "https://opencode.ai/config.json",\n  "model": "bosapi/{{model}}",\n  "provider": {\n    "bosapi": {\n      "npm": "@ai-sdk/openai-compatible",\n      "name": "BOS API",\n      "options": {\n        "baseURL": "{{baseUrl}}",\n        "apiKey": "{env:BOS_GATEWAY_KEY}"\n      },\n      "models": {\n        "{{model}}": { "name": "{{model}}" }\n      }\n    }\n  }\n}',
          },
        ],
      },
      {
        id: 'select',
        title: '4. Supply the Key and choose the model',
        blocks: [
          {
            type: 'paragraph',
            text: 'Open a terminal in your project directory and run the block for your system. In OpenCode, enter `/models` and choose `bosapi/YOUR_MODEL_ID` with your actual ID.',
          },
          {
            type: 'tabs',
            tabs: [
              {
                label: 'macOS / Linux',
                language: 'bash',
                code: 'export BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"\nopencode',
              },
              {
                label: 'Windows PowerShell',
                language: 'powershell',
                code: '$env:BOS_GATEWAY_KEY="YOUR_BOS_API_KEY"\nopencode',
              },
            ],
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'undo',
        title: '6. Troubleshooting and undo',
        blocks: [
          {
            type: 'paragraph',
            text: 'Provider missing: check that the configuration uses `provider`, `npm` and `options`, and that the file is valid JSON. 401: set the Key in the launching terminal. To undo, restore the previous default model and remove only the bosapi entry.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: opencode.ai',
        url: 'https://opencode.ai/docs/providers/',
      },
      {
        title: 'Official reference: opencode.ai',
        url: 'https://opencode.ai/docs/config/',
      },
    ],
  },
  {
    slug: 'cursor',
    title: 'Cursor',
    description: 'Use a custom OpenAI Base URL only if your Cursor version exposes that control.',
    group: 'Coding agents',
    audience: 'Developers',
    icon: 'cursor',
    protocol: 'chat',
    sections: [
      {
        id: 'requirements',
        title: '1. Prepare your connection details',
        blocks: [
          {
            type: 'paragraph',
            text: 'You need a BOS API Key and a model authorized for Chat Completions, including streaming and tool calls. Ask your administrator if either is missing. BOS API does not supply model accounts or subscriptions.',
          },
          {
            type: 'paragraph',
            text: 'Once configured by the administrator, examples use this instance’s API address. If you see `YOUR_API_ADDRESS`, ask the administrator to configure it first. Replace `YOUR_BOS_API_KEY` and `YOUR_MODEL_ID` on your computer. [How to create a Key](/docs/api-keys).',
          },
        ],
      },
      {
        id: 'install',
        title: '2. Install and open model settings',
        blocks: [
          {
            type: 'paragraph',
            text: 'Install [Cursor](https://cursor.com/downloads), then open Cursor Settings > Models. Look for the OpenAI API Key and Override OpenAI Base URL controls.',
          },
        ],
      },
      {
        id: 'configure',
        title: '3. Set the custom endpoint first',
        blocks: [
          {
            type: 'note',
            title: 'Before you continue',
            text: 'If there is no Base URL override, stop here and use Cline or OpenCode. A provider Key field alone may send your Key to the official vendor rather than BOS API.',
            tone: 'warning',
          },
          {
            type: 'table',
            headers: ['Field', 'Enter this value'],
            rows: [
              ['Provider name', 'BOS API'],
              ['Base URL / API endpoint', '`{{baseUrl}}`'],
              [
                'API Key',
                'Your full BOS API Key, not your account password or upstream credential.',
              ],
              ['Model ID', 'Replace `YOUR_MODEL_ID` with the exact ID authorized for your Key.'],
            ],
          },
          {
            type: 'paragraph',
            text: 'Enable the URL override before entering your Key, add the authorized model ID, and verify the provider.',
          },
        ],
      },
      {
        id: 'select',
        title: '4. Select the model in chat',
        blocks: [
          {
            type: 'paragraph',
            text: 'Create a new chat and select the added model instead of Auto. Cursor routes requests via its backend, so localhost-only gateways cannot be reached by that backend. Custom keys do not configure Cursor Tab or every hosted feature.',
          },
        ],
      },
      {
        id: 'verify',
        title: '5. Check that the request reached BOS API',
        blocks: [
          {
            type: 'list',
            ordered: true,
            items: [
              'Start a new conversation and send: “Reply with exactly BOS_OK. Do not use tools.” A text response checks basic connectivity.',
              'Open BOS API > Usage Details or Error Logs for your Key. Match the time and model of this request; a client’s Connected label alone is not proof.',
              'In a small project, ask the agent to read one file without editing it. Approve that operation, then check streaming output and the answer. Test edits separately.',
            ],
          },
          {
            type: 'paragraph',
            text: 'If text works but tools fail, check model capabilities and the error Request ID. Keep normal approval prompts enabled. [Troubleshooting](/docs/troubleshooting).',
          },
        ],
      },
      {
        id: 'undo',
        title: '6. Restore the previous setup',
        blocks: [
          {
            type: 'paragraph',
            text: 'Restore the prior Base URL and model selection, and remove the BOS API Key from this provider. Only use a hosted client if your organization permits sending its Key and requests through that service.',
          },
        ],
      },
    ],
    sources: [
      {
        title: 'Official reference: cursor.com',
        url: 'https://cursor.com/help/models-and-usage/api-keys',
      },
    ],
  },
  {
    slug: 'troubleshooting',
    title: 'Errors & troubleshooting',
    group: 'Help',
    icon: 'help',
    audience: 'All users',
    description:
      'Find the earliest failing step, collect a Request ID, and fix the cause instead of retrying blindly.',
    sections: [
      {
        id: 'setup-problems',
        title: 'Stuck before your first request?',
        blocks: [
          {
            type: 'table',
            headers: ['Symptom', 'Next step'],
            rows: [
              [
                'Cannot find Register',
                'The current console has no self-service registration. Ask the administrator for an account or gateway Key.',
              ],
              [
                'Missing menu / disabled Create button / 403',
                'Check the role shown next to your account. Organization and administrator provisioning require super_admin. An org_admin manages its organization’s Keys and can view assigned channels. Super administrators maintain shared channels.',
              ],
              [
                'Organization administrator cannot edit a public provider',
                'Shared channels are maintained by super administrators. Organization administrators can view the channels assigned to their organization.',
              ],
              [
                'No organization, provider, or model to select',
                'Ask a super administrator to finish organization creation, public provider assignment, and upstream model discovery. Reload after the setup is complete.',
              ],
              [
                'A new organization has no users',
                'A Key requires an organization, not a user. A super administrator can create its Key immediately; provision an org_admin separately only when console access is needed.',
              ],
              [
                'A channel has models but the Key model selector is empty',
                'Check the selected organization and channels, active model status, and any loading error. Ask a super administrator to configure shared channels, discover their models, and assign access to your organization. Retry the failed load if shown.',
              ],
              [
                'An old Key has no usable models after an upgrade',
                'Empty Key lists inherit organization/channel access. Review the organization’s explicit public assignments and model restrictions. An organization with no public assignments does not receive global channels automatically.',
              ],
              [
                'Key created but quota already exhausted',
                'Check for a budget entered as 0. A blank budget and a zero budget are different; request the intended positive budget, not an arbitrary increase.',
              ],
              [
                'Example still says YOUR_MODEL_ID',
                'YOUR_MODEL_ID is a placeholder. Replace it in your local copy with the exact model ID authorized for your Key.',
              ],
              [
                'export / curl / python command not found',
                'Use a terminal with the required runtime. The shell snippets use macOS/Linux/WSL syntax, not PowerShell. Start with one cURL example rather than all languages.',
              ],
            ],
          },
        ],
      },
      {
        id: 'subscription-problems',
        title: 'Subscription authorization problems',
        blocks: [
          {
            type: 'table',
            headers: ['Symptom', 'Next step'],
            rows: [
              [
                'Official sign-in tab does not open',
                'Use Open official sign-in page or copy the authorization link from the form. Check the browser popup setting and keep the original form open.',
              ],
              [
                'localhost shows ERR_CONNECTION_REFUSED',
                'For OpenAI or Antigravity, copy the full callback address including code and state, paste it into the original form, and save. The callback port does not need to be published on the deployment host.',
              ],
              [
                'Expired link, failed exchange, or mismatched session',
                'Start sign-in again with the same administrator and platform and use only the new result in that form. Sessions expire after 10 minutes, can be exchanged once, and are cleared by backend restarts.',
              ],
              [
                'Channel saved, but no models are available',
                'Retry discovery or edit the saved channel to add models available to that account. Gemini discovery retains manual model IDs. Check model status, scheduling, organization access, and Key restrictions before making a request.',
              ],
              [
                'A previously connected account no longer works',
                'Check the sanitized upstream error and subscription access. BOS refreshes tokens automatically; if authorization is revoked or refresh fails, edit the existing channel and restart official sign-in, then save the new result.',
              ],
            ],
          },
        ],
      },
      {
        id: 'google-project-errors',
        title: 'Google project and permission errors',
        blocks: [
          {
            type: 'table',
            headers: ['Symptom', 'Next step'],
            rows: [
              [
                'No managed project or multiple Cloud projects',
                'Enter a Google Cloud Project ID accessible to the signed-in account in the Gemini or Antigravity authorization form, then start a new sign-in. BOS only discovers existing projects; it does not provision them.',
              ],
              [
                'SERVICE_DISABLED',
                'Ask the project administrator to enable the Gemini for Google Cloud API for the selected project, then restart sign-in.',
              ],
              [
                'ACCESS_TOKEN_SCOPE_INSUFFICIENT',
                'Restart sign-in and grant the required Google permissions. Confirm that the account can access the intended project.',
              ],
              [
                'BILLING_DISABLED',
                'Ask the project administrator to check billing for the selected project, then restart sign-in after it is resolved.',
              ],
              [
                'CONSUMER_INVALID',
                'Check the Google Cloud Project ID and the signed-in account, then restart sign-in with the corrected project.',
              ],
              [
                'Project initialization timed out or Google returned a permission/network error',
                'Check the reported action and sanitized status, server connectivity to Google, account eligibility, and project access. After resolving the cause, start a new authorization session. Repeated saving does not fix project permissions.',
              ],
            ],
          },
        ],
      },
      {
        id: 'client-import-problems',
        title: 'CC Switch / Vary AI import problems',
        blocks: [
          {
            type: 'table',
            headers: ['Symptom', 'Next step'],
            rows: [
              [
                'The local app does not open',
                'Confirm that the selected app is installed on the browser’s computer and supports provider import links. Check the browser’s external-app prompt, then retry the import.',
              ],
              [
                'No authenticator or rejected verification code',
                'Open Two-Factor Authentication in the account menu to enroll first. Use the current administrator’s authenticator code, check the device clock, and retry with the next code. Email/provisioning OTPs do not work here.',
              ],
              [
                'The imported address is wrong',
                'Ask a super administrator to check Settings > API Address. Import again after saving; an existing client configuration is not rewritten automatically. Claude Code uses the gateway root; Codex uses the /v1 Base URL.',
              ],
              [
                'The app opens but the model request fails',
                'Confirm the import inside the app and select an authorized model. Check the Key, organization/channel access, protocol, and quotas. Importing a credential does not grant additional model access.',
              ],
            ],
          },
        ],
      },
      {
        id: 'checklist',
        title: 'Start with four checks',
        blocks: [
          {
            type: 'list',
            items: [
              'Address: confirm the gateway host and whether the SDK needs `/v1`.',
              'Credential: use the complete BOS API Key, without spaces or masking. Confirm the organization and Key are active.',
              'Model: copy its exact ID and verify access and protocol support.',
              'Evidence: record the HTTP status, `error.code`, timestamp, and `X-Request-ID`.',
            ],
            ordered: true,
          },
        ],
      },
      {
        id: 'errors',
        title: 'Common errors',
        blocks: [
          {
            type: 'table',
            headers: ['Signal', 'Likely cause', 'Next action'],
            rows: [
              [
                '401 / unauthorized',
                'Missing or unusable Key; inactive organization; IP policy; no usable provider.',
                'Check the full Key, active state, caller IP, and assigned provider with your administrator.',
              ],
              [
                'model_not_allowed',
                'Requested model is outside the effective access policy.',
                'Check model spelling, organization access, and provider bindings.',
              ],
              [
                'rate_limited',
                'A request-rate limit is exhausted.',
                'Reduce concurrency; use bounded exponential backoff and honor Retry-After.',
              ],
              [
                'quota_exceeded',
                'A token or amount budget/window is exhausted.',
                'Review the relevant quota and recent usage; request a limit change if appropriate.',
              ],
              [
                'security_blocked',
                'A security policy rejected content or a tool operation.',
                'Review the security event and rule with an administrator.',
              ],
              [
                'provider_unavailable / upstream_error',
                'The selected upstream could not serve the request.',
                'Provide the Request ID and model to your administrator.',
              ],
              [
                '404 / wrong response format',
                'Wrong path, wrong host, or protocol mismatch.',
                'Check the SDK Base URL and use the correct protocol parser.',
              ],
            ],
          },
        ],
      },
      {
        id: 'agents',
        title: 'The agent chats but cannot use tools',
        blocks: [
          {
            type: 'paragraph',
            text: 'Check that the selected model and provider support the tool protocol required by the client, including streaming tool arguments and follow-up tool results. Reproduce with one file in a small test project. Do not assume a working text reply proves agent compatibility.',
          },
        ],
      },
      {
        id: 'retry-after',
        title: 'Understand a 429 response',
        blocks: [
          {
            type: 'paragraph',
            text: 'Gateway 429 responses include `error.reason` (also in `X-RateLimit-Reason`) and `error.retryable`. QPS reasons are `global_qps`, `organization_qps`, and `key_qps`; window reasons are `quota_5h`, `quota_1d`, and `quota_7d`. Key budget reasons identify the period and unit, such as `daily_tokens` or `total_amount`. Upstream rejections use `upstream_rate_limit` or `upstream_quota`.',
          },
          {
            type: 'paragraph',
            text: 'When `error.retryable` is true, wait at least the seconds in `Retry-After`. Daily, weekly, and monthly budgets reset using the UTC+8 business calendar. Total and zero budgets return `error.retryable: false` without Retry-After; ask an administrator to adjust the budget. Upstream retry delays are honored; an upstream rate limit without a delay uses a one-second minimum backoff. An explicit upstream quota error without a reset delay is not automatically retryable.',
          },
        ],
      },
      {
        id: 'support',
        title: 'What to send to your administrator',
        blocks: [
          {
            type: 'list',
            items: [
              'Client name and version; whether the problem affects cURL too.',
              'Gateway hostname, endpoint, exact model ID, and approximate request time.',
              'Request ID, HTTP status, and sanitized error code/message.',
              'A minimal reproduction with all credentials and private content removed.',
            ],
          },
          {
            type: 'note',
            title: 'Never attach full credentials',
            text: 'Do not include Keys, passwords, OTPs, Authorization headers, or provider secrets in tickets or screenshots.',
            tone: 'warning',
          },
        ],
      },
    ],
  },
];

export const docGroups = [...new Set(docPages.map(page => page.group))];
