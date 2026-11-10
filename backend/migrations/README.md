# SQL migrations

## Community database scope

The community initial schema and checksums differ from the internal product.
Use a fresh database for the first community installation. The upgrade guidance
below applies to existing community deployments using this migration lineage;
it is not a procedure for migrating a commercial database.

## Upgrade an existing community deployment

`022_announcements.sql` adds global Markdown announcements for authenticated
administrator and API Key portal readers. Super administrators manage global
publications. The migration runs automatically before HTTP startup;
no sample announcements are seeded. The manual rollback script
`022_announcements.down.sql` removes all announcement content, so back it up and
coordinate application rollback before running that script.

`023_announcement_organizations.sql` adds organization ownership. Existing
announcements remain global. Organization administrators can create, edit and
delete only announcements for their own organization. Readers see global
announcements plus their current organization's announcements; super
administrators see global announcements only. Organization ownership is derived
from the authenticated session, including the active Key's current organization.
The manual `023_announcement_organizations.down.sql` deletes organization-only
announcements before removing ownership so an older application cannot expose
them globally. Back up these publications and stop the application before a
coordinated rollback; never run an older backend against the scoped data.

No separate migration shell script or manual SQL import is required. Migration
SQL is embedded when the backend is built; the new backend applies pending files
in filename order before opening its HTTP listeners. Uploading SQL files or
restarting an old image alone does not include a newly added migration.

Back up the database and retain the original private `deploy/stack.env`,
encryption key, and persistent data. On the intended deployment host, update the
checkout to the desired release and rebuild/start the application:

```sh
./deploy/stack.sh up
./deploy/stack.sh status
curl --fail --silent --show-error --connect-timeout 5 --max-time 20 \
  http://127.0.0.1:18080/health/ready
```

Substitute the configured application port. If using the
[community deployment guide](../../deploy/README.md),
run `./deploy/stack.sh up --source` on the intended host; the newly built
image uses the startup migration runner. Nginx delivery
also requires the prepared static publication root described in the
[static delivery guide](../../deploy/STATIC_WEB.md).

For the organization/Key upgrade, the runner applies `009_organization_keys.sql`
and then `010_legacy_organization_bindings.sql` if they are pending. Existing
Keys with empty policy lists continue to inherit organization/channel access;
explicit Key restrictions remain unchanged. Do not assign every shared channel
or copy model lists onto all old Keys. Organizations
without explicit shared channel assignments need a super administrator to
assign channels in the console. Sign in again after `009` invalidates old
administrator organization claims.

Inspect application logs for migration failures. A read-only query on the
application database can confirm completion:

```sql
SELECT filename, applied_at
FROM schema_migrations
WHERE filename IN ('009_organization_keys.sql', '010_legacy_organization_bindings.sql')
ORDER BY filename;
```

Both rows must have a non-NULL `applied_at` after a successful upgrade to this
release. Verify an existing Key's model access and historical organization/Key
usage as well as readiness. If startup fails, inspect the named migration and
follow the recovery guidance below; application image rollback does not undo
database changes.

## Security event severity and unread notifications

`013_security_event_severity.sql` reclassifies historical events using the same
rule-based severity as new events. Invalid tool calls and the specified access,
authentication, provider, and model-output diagnostics are informational; other
tool-call events and prompt credential detections are warnings. Event types,
actions, evidence, and ownership remain unchanged.

`014_security_event_reads.sql` stores each administrator's read acknowledgements separately
for an organization or the super administrator's global view. Existing warning,
high, and critical events start unread until that administrator opens Security
Events. Informational events do not contribute to notifications. The console
defaults to warning or higher and still allows viewing all severities.

Both migrations run automatically on backend startup. Their `.down.sql` files
are manual rollback scripts and are excluded from startup migration discovery.
Rolling back `014` removes the saved read acknowledgements; rolling back `013` restores
the previous action-based severity calculation. Roll back application code and
database changes together using the recovery procedure below.

## First-token request timing

`015_usage_first_token.sql` adds nullable `usage_logs.first_token_ms`, measured
in milliseconds by the gateway. Usage record APIs return the same nullable
`first_token_ms` field. Historical requests and requests with no observed model
output retain NULL; a measured zero remains zero. This stores only a server-side
duration and adds no request or response content or changes to query ownership.

The migration runs automatically before the new backend accepts requests.
`015_usage_first_token.down.sql` removes the timing column; it does not change
request counters or billing. Stop the backend and coordinate application rollback
before running that manual script with the migration account. To redeploy a
version that includes `015` after a manual rollback, remove only its completed
`schema_migrations` row so the forward migration can run again.

## Usage preaggregation

`017_usage_daily_rollups.sql` adds day/organization/Key/model counters and an
indexed pending flag on the immutable usage ledger. The new backend starts a
bounded background backfill after startup; requests combine completed buckets
with pending logs so the page remains accurate throughout backfill. The
five-minute cache and page force-refresh control continue to work. See the
[preaggregation guide](../docs/usage-preaggregation.md) for behavior, progress
queries, tests, and coordinated rollback instructions.

## Setup status usage indexes

`025_setup_usage_indexes.sql` adds the covering
`(api_key_id, rollup_applied, created_at, total_tokens, cost_amount)` index to
both usage tables. Setup status combines daily rollups with only the selected
Key's pending tail, compares all budgets in SQL, and skips usage aggregation for
unlimited Keys. Successful-call evidence reads each physical table by Key and
time, applies all configuration/model predicates before its top-one limit, and
merges at most two timestamps per Key. One repeatable-read snapshot prevents
double counts or omissions while rollups and legacy moves commit.

Both index builds use `ALGORITHM=INPLACE, LOCK=NONE`. They still read existing
data and require disk space and metadata locks; allow for this work during
deployment. The forward migration runs before the new backend serves requests.
The `.down.sql` file removes only these indexes and requires coordinated rollback
to the previous application, which does not reference them.

Set `BOS_SETUP_TEST_DSN` to a disposable MySQL 8.4 server and run
`go test ./internal/admin/interfaces -run 'TestSetup.*MySQL' -v` from `backend`.
The tests create and drop randomly named schemas, verify scope and calendar/
DECIMAL/BIGINT boundaries before and after rollups/moves, and reject unbounded
physical-table access in query plans. Evidence branches must also use backward
time-index scans; an indexed scan followed by a history-wide sort is rejected.
`BOS_SETUP_PERF=1` expands the busy-Key
fixture from 10,000 to 1,000,000 acknowledged usage rows and reports timing
without asserting an environment-dependent latency threshold.

## Prompt-cache usage fields

`024_cache_usage.sql` adds request-level `cache_read_tokens`,
`cache_write_tokens`, `cache_read_cost_amount`, and `cache_write_cost_amount`
to `usage_logs` and `usage_logs_v2`, then adds matching aggregate fields to
`usage_daily_rollups`. It rebuilds the explicit `usage_logs_read` and
`usage_logs_pending` views. Historical rows keep zero cache fields, while
`cost_amount` remains the total request cost.

The migration runs automatically on backend startup. `024_cache_usage.down.sql`
is a manual rollback script; stop new backend replicas and roll back
application code first. To redeploy after a manual rollback, remove only its
completed `schema_migrations` row.

## Legacy organization bindings

`010_legacy_organization_bindings.sql` converts explicit legacy organization
channel allowlists into active channel bindings, only for organizations with no
binding rows at all. Existing bindings (including paused bindings, priorities,
and channel-specific model restrictions) remain authoritative and unchanged.
Only existing shared channels explicitly named in the organization's legacy
allowlist are eligible. Channel/organization status and schedulability are not changed.
Legacy explicit model lists are retained even if no model currently matches;
NULL, JSON null, empty lists, and the legacy `*` wildcard become an unrestricted
binding model list. Malformed policy shapes are skipped rather than widened.
Organizations without explicit channel IDs, including model-only policies, gain
no channels automatically; assign their channels explicitly in the
organization settings. No default/global channel is assigned.

The migration leaves API Key policies, organization ownership, historical usage,
request logs, security events, and billing attribution untouched. Existing Keys
with empty/NULL allowlists inherit the resulting organization/channel policy;
explicit Key restrictions still narrow access. This is one atomic, repeatable
INSERT and runs automatically on the next backend startup through the normal
migration runner. Applied migrations are not rewritten.

To execute the binding migration regression test against an isolated MySQL 8.4
server, set `BOS_MIGRATIONS_TEST_DSN` and run
`go test ./migrations -run TestLegacyOrganizationBindingsMySQL -v` from `backend`.
The test requires CREATE/DROP DATABASE privileges, creates a randomly named
disposable schema, and removes only that schema afterward. It never uses the
database named in the DSN. Without this variable the MySQL test is skipped.

`009_organization_keys.sql` removes organization membership from super
administrators and the user assignment from API Keys. Each organization
administrator still belongs to exactly one organization, and every Key requires
one organization. Super administrators can change a Key's organization after
creation; future channel access follows the new organization while historical
usage remains attributed to the organization recorded at request time. Existing organizations, including any formerly used
as the default/platform organization, remain ordinary organizations; no usage,
request log, security event, or organization is deleted or reassigned.
The migration also adds optional Key channel/model allowlists. Empty lists
inherit available channel policy; explicit lists narrow it. Shared channels
require an active organization assignment. Super administrators manage channels
and their organization assignments.
Existing administrator access tokens with the old organization claim become
invalid after migration; sign in again. New super administrators are stored
without an organization. The original bootstrap is retained unchanged for
checksum compatibility; this migration clears its super administrator's
organization membership.

`006_security_event_evidence.sql` adds rule-name snapshots and actual processing
results to events, plus a separate `security_event_evidence` table. Evidence is
generated and normalized before queueing: credentials use fixed hidden labels,
code uses allowlisted markers without surrounding text, and opaque match values
are withheld. Dynamic JSON field names use ordinal paths. Each event collects at
most 8 items, with snippets bounded by fixed templates (under 160 characters).
Evidence expires after 7 days; the gateway sink deletes expired rows hourly in
bounded batches, while the detail API stops returning them as soon as they expire.
Event metadata is retained. Existing events are not backfilled.

Only the administrator `/security-events/:id` endpoint returns evidence, scoped
to the organization for organization administrators. Event lists and API Key
portal responses retain their metadata-only contract. Security-event Webhook
delivery is excluded from the community edition.

`001_initial_schema.sql` creates the application's initial MySQL schema and
seeds the model catalogue and initial super administrator. It is applied
automatically before the service opens its HTTP listeners when
`BOS_DATABASE_DSN` is configured. It creates the community application's base
tables; later migrations add evidence, rollups and other community storage. The
migration runner creates and manages `schema_migrations` itself. Fixed security
checks are implemented in code; no configurable security-rule tables are seeded.

The initial schema uses `CREATE TABLE IF NOT EXISTS`, so startup can record the
baseline when the tables already exist. Existing table definitions stay intact;
built-in model metadata is refreshed. A clean database gets the base tables and indexes from the initial SQL
file. The appended bootstrap creates a `Platform Administration` organization and an active `super_admin` user in one transaction if no super administrator exists. Existing super administrators (including disabled ones) and their passwords are never changed.

Migration SQL is embedded in the application at build time. Only top-level
`.sql` files are executed; `.down.sql` files are ignored. Forward scripts under
`retired/` are retained unchanged and checksum-validated if previously applied,
but are not executed on databases where they have not run. Duplicate active and
retired filenames are rejected. Each applied file is
recorded with its filename and SHA-256 checksum, and startup skips completed
files. The runner serializes concurrent startups using a MySQL named lock. Do
not edit an applied migration; add a new numbered forward SQL file for later
schema changes.

The in-place request-log conversion `019_request_log_partitions.sql` is retired.
`021_request_log_transition.sql` creates an empty partitioned destination and a
durable cursor for asynchronous, throttled copying of the most recent 24 hours.
See [request log transition](../docs/request-log-retention.md) for compatibility,
progress checks and rollback requirements. An unfinished retired migration still
requires explicit repair; retiring it never marks an interrupted DDL complete.

`023_usage_log_transition.sql` creates an empty date-partitioned usage destination
and durable retry receipts. Compatibility writes remain in the source until all
instances have upgraded and partition writes/draining are explicitly activated.
Raw data then retains 30 complete UTC+8 business days plus today; acknowledged
daily summaries and quota counters survive expiry. See
[usage log retention](../docs/usage-log-retention.md) for activation, bounded
history movement, diagnostics and application rollback constraints.

The sole compatibility exception is the pre-bootstrap `001_initial_schema.sql`
checksum `dc40b66606e81206178199382a54b22aee97d938a68070a2d77d91297842948c`.
When its original SQL bytes are unchanged, the runner executes only the appended
administrator bootstrap and updates the checksum after success. Any other edit
to an applied file still blocks startup. The bootstrap follows the same
unfinished-migration recovery rules below.

When applying the initial schema or this one-time upgrade, supply
`BOS_ADMIN_EMAIL` and `BOS_ADMIN_PASSWORD` (12–128 bytes). `deploy/stack.sh init`
or `up` generates them in the private `deploy/stack.env`. The runner normalizes
the email, hashes the password using the login service's Argon2id function, and
binds `@bos_initial_admin_email` / `@bos_initial_admin_password_hash` on the same
MySQL connection that executes the SQL. No plaintext password is sent to MySQL.
These values are not needed once this migration is complete. For manual SQL
execution, bind these session variables first using a compatible Argon2id hash;
without them the SQL creates the schema but skips administrator insertion.

Each file may contain multiple MySQL statements, comments, and quoted
semicolons. Write server SQL directly; MySQL client commands such as
`DELIMITER`, `SOURCE`, and `USE` are not supported. Do not modify
`schema_migrations` from migration SQL.

MySQL DDL may commit before a whole file finishes. A failed or interrupted file
remains marked unfinished (`applied_at IS NULL`) and blocks startup. Repair
partial changes, then either remove the unfinished history row so the file can
run again or mark it complete after verifying every statement. Startup reports
the affected filename and logs successful migrations.

`011_usage_reporting.sql` adds optional local usage-reporting state, outbox and operation records. It creates only an empty singleton state row; it does not create an installation ID or enable reporting. See [the client protocol and lifecycle](../docs/usage-reporting.md).

`012_installation_telemetry_privacy.sql` discards all earlier queue bodies without reading them and invalidates old leases. The client now uses schema version 2 with installation/platform metadata and current-day running heartbeats only; it never reads usage_logs. Existing business/local logs and their retention are untouched.

### Key IP summary covering indexes

`024_usage_ip_covering_indexes.sql` adds
`(api_key_id, client_ip, organization_id, created_at)` indexes to both
`usage_logs` and `usage_logs_v2`. Administrative IP snapshots aggregate within
each physical table and reuse the grouped results across pages. The indexes
avoid reading unrelated request columns while counting historical IP activity.
The current IP endpoints restrict aggregation to the last 24 hours and use the
existing `(api_key_id, created_at)` indexes instead, so their scans can seek
directly into that time range. This restriction needs no new migration.
The migration uses `ALGORITHM=INPLACE, LOCK=NONE`; index construction still uses
I/O and requires metadata locks. The matching `.down.sql` removes only these
indexes; query correctness does not depend on them.

### Organization calendar quota indexes

`026_organization_quota_indexes.sql` adds the covering index
`idx_usage_org_pending_time (organization_id, rollup_applied, created_at,
total_tokens, cost_amount)` to both `usage_logs` and `usage_logs_v2`.
Organization quota reads combine daily rollups with pending rows from both raw
sources in one statement. They use `idx_rollup_org_date` for stored totals and
the new indexes for each organization's pending tail. Filtering by the recorded
organization includes all its keys, including deleted keys and history preceding
a key's move to another organization. The pending flag before the timestamp
avoids scanning acknowledged history when probing a period.

Apply migration 026 before enabling organization quota reads: the query explicitly
names these indexes with `FORCE INDEX`. The migration is embedded and applied
at startup like other forward scripts. Index construction uses
`ALGORITHM=INPLACE, LOCK=NONE`, but still consumes I/O and acquires metadata locks.
If one statement fails, repair the partial DDL and unfinished migration history
before restarting; do not blindly rerun an index addition that already succeeded.

The matching `.down.sql` removes only these two indexes. Disable or roll back
organization quota readers before removing them. Settings and usage data are
preserved. Configuration continues to use `rate_limit.organization.{id}` in
`settings`; replacing its JSON clears quotas sent as `null`, while numeric zero
remains a blocking quota. No settings backfill is needed for existing QPS-only
JSON.

Unit coverage in `organization_quota_indexes_test.go` checks forward and rollback
SQL and index column order. The user infrastructure periodic-usage unit tests
check generated MySQL SQL, bound UTC+8 boundaries, organization scope, query
isolation, cancellation, and error propagation with SQL mocks.
