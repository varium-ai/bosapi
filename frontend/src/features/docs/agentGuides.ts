import type { DocPage } from './types';

const guides: DocPage[] = [
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
];

export function getAgentGuides(): DocPage[] {
  return guides;
}
