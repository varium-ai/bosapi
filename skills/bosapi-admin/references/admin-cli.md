# BOS API Admin Reference

## Environment

```bash
export BOSAPI_BASE_URL='https://your-bosapi-host'
export BOSAPI_ADMIN_API_KEY='<admin api key>'
# Alternatively, use an administrator JWT when no administrator API key is configured:
# export BOSAPI_JWT='<admin access_token>'
```

Authentication prefers `BOSAPI_ADMIN_API_KEY` in `x-api-key`. Otherwise, it uses `BOSAPI_JWT` in `Authorization: Bearer <jwt>`. Regenerate the administrator API key if the server returns `INVALID_ADMIN_KEY`. For JWT authentication, sign in with the administrator email/password and copy `data.access_token` from the response:

```bash
curl -sS "$BOSAPI_BASE_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"your-password"}'
```

## CLI

Run the following commands from this skill's directory.

```bash
node scripts/bosapi-admin.js <command>
```

## Accounts

### Read-only operations

```bash
node scripts/bosapi-admin.js accounts list --page-size 20
node scripts/bosapi-admin.js accounts list --search outlook --platform openai --type oauth --status active
node scripts/bosapi-admin.js accounts get 40
node scripts/bosapi-admin.js accounts usage 40
node scripts/bosapi-admin.js accounts stats 40 --days 30
node scripts/bosapi-admin.js accounts today-stats 40
node scripts/bosapi-admin.js accounts batch-today-stats --ids 40,39
node scripts/bosapi-admin.js accounts models 40
node scripts/bosapi-admin.js accounts temp-unschedulable 40
node scripts/bosapi-admin.js accounts antigravity-default-model-mapping
```

`accounts export` includes account credentials and tokens. Write the output to a protected file rather than printing it to the terminal:

```bash
node scripts/bosapi-admin.js accounts export --ids 40,39 --file accounts-export.json
node scripts/bosapi-admin.js accounts export --platform openai --type oauth --include-proxies false --file accounts-export.json
```

### Single-account mutations

```bash
node scripts/bosapi-admin.js accounts create --file account.json
node scripts/bosapi-admin.js accounts update 40 --json '{"concurrency":20}'
node scripts/bosapi-admin.js accounts set-status 40 active
node scripts/bosapi-admin.js accounts set-schedulable 40 true
node scripts/bosapi-admin.js accounts clear-error 40
node scripts/bosapi-admin.js accounts clear-rate-limit 40
node scripts/bosapi-admin.js accounts recover-state 40
node scripts/bosapi-admin.js accounts reset-quota 40
node scripts/bosapi-admin.js accounts refresh 40
node scripts/bosapi-admin.js accounts test 40
node scripts/bosapi-admin.js accounts sync-models 40
node scripts/bosapi-admin.js accounts apply-oauth 40 --file credentials.json
node scripts/bosapi-admin.js accounts reset-temp-unschedulable 40
```

### Deletion and cleanup

List the target account names and IDs before deleting them.

```bash
node scripts/bosapi-admin.js accounts delete 25
node scripts/bosapi-admin.js accounts keep-only --name 'target@example.com'
```

### Bulk mutations

```bash
node scripts/bosapi-admin.js accounts batch-create --file accounts.json
node scripts/bosapi-admin.js accounts batch-update-credentials --file payload.json
node scripts/bosapi-admin.js accounts bulk-update --ids 40,39 --json '{"concurrency":10,"priority":2}'
node scripts/bosapi-admin.js accounts batch-refresh --ids 40,39
node scripts/bosapi-admin.js accounts batch-clear-error --ids 40,39
```

`bulk-update` supports the fields exposed by the bulk-update form, such as `base_url`, `model_mapping`, `group_ids`, `proxy_id`, `concurrency`, `priority`, `rate_multiplier`, `status`, and `compact_mode`. Confirm field names with `accounts get <id>` before updating.

### Import

General management import:

```bash
node scripts/bosapi-admin.js accounts import-data --file accounts-export.json
node scripts/bosapi-admin.js accounts import-codex-session --file payload.json
```

CRS synchronization:

```bash
node scripts/bosapi-admin.js accounts crs-preview --file payload.json
node scripts/bosapi-admin.js accounts crs-sync --file payload.json
```

Legacy JSON import is still available and copies template account settings to imported accounts:

```bash
node scripts/bosapi-admin.js accounts import-json \
  --file /path/accounts.json \
  --template-name 'template@example.com' \
  --dry-run
```

Copied fields:

- `concurrency`
- `priority`
- `group_ids`
- `credentials.model_mapping`

## Groups And Proxies

```bash
node scripts/bosapi-admin.js groups all
node scripts/bosapi-admin.js proxies all
```

## Redeem Codes

Redeem code types include `balance`, `concurrency`, `subscription`, and `invitation`. Common states are `unused`, `used`, and `expired`.

### Read-only operations

```bash
node scripts/bosapi-admin.js redeem-codes list --page-size 20
node scripts/bosapi-admin.js redeem-codes list --type balance --status unused --search user@example.com
node scripts/bosapi-admin.js redeem-codes get 123
node scripts/bosapi-admin.js redeem-codes stats
node scripts/bosapi-admin.js redeem-codes export --file redeem-codes.csv
```

### Generate redeem codes

```bash
node scripts/bosapi-admin.js redeem-codes generate \
  --json '{"count":1,"type":"balance","value":10}' \
  --idempotency-key "redeem-generate-$(date +%s)"
```

Subscription codes require `group_id` and nonzero `validity_days`:

```bash
node scripts/bosapi-admin.js redeem-codes generate \
  --json '{"count":1,"type":"subscription","value":0,"group_id":2,"validity_days":30}' \
  --idempotency-key "redeem-subscription-$(date +%s)"
```

### Create and redeem

For payment callbacks or manual crediting, create a code and redeem it for a user in one operation. Production workflows must provide a stable `--idempotency-key`.

```bash
node scripts/bosapi-admin.js redeem-codes create-and-redeem \
  --json '{"code":"order_123","type":"balance","value":10,"user_id":123,"notes":"manual recharge"}' \
  --idempotency-key order-123
```

### Updates and cleanup

Verify target IDs with `list` or `get` before writes.

```bash
node scripts/bosapi-admin.js redeem-codes batch-update --ids 123,124 --json '{"notes":"campaign A"}'
node scripts/bosapi-admin.js redeem-codes expire 123
node scripts/bosapi-admin.js redeem-codes delete 123
node scripts/bosapi-admin.js redeem-codes batch-delete --ids 123,124
```

## Error Rules And TLS Profiles

These commands correspond to Error Passthrough Rules and TLS Fingerprint Profiles on the accounts page.

```bash
node scripts/bosapi-admin.js error-rules list
node scripts/bosapi-admin.js error-rules get 1
node scripts/bosapi-admin.js error-rules create --file rule.json
node scripts/bosapi-admin.js error-rules update 1 --json '{"enabled":true}'
node scripts/bosapi-admin.js error-rules toggle 1 false
node scripts/bosapi-admin.js error-rules delete 1

node scripts/bosapi-admin.js tls-profiles list
node scripts/bosapi-admin.js tls-profiles get 1
node scripts/bosapi-admin.js tls-profiles create --file profile.json
node scripts/bosapi-admin.js tls-profiles update 1 --file profile.json
node scripts/bosapi-admin.js tls-profiles delete 1
```

## Raw Admin API

Use `api` for management endpoints without dedicated wrappers or added in newer versions. Paths may use `/admin/...` or `/api/v1/admin/...`.

```bash
node scripts/bosapi-admin.js api GET /admin/groups/all
node scripts/bosapi-admin.js api POST /admin/accounts/bulk-update \
  --json '{"account_ids":[40],"concurrency":10}'
```

## Confirmed Admin Endpoints

- `GET /api/v1/admin/accounts`
- `GET /api/v1/admin/accounts/:id`
- `POST /api/v1/admin/accounts`
- `PUT /api/v1/admin/accounts/:id`
- `DELETE /api/v1/admin/accounts/:id`
- `POST /api/v1/admin/accounts/check-mixed-channel`
- `GET /api/v1/admin/accounts/:id/usage`
- `GET /api/v1/admin/accounts/:id/stats`
- `GET /api/v1/admin/accounts/:id/today-stats`
- `POST /api/v1/admin/accounts/today-stats/batch`
- `POST /api/v1/admin/accounts/:id/schedulable`
- `POST /api/v1/admin/accounts/:id/test`
- `POST /api/v1/admin/accounts/:id/refresh`
- `POST /api/v1/admin/accounts/:id/apply-oauth-credentials`
- `POST /api/v1/admin/accounts/:id/clear-error`
- `POST /api/v1/admin/accounts/:id/clear-rate-limit`
- `POST /api/v1/admin/accounts/:id/recover-state`
- `POST /api/v1/admin/accounts/:id/reset-quota`
- `GET /api/v1/admin/accounts/:id/temp-unschedulable`
- `DELETE /api/v1/admin/accounts/:id/temp-unschedulable`
- `GET /api/v1/admin/accounts/:id/models`
- `POST /api/v1/admin/accounts/:id/models/sync-upstream`
- `POST /api/v1/admin/accounts/batch`
- `POST /api/v1/admin/accounts/batch-update-credentials`
- `POST /api/v1/admin/accounts/bulk-update`
- `POST /api/v1/admin/accounts/batch-refresh`
- `POST /api/v1/admin/accounts/batch-clear-error`
- `GET /api/v1/admin/accounts/data`
- `POST /api/v1/admin/accounts/data`
- `POST /api/v1/admin/accounts/import/codex-session`
- `POST /api/v1/admin/accounts/sync/crs/preview`
- `POST /api/v1/admin/accounts/sync/crs`
- `GET /api/v1/admin/accounts/antigravity/default-model-mapping`
- `GET /api/v1/admin/groups/all`
- `GET /api/v1/admin/proxies/all`
- `GET /api/v1/admin/redeem-codes`
- `GET /api/v1/admin/redeem-codes/export`
- `GET /api/v1/admin/redeem-codes/stats`
- `GET /api/v1/admin/redeem-codes/:id`
- `POST /api/v1/admin/redeem-codes/generate`
- `POST /api/v1/admin/redeem-codes/create-and-redeem`
- `POST /api/v1/admin/redeem-codes/batch-update`
- `POST /api/v1/admin/redeem-codes/batch-delete`
- `POST /api/v1/admin/redeem-codes/:id/expire`
- `DELETE /api/v1/admin/redeem-codes/:id`
- `GET /api/v1/admin/error-passthrough-rules`
- `GET /api/v1/admin/error-passthrough-rules/:id`
- `POST /api/v1/admin/error-passthrough-rules`
- `PUT /api/v1/admin/error-passthrough-rules/:id`
- `DELETE /api/v1/admin/error-passthrough-rules/:id`
- `GET /api/v1/admin/tls-fingerprint-profiles`
- `GET /api/v1/admin/tls-fingerprint-profiles/:id`
- `POST /api/v1/admin/tls-fingerprint-profiles`
- `PUT /api/v1/admin/tls-fingerprint-profiles/:id`
- `DELETE /api/v1/admin/tls-fingerprint-profiles/:id`

## Notes

- Verify the target set with read-only queries before production writes.
- Exports include sensitive credentials; prefer `--file`.
- `PUT /admin/accounts/:id` and `bulk-update` accept flexible request bodies. Confirm uncertain field names with `accounts get` or the management UI.
