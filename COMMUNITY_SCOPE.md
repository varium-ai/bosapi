# Community edition scope

This document describes the code included in this community source tree.
A missing implementation or an undecided feature-list entry is not permission
to add an excluded implementation. Dependent features follow the boundary of
their parent capability.

## Included capabilities

| Area                         | Community scope                                                                                                  | Important limits                                                                                                |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Authentication               | Administrator login, MFA, password recovery, login lockout, and role checks                                      | Upstream service credentials are supplied by the operator                                                       |
| Organizations                | Organization management and assignments of shared channels and models                                            | At most two organizations per installation                                                                      |
| API Keys                     | Creation, batch creation, import, copying, rotation, access policies, quotas, and rate limits                    | At most ten Keys per installation                                                                               |
| Channels                     | Shared upstream channels, supported subscription flows, model discovery, and model assignments                   | Super administrators maintain channels; organization administrators view their assigned channels                |
| Gateway                      | Supported OpenAI, Anthropic, and Gemini interfaces and protocol conversion                                       | Capabilities depend on the configured upstream                                                                  |
| Optional Live attestation    | A macOS-specific local adapter                                                                                   | Requires a separately installed desktop runtime and native module; it is not a portable Linux/Docker capability |
| Usage                        | Usage records, recorded costs, statistics, snapshots, and request diagnostics                                    | Viewing usage does not include scheduled report generation or delivery                                          |
| Basic inspection             | Fixed credential checks, current-input redaction, output code-marker observations, and tool authorization checks | The advanced-detector contract is wired to a no-op implementation                                               |
| Security events              | Local events, redacted evidence, event details, and unread indicators                                            | No configurable security-rule management or event Webhook delivery                                              |
| Authentication notifications | OTP delivery through configured SMTP, Lark, or generic HTTPS notification transports                             | This does not include usage report jobs or security-event delivery                                              |
| Installation telemetry       | Installation-only reporting and administrator controls                                                           | Enabled by default when no saved choice exists; supports persistent opt-out and deployment-level force-disable  |
| Deployment                   | Source-built Docker Compose, frontend hosting, optional HTTPS and monitoring, and manual source/image upgrades   | Online system updates, host updater agents, internal release tools and blue/green controllers are excluded      |

## Capacity semantics

The initial organization, disabled organizations, and disabled Keys count toward
capacity. Removing an organization disables it and keeps its slot. Deleting a Key
releases its slot. Rotating an existing Key does not allocate another slot.
Imports and batch creation reject the entire batch if it would exceed capacity.
Creation requests that exceed either limit return HTTP 409. Existing resources
are not removed when capacity is reached.

## Excluded capabilities and dependencies

| Capability                                | Dependent features that are also excluded                                                                                                                                                                                             |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Organization-private channels             | Direct routing to organization-private channels, private-channel creation, and private-channel onboarding                                                                                                                             |
| Configurable automatic routing            | Named exact, prefix, and regular-expression routing rules, aggregate-model policies, long/short request targets, and routing-session configuration                                                                                    |
| Advanced content detection                | Injection signatures, system-prompt extraction patterns, credential-exfiltration instructions, sensitive-file instruction detection, encoded-fragment matching, obfuscation normalization, and their detection-specific optimizations |
| Custom security rules                     | Global/organization rule management, pattern compilation and caching, custom sensitivity and action settings                                                                                                                          |
| Security-event Webhooks                   | Configuration, signing secrets, delivery queues, retries, leases, and delivery status                                                                                                                                                 |
| Usage report jobs                         | Scheduling, aggregation for report delivery, job lookup, workers, notification delivery, and the weekly-report button/API                                                                                                             |
| Internal publishing and remote deployment | Fixed-environment uploaders, remote synchronization/build/status scripts, internal image publishing, and internal test-image installers                                                                                               |
| Online system updates                     | Console update pages, image checks and apply APIs, host updater agents, updater installation and Compose overlays                                                                                                                     |
| Blue/green deployment controllers         | Candidate slots, traffic switching, draining, recovery, rollback coordination, and host-agent integration                                                                                                                             |

Format decoding needed to parse supported API protocols and normalize redacted
evidence remains included. It is distinct from the excluded advanced matching
and obfuscation/encoded-fragment detection.

## Verification and publication

Run `python3 tools/check_community_source.py` to check source boundaries. After a
frontend production build, also run
`python3 tools/check_community_source.py --frontend-dist` to check bundled routes
and excluded feature text. These checks do not replace live database, deployment,
or upstream-service acceptance tests.

The source license and publication decisions remain tracked in
[LICENSING.md](LICENSING.md). Scope approval does not make this local review tree
ready for publication.
