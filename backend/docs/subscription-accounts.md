# Official subscription accounts

BOS supports browser OAuth sign-in for ChatGPT/Codex, Claude, Gemini CLI /
Code Assist, and Antigravity accounts. These accounts are stored in the same `accounts` table as
API-key channels and participate in organization bindings, model restrictions,
gateway keys, request inspection, quotas, and usage recording.

## Connect an account

1. Open **Upstream Providers**, choose **Add Provider**, and enter a name.
2. Select OpenAI, Anthropic, Gemini, or Antigravity and **Official subscription (OAuth)**.
   The official endpoint and protocol are filled automatically.
   New channels default to subscription sign-in. API-key and upstream-proxy
   channels retain their separate credential forms.
3. Choose **Start official account sign-in** to open the official site in a new
   tab. You can also **Copy authorization link** and send it to the account holder;
   they do not need access to BOS Admin.
4. Ask the account holder to return the complete callback URL or displayed code,
   and paste it into the same BOS form that generated the link. Keep this form open.
   Claude also accepts `code#state`. If the browser lands on an unreachable
   `localhost:1455` page after OpenAI sign-in, copy the full address anyway.
   Antigravity uses `localhost:8085/callback` with the same copy/paste flow.
   The deployment does not need to publish a callback port.
   `ERR_CONNECTION_REFUSED` on that localhost page means there is no local callback
   listener. This alone does not indicate an OAuth denial: copy the entire address
   including `code` and `state`, then continue in BOS. The browser does not return to
   BOS automatically; the account is connected only after BOS exchanges the code
   and saves successfully. Keep the same authorization session while pasting its result.
5. Choose **Save Channel** at the bottom of the form. Models may be left empty;
   BOS saves the authorized account first, then tries to discover its models.
   **Save and discover models** performs both steps from the model section.
   Discovery failures do not discard the saved account. In the model drawer,
   retry discovery or choose **Edit Provider** to add model names manually.
   A channel without configured models cannot serve model requests. Only add
   models available to the account; presets do not establish subscription access.
6. Assign shared channels to an organization, then call BOS with a gateway key
   authorized for those models.

Authorization sessions last ten minutes and are tied to the administrator,
organization, role, and platform that started them. A session can be exchanged
once. For an expired/revoked account, edit its channel and choose **Restart official
account sign-in** to replace credentials while retaining the channel ID and bindings.
If token exchange fails, start a new authorization session.

## Verify and hand over access

Authorization, model configuration, and inference are separate checks. After saving
the account, configure its actual model IDs, enable the channel for scheduling,
and grant the intended organization and gateway key access. Query `/v1/models`
with that key, then make a small request using the intended protocol and inspect
usage. A saved account or successful model discovery alone does not prove that an
inference request will succeed. For full ChatGPT/Codex capabilities, use Responses;
the Chat bridge supports a bounded set of content and function tools, while the
Messages bridge supports plain non-streaming conversation.

Set **Settings > API Address** before distributing a key. The creation result and
verified key-reveal dialogs show the full key, API Base URL, and usage lookup URL;
**Copy all information** copies those values with a confidentiality reminder. The
API Base URL includes `/v1`; Claude Code uses the gateway root instead. The usage
lookup URL points to `/login` on the current console site, which may have a
different domain from the API. Recipients select **API Key** sign-in to inspect
only that key's usage. Send the allowed model IDs and protocol separately, and
deliver the copied credential only through a secure channel.

The console's `/docs/providers-models`, `/docs/api-keys#handoff`, and
`/docs/troubleshooting` guides explain the same workflows in Simplified Chinese,
English, Japanese, German, and Spanish. They are accessible without signing in.

## Supported upstreams

### Codex client version

Model discovery (`client_version`) and OAuth HTTP, WebSocket and live requests
share a single version for their `Version` and `User-Agent` headers. The bundled
fallback is `0.166.0`. At startup and every six hours, BOS checks the latest stable
`rust-vX.Y.Z` release from `openai/codex` on GitHub and adopts it when newer.
Discovery runs in the background and does not delay serving requests. Failures,
rate limits and malformed/prerelease responses retain the current version and
produce a `codex.version.sync_failed` log. A restart uses the bundled fallback
until a successful check; synchronized values are not persisted.

Set `BOS_CODEX_CLIENT_VERSION=0.166.0` in `deploy/stack.env` to pin a tested stable
version and override automatic updates. Alternatively, set
`BOS_CODEX_VERSION_AUTO_UPDATE=false` to keep the bundled version. Recreate the
application container after changing these settings. Version updates change the
protocol version declaration, not the installed Codex software or the gateway's
implemented capabilities; new upstream protocol features can still require a
gateway update. They do not establish model access or guarantee inference success.

| Account | Upstream | Client protocol |
| --- | --- | --- |
| ChatGPT/Codex | ChatGPT Codex backend | Responses JSON/SSE, Chat JSON/SSE with a bounded function-tool subset, and the existing Codex WebSocket/compaction routes |
| Claude | Official Anthropic API with OAuth Bearer authentication | Messages JSON/SSE, count_tokens; existing Chat conversion |
| Gemini CLI / Code Assist | Google Code Assist with its managed/project ID | Gemini generateContent JSON, streamGenerateContent SSE; existing Chat/Messages conversion |
| Antigravity | Google Antigravity with its own OAuth client and managed/project ID | Gemini generateContent JSON, streamGenerateContent SSE; existing Chat/Messages conversion |

OpenAI OAuth Chat accepts text, user images, function tools and matched textual
tool history in JSON or streaming mode. It rejects generation controls including
token limits, sampling and stop sequences until their Codex support is verified;
other unsupported fields return an explicit conversion error. Output types that
cannot be represented in Chat, including opaque reasoning items, remain rejected.
Chat `reasoning_effort` maps unchanged to Responses `reasoning.effort`; actual
effort availability depends on the selected upstream model.
The separate Messages compatibility path remains limited to plain non-streaming
conversation; use native Responses for full Codex capabilities.
Subscription OAuth does not grant OpenAI API embeddings or image API access.
Gemini token counting uses Google's generative-language endpoint with the OAuth
token and remains subject to that endpoint's access policy. Code Assist embedding
operations are not supported. Native Gemini replies retain thought signatures and
tool arguments. Existing cross-protocol conversion limits still apply.
Antigravity uses an agent request envelope and adds an Antigravity identity
preamble while retaining caller system instructions and opaque tool fields.
Antigravity token counting is unavailable and returns a capability error.

Gemini and Antigravity resolve their project using `loadCodeAssist`; new eligible
users can be onboarded automatically. Named asynchronous operations are polled rather than
started again, and an operation's `error` is handled even if `done` is true.
When onboarding finishes without a project, BOS reloads the account state. If
Google still does not return a managed project, or an existing subscriber has no
managed project, BOS lists existing ACTIVE projects through Cloud Resource
Manager using the same account token. It prefers a unique Code Assist/companion
project, then a unique default project, then the sole remaining project. With no
accessible project or multiple equally suitable projects, enter the account's
Google Cloud Project ID in the authorization form and authorize again. BOS does
not create Cloud projects, enable APIs, or change billing during this discovery.
Google permissions or operation errors are reported rather than treated as a
successful onboarding with a missing project. A consumer Gemini subscription
alone does not guarantee Code Assist access or access to every listed model.

OpenAI and Claude model discovery uses their authenticated official catalogs.
Antigravity discovery uses its authenticated `fetchAvailableModels` catalog.
Code Assist has no equivalent catalog here: Gemini discovery validates account
connectivity and retains the manually configured models. Gemini discovery returns
those model names and the implemented operations without inventing token limits.

## Authorization and Google project troubleshooting

| Symptom or diagnostic | Next step |
| --- | --- |
| Official sign-in tab does not open | Use **Open official sign-in page** or copy the authorization link. Check browser popup settings and keep the original form open. |
| `ERR_CONNECTION_REFUSED` on the OpenAI or Antigravity localhost callback | Copy the complete callback URL, including `code` and `state`, into the original BOS form and save. The deployment host does not need a published callback port. |
| Expired link, failed exchange, or mismatched session | Start a new sign-in with the same administrator and platform; paste only its new result into the corresponding form. Sessions expire after ten minutes, are single-use, and are cleared by backend restarts. |
| Channel saved without models | Retry discovery or edit the saved channel to add exact model IDs available to the account. Gemini discovery keeps manual model IDs. Verify model status, scheduling, organization bindings, and key restrictions. |
| No managed project or ambiguous Cloud projects | Enter a Google Cloud Project ID accessible to the signed-in account in the Gemini or Antigravity form, then restart sign-in. Discovery does not create projects, enable APIs, or change billing. |
| `SERVICE_DISABLED` | Ask the project administrator to enable the Gemini for Google Cloud API for the selected project, then restart sign-in. |
| `ACCESS_TOKEN_SCOPE_INSUFFICIENT` | Restart sign-in and grant the required Google permissions. Confirm account access to the intended project. |
| `BILLING_DISABLED` | Ask the project administrator to check the selected project's billing; restart sign-in after it is resolved. |
| `CONSUMER_INVALID` | Check the Google Cloud Project ID and signed-in account, then restart sign-in with the corrected project. |
| Project initialization timeout or permission/network error | Check the reported action, sanitized HTTP/status information, server connectivity to Google, account eligibility, and project access. Resolve the cause before starting a new authorization session. |
| Previously connected account stops working | Check the sanitized upstream error and subscription access. Tokens refresh automatically; if authorization is revoked or refresh fails, edit the existing channel and restart sign-in to replace its credentials. |

Google onboarding polls a returned asynchronous operation instead of starting a
new operation on every poll. If initialization does not complete within the
request's bounded attempts/time, authorization fails; repeated saving does not
repair project permissions. Diagnostics expose recognized status/reason codes
without Google's raw messages, metadata, or credential-bearing response bodies.
When requesting help, provide the action and sanitized error, never the callback
URL, authorization code, tokens, or full gateway key.

## Credentials and deployment

Access tokens, refresh tokens, account/project identity, and expiry are serialized
inside the existing encrypted credential column. They are excluded from provider
API responses. OAuth channels pin their endpoint and protocol to the official
service; API-key channels retain their configurable endpoints.

When a token has less than one minute remaining, BOS reloads the account under a
database row lock, refreshes once, and saves rotated tokens before releasing the
lock. Concurrent requests and replicas sharing the database reuse the updated
credential. Failed refreshes preserve the stored credential and fail the request.
There is no schema migration or additional background service for this feature.

Gemini sign-in and token refresh require operator-supplied
`BOS_GEMINI_OAUTH_CLIENT_ID` and `BOS_GEMINI_OAUTH_CLIENT_SECRET` in the server
environment (Compose: the deployment host's private, untracked `deploy/stack.env`).
The registered redirect URI must include `https://codeassist.google.com/authcode`.
Missing or incomplete configuration fails authorization and refresh before a
network request. The community source does not embed OAuth client credentials.
Endpoint validation and routing with an unexpired token do not depend on this
client configuration. Source changes require rebuilding and deploying the backend.
Antigravity requires operator-supplied `BOS_ANTIGRAVITY_OAUTH_CLIENT_ID` and
`BOS_ANTIGRAVITY_OAUTH_CLIENT_SECRET` for a client with
`http://localhost:8085/callback` registered. Missing or incomplete configuration is
rejected. Gemini and Antigravity use separate client identities and environment
variables.
For Docker Compose, edit the existing private `deploy/stack.env` on the deployment
host and run `./deploy/stack.sh start` from that checkout's root. This applies the
updated environment using the existing images and preserves service credentials.
`docker restart` or `docker compose restart` alone does not inject changed
environment values. `backend/.env.example` is not read by the deployed stack.
For a directly launched backend, set these variables in its process environment
and restart that process.

OAuth sessions are in memory:
restarting the process clears pending authorizations, so use one app instance or
sticky routing while connecting accounts. Saved credentials persist in MySQL.

Protocol references:

- https://github.com/Wei-Shaw/sub2api/tree/ba81c7fd758e425d67f856d601147ef0c5263d3f/backend/internal/pkg
- https://github.com/google-gemini/gemini-cli
- https://github.com/openai/codex
- https://developers.openai.com/codex/auth

Automated tests use fake token/model services and verify account isolation,
PKCE/state/session replay, credential rotation, database locking, and JSON/SSE
envelopes. Live sign-in and inference require an operator-owned eligible account;
automated regression tests do not establish current upstream availability.
