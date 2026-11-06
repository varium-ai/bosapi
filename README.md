<p align="center">
  <a href="https://bosapi.net/" title="BOS API official website">
    <img src="assets/readme/banner.png" width="1200" alt="BOS API — Your models. One gateway. A self-hosted gateway for model access, content checks, and usage accounting.">
  </a>
</p>

<p align="center">
  A self-hosted AI gateway for your team.<br>
  Connect model services. Define access. Understand usage.
</p>

<p align="center">
  <code>Go + React + TypeScript</code> &nbsp; <code>Docker Compose</code> &nbsp; <code>Community edition</code>
</p>

<p align="center">
  <a href="https://github.com/varium-ai/bosapi"><strong>GitHub</strong></a> &nbsp;·&nbsp;
  <a href="https://bosapi.net/"><strong>Official website</strong></a> &nbsp;·&nbsp;
  <a href="#quick-start"><strong>Get started</strong></a> &nbsp;·&nbsp;
  <a href="#console-preview">Console preview</a> &nbsp;·&nbsp;
  <a href="#community-security">Security</a> &nbsp;·&nbsp;
  <a href="#documentation">Documentation</a> &nbsp;·&nbsp;
  <a href="#contributing">Contribute</a>
</p>

BOS API brings upstream model connections, organization permissions, API Keys,
content checks, and usage records into one gateway and administration console.
Deploy it in your own environment and connect the model services you use.
Operators supply their own upstream accounts, credentials, and model credits.

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-4f46e5?style=flat-square" alt="License: MIT"></a>
</p>

## Console preview

### A shared view of your AI workspace

Review requests, tokens, recorded costs, resource counts, runtime health, and
recent security events from the operations overview.

![BOS API operations overview with synthetic demonstration data](assets/readme/console-overview.jpg)

<p align="center"><sub>Actual community console, captured locally with synthetic demonstration data.</sub></p>

<details>
<summary><strong>Explore security events</strong> — credential findings, code markers, and request correlation</summary>

Inspect rule names, request IDs, actions, and source metadata. Event details
provide bounded, redacted evidence for administrator review.

![BOS API security events with synthetic demonstration data](assets/readme/security-events.jpg)

<p align="center"><sub>Example observations only. A rule match does not establish malicious execution or disclosure.</sub></p>

</details>

## Built for shared model access

<table>
  <tr>
    <td width="33%" valign="top">
      <strong>Connect your stack</strong>
      <p>Manage upstream channels, credentials, and model access behind a self-hosted gateway. Use supported OpenAI, Anthropic, and Gemini protocol interfaces.</p>
    </td>
    <td width="33%" valign="top">
      <strong>Define team boundaries</strong>
      <p>Assign models and channels by organization. Issue API Keys with access policies, quotas, rate limits, and separate inspection controls.</p>
    </td>
    <td width="33%" valign="top">
      <strong>See how AI is used</strong>
      <p>Review token categories, cache usage, recorded costs, request diagnostics, and security events in one administration console.</p>
    </td>
  </tr>
</table>

Protocol support includes OpenAI Chat Completions and Responses, Anthropic
Messages, and Gemini interfaces. Available models and conversion behavior depend
on the configured upstream channel and its capabilities.

**Community capacity: 2 organizations and 10 API Keys per installation.**

<details>
<summary>Capacity rules and included capabilities</summary>

| Area                | Community capabilities                                                                                           |
| ------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Administration      | MFA, login lockout, role-based access, and organization isolation                                                |
| Upstream access     | Shared channels, model assignments, API credentials, and supported subscription flows                            |
| API Keys            | Creation, import, duplication, rotation, model policies, quotas, and rate limits                                 |
| Observability       | Usage and cost records, request diagnostics, runtime health, and security events                                 |
| Built-in inspection | Fixed credential checks, current-input redaction, output code-marker observations, and tool authorization checks |
| Deployment          | Source-built Docker Compose, optional HTTPS, monitoring, and manual upgrades                                     |

The initial organization and disabled resources count toward capacity. Removing
an organization disables it and keeps its slot; deleting an API Key releases its
slot. Rotating a Key does not consume another slot. Imports and batch creation
reject the entire batch if it would exceed capacity. Creation requests over
either limit return HTTP 409. Reaching capacity does not delete existing data.

Private channels, configurable automatic-routing and aggregate-model rules,
custom security rules, security event Webhooks, usage report jobs and delivery,
online system updates,
and blue/green or HA deployment controllers are excluded. Authentication
notification delivery remains available.

</details>

## Quick start

### 1. Start your instance

Use a new instance with Docker Engine, Compose v2, Buildx, Bash, OpenSSL, and
curl. From the root of your source checkout, run:

```bash
git clone https://github.com/varium-ai/bosapi.git
cd bosapi
./deploy/stack.sh up --source
./deploy/stack.sh status
curl --fail --silent --show-error http://127.0.0.1:18080/health/ready
```

The first run builds the frontend and Go backend, starts MySQL and Redis,
applies migrations, and creates the initial administrator. The Go application
serves the frontend by default.

### 2. Configure your workspace

Open **http://127.0.0.1:18080/login** on the deployment host. Retrieve the initial
administrator credentials privately from `deploy/stack.env` and complete MFA
enrollment. This generated file has mode `600` and must not be committed or
shared.

Add an upstream channel, assign its models to an organization, and create a
gateway API Key. A healthy gateway still needs a configured model service and
upstream credits before it can answer model requests.

### 3. Make a request

Replace the example Key and model ID with values from your instance:

```bash
export BOS_GATEWAY_KEY="YOUR_GATEWAY_API_KEY"

curl --fail-with-body "http://127.0.0.1:18080/v1/chat/completions" \
  -H "Authorization: Bearer $BOS_GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "YOUR_MODEL_ID",
    "messages": [
      {"role": "user", "content": "Say hello in one sentence."}
    ]
  }'
```

For remote administration, keep the default loopback bind and use an SSH tunnel:

```bash
ssh -L 18080:127.0.0.1:18080 deploy-user@server.example.com
```

For public HTTPS, monitoring, and alternative frontend delivery, follow the
[deployment guide](deploy/README.md). The example host and username are
placeholders. The bundled Docker installer supports Ubuntu 26.04; configure
Docker separately on other operating systems.

## Community security

Built-in inspection runs locally and retains fixed credential checks, redaction
of the current user input, model-output code markers, and tool authorization
checks. Administrators can review events and redacted evidence within their
permitted organization scope.

| Control                  | Behavior                                                                                                 |
| ------------------------ | -------------------------------------------------------------------------------------------------------- |
| Request inspection       | Checks supported current-user-input fields and redacts credential-like values before upstream forwarding |
| Response and tool checks | Observes supported output code markers and checks tool content and declared tool authorization           |
| Inspection controls      | Separate upstream and downstream controls per API Key; the default global tool mode is `observe`         |
| Evidence handling        | At most eight evidence items per event, redacted at queue and read boundaries, expiring after seven days |

Upstream credential redaction still applies in observe mode when upstream
inspection is enabled. Event metadata has a separate lifecycle, and events are
queued for best-effort local persistence. Raw tool arguments and results are not
stored as event payloads.

The proprietary injection signatures, obfuscation matching, and targeted encoded
fragment detection are excluded. The public advanced-detector interface uses a
no-op implementation. This community profile does not provide complete
bidirectional credential leak prevention or model-based security analysis.

## Configuration and operation

Run `./deploy/stack.sh init` before startup when you need to edit configuration.
Use the [environment example](deploy/stack.env.example) as a reference. Keep the
generated database passwords, JWT signing key, and encryption key across updates.

<details>
<summary>Environment settings and optional services</summary>

| Setting                              | Purpose                                                           |
| ------------------------------------ | ----------------------------------------------------------------- |
| `BOSAPI_PORT`                        | Loopback HTTP port; default `18080`                               |
| `BOS_PRIMARY_DOMAIN`                 | Your public HTTPS gateway URL, when configured                    |
| `BOS_WEB_DELIVERY`                   | Frontend delivery; `go` by default, with optional `nginx` hosting |
| `BOS_ALLOWED_UPSTREAM_HOSTS`         | Explicit exceptions for trusted upstream hosts                    |
| `BOS_ENFORCE_ADMIN_MFA`              | Administrator MFA enforcement; enabled unless explicitly disabled |
| `BOS_USAGE_REPORTING_FORCE_DISABLED` | Set to `true` to prevent optional installation reporting          |

Gemini and Antigravity subscription sign-in and token refresh require an
operator-supplied OAuth client authorized for the corresponding upstream service.
Configure both `BOS_GEMINI_OAUTH_CLIENT_ID` and `BOS_GEMINI_OAUTH_CLIENT_SECRET`,
or the matching `BOS_ANTIGRAVITY_OAUTH_CLIENT_ID` and
`BOS_ANTIGRAVITY_OAUTH_CLIENT_SECRET`, before using that subscription flow.
Missing or incomplete client configuration is rejected before a network request.

Installation telemetry is enabled on first startup when no saved choice exists.
Saved opt-outs survive restarts. Settings includes its controls and payload
preview; the fixed receiver is documented in
[`usage_reporting.json`](backend/internal/gateway/application/usage_reporting.json).
Only random deployment identifiers, build version, platform and daily running
heartbeats are sent; business logs, user content and credentials are excluded.
Set `BOS_USAGE_REPORTING_FORCE_DISABLED=true` before first startup to prevent
initialization and sending, or disable telemetry in Settings. Missing or invalid
receiver/storage configuration prevents sending. Telemetry is separate from
excluded usage report jobs.

No GeoLite2 database is bundled. IP activity remains available without location
enrichment. An operator may mount a separately licensed `.mmdb.gz` database and
set `BOS_GEOIP_DB` to its container path.

</details>

<details>
<summary>Updates, backups, and database scope</summary>

Back up MySQL, encrypted gateway data, and `deploy/stack.env` before updating.
Select a reviewed community version, then run `./deploy/stack.sh up --source`.
The `up` command does not fetch Git; it preserves credentials and applies pending
migrations. The `down` command stops services without deleting named volumes.

This community snapshot has a separate initial schema and is intended for a new
database. Do not point it at an existing commercial installation: migration
checksums differ. Cross-edition migration needs a separately reviewed plan.

</details>

## Documentation

| Topic                           | Guide                                                                                           |
| ------------------------------- | ----------------------------------------------------------------------------------------------- |
| Product and official guides     | [BOS API website](https://bosapi.net/) · [Official documentation](https://bosapi.net/docs/)     |
| Community scope and capacity    | [Included and excluded capabilities](COMMUNITY_SCOPE.md)                                        |
| Self-hosted deployment          | [Deployment guide](deploy/README.md) · [Environment reference](deploy/stack.env.example)        |
| Backend development             | [Go backend](backend/README.md)                                                                 |
| Frontend development            | [React console](frontend/README.md)                                                             |
| Contributions                   | [Contributing guide](CONTRIBUTING.md)                                                           |
| Vulnerability reporting         | [Security policy](SECURITY.md)                                                                  |
| Source and dependency licensing | [Source and dependency licenses](LICENSING.md) · [Third-party notices](THIRD_PARTY_NOTICES.txt) |

The running console also includes Help & documentation. Official product guides
may describe additional capabilities; the community scope is defined above.

## Development

The backend uses **Go 1.26**. The frontend uses **Node.js 22** and **pnpm 9.15.9**.
See the [contributing guide](CONTRIBUTING.md) for the development workflow.

<details>
<summary>Checks and local builds</summary>

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm run check
pnpm run build

cd ../backend
go test -race ./...
go vet ./...
go build ./...

cd ..
node tools/check_repository_language.cjs
python3 tools/check_community_source.py
```

Tests requiring MySQL, Redis, or live upstream access need explicit test
configuration. Developer documentation, comments, and diagnostics use English;
product UI localization and Unicode/security test fixtures are retained.

</details>

## Contributing

For a change request, describe the problem, expected behavior, and how you
verified the result. Keep patches focused and pair behavior changes with updated
documentation. Contributions and translations follow
[CONTRIBUTING.md](CONTRIBUTING.md). Handle sensitive reports through
[SECURITY.md](SECURITY.md).

### License

BOS API's original source code is licensed under the [MIT License](LICENSE).
Third-party dependencies and product marks retain their own terms; see
[licensing notes](LICENSING.md) and [third-party notices](THIRD_PARTY_NOTICES.txt).

<p align="center">
  <a href="https://bosapi.net/"><strong>BOS API</strong></a><br>
  <sub>Your models. One gateway. On your infrastructure.</sub>
</p>
