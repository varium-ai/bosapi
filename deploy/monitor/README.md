# Bounded system monitoring

This feature does not add schema migrations, monitoring tables or metric writes to the business database.

This is the canonical host monitoring runbook. Start with the [main deployment walkthrough](../../README.md#first-deployment) and [Docker deployment guide](../README.md#host-monitoring-deployment-decision) for the application itself. Host monitoring is an explicit per-environment choice, not an automatic side effect of deploying the app.

## Application collection

The existing super-admin-only `GET /api/v1/admin/status` serves an immutable in-memory snapshot. A single background collector samples every 30 seconds regardless of page views. Concurrent readers, refresh buttons and polling cannot trigger additional probes. The collector closes with the gateway.

- Go `runtime/metrics`: heap, Go-managed memory (not process RSS), goroutines and GC cycles. No forced GC, heap profiles or stop-the-world `ReadMemStats` calls.
- Whole gateway process: user + system CPU time across all threads, current RSS and RSS / memory denominator. CPU uses successive `getrusage` readings (100% = one logical core, may exceed 100%; first or failed sample is unavailable). Linux RSS uses the process's own `/proc/self/statm`, not host PID access. The denominator is the smaller of host `MemTotal` and visible cgroup v1/v2 memory limits, including visible ancestors; its value and source are returned. It is not cgroup-wide memory usage, and a cgroup limit can be shared with other processes. Hidden ancestor limits cannot be discovered. macOS development uses a bounded one-second `/bin/ps` call for current RSS and `hw.memsize` for the denominator. Other operating systems return unavailable metrics, never synthetic zeros. All reads occur in the background collector, never in HTTP middleware or status reads.
- Gateway-only HTTP counters: completed requests, HTTP >=400 errors, in-flight requests, mean full-session duration and approximate P95 histogram upper bound. An overflow P95 is shown as `> 5000 ms`. No per-user/model labels or request content. Admin/static/health traffic is excluded.
- HTTP error rate uses summed errors / summed completions over a rolling 300-second window, updated every 30 seconds. A fixed ring of 301 one-second buckets records completion time independently of sampler timing (less than one second of boundary quantization). Old buckets expire even when idle; no traffic yields JSON `null`, and startup uses only observed traffic. `traffic.error_window_requests`, `error_window_errors` and `error_window_seconds` describe this window. `requests`, `errors`, RPS, mean latency and the P95 histogram all use the same rolling window; `window_seconds` is capped at 300 seconds and reflects uptime during startup. History points contain rolling metrics, not per-sample metrics. No averaging of percentages or request-sized history is used.
- MySQL: one ping and a fixed six-field `SHOW GLOBAL STATUS` query per sample; one-second shared timeout. Connection-pool statistics come from the existing local pool. No business table scans or `PROCESSLIST`.
- Redis: one ping and one allowlisted INFO read per sample on a separate, single-connection monitoring client with no retries and one-second timeouts. No cache-key enumeration, `MONITOR` or configuration writes. Redis is used for rate-limit counters, not an assumed response-cache layer.
- Queue: bounded channel occupancy and existing cumulative drop/persistence-failure counters. Counters reset on process restart.

Application history retains at most 120 points (approximately one hour) in memory. It resets on restart and is instance-local, not aggregated across replicas. A load balancer may return different replicas. Long-term history and cluster aggregation require a separate monitoring store; neither is enabled here.

## Host collector deployment

### Data flow and scope

```text
Linux host: bosapi-monitor.timer -> bosapi-monitor.service -> collect.py
  -> /var/lib/bosapi-monitor/local.json (atomic replacement)
  -> read-only container mount: /app/monitor/local.json
  -> backend background sampler: BOS_MONITOR_NODE_DIR=/app/monitor
  -> cached GET /api/v1/admin/status -> super-admin System status page
```

The host timer and backend sampler each run about every 30 seconds. A page refresh only reads the cached backend snapshot; it does not run the collector. Application traffic/process metrics do not depend on the host JSON, so they can work while the node list is empty. The app does not discover hosts or SSH into them.

### Prerequisites and change boundaries

- Select the intended deployment host explicitly. Confirm the Docker context and `DOCKER_HOST` target that same host. The bind source path belongs to the daemon host, not necessarily the machine running the Docker CLI.
- Run commands from the target's repository root, not from inside the app container. The supplied installer requires Linux, systemd, Python 3, and root via approved `sudo`. It uses `useradd` and `/usr/sbin/nologin`; verify those are available on the chosen distribution. Non-systemd hosts and macOS/Windows development are not covered by this installation procedure.
- Confirm the normal stack/frontend delivery prerequisites first. Existing installations must retain their original private `deploy/stack.env`, persistent mounts, and compatible app image. Do not run `init` to enable monitoring on an existing instance.
- Record whether the collector user, service/timer, snapshot directory, marker, or systemd overrides already exist. Before replacing an existing collector, retain the previous script, packaged units, and overrides in approved backup storage, and record the prior timer state and node identity.
- Installation writes `/usr/local/lib/bosapi-monitor`, `/etc/systemd/system/bosapi-monitor.*`, and `/var/lib/bosapi-monitor`, creates a dedicated system account if missing, and enables/starts the timer. This is an explicit host change, not a read-only check. Reinstallation replaces the packaged script and units; it is not an automatic rollback mechanism.
- Adding/removing the app mount can recreate the app container and briefly interrupt service. Use an approved change window. Keep a recoverable prior deployment and do not change business data or credentials as part of monitoring setup.

Check prerequisites without starting services:

```sh
set -e
python3 --version
systemctl --version
command -v useradd
test -x /usr/sbin/nologin
docker context show
printf 'DOCKER_HOST=%s\n' "${DOCKER_HOST:-<not set>}"
test -f deploy/monitor/install.sh
test -f deploy/monitor/compose.monitor.yml
test -f deploy/stack.env
```

The final check assumes normal private configuration has already been prepared. If an existing installation's original environment file is missing, stop for restoration; do not generate a replacement.

### Install and verify the host collector

```sh
set -e
sudo bash deploy/monitor/install.sh
sudo systemctl is-active bosapi-monitor.timer
sudo systemctl --no-pager status bosapi-monitor.timer
sudo systemctl show bosapi-monitor.service -p Result -p ExecMainStatus
sudo journalctl -u bosapi-monitor.service -n 30 --no-pager
test -r /var/lib/bosapi-monitor/local.json
ls -lh /var/lib/bosapi-monitor/local.json
```

Expected: the timer is active, the latest service run succeeded (`Result=success`, `ExecMainStatus=0`), and a readable snapshot exists. The service is `Type=oneshot`; `inactive (dead)` after a successful run is normal. Inspect the timer and last execution result instead of requiring a permanently running service process.

If installation fails, stop before enabling the app overlay and inspect bounded logs. Do not fix permissions by making the app privileged, mounting the Docker socket, or using world-writable directories.

### Enable the application integration

After the host snapshot exists, create the host-local marker:

```sh
touch deploy/monitor.enabled
```

For a new installation, or an approved source upgrade requiring a build:

```sh
./deploy/stack.sh up
```

For an existing installation with a compatible image, where only this runtime configuration is changing:

```sh
./deploy/stack.sh start
```

Choose one path, not both. `start` reuses images and does not compile source. A plain container restart does not add the new environment/mount. Do not use `down` as a prerequisite, change the Compose project name, or initialize new keys.

The marker makes all stack commands include `deploy/monitor/compose.monitor.yml`. It is ignored by Git, so a Git checkout does not enable monitoring. Do not use the base Compose file alone for subsequent operations on an enabled stack; include the overlay or use `stack.sh`.

The collector is a dedicated unprivileged `bosapi-monitor` user, with no IP network access, no capabilities, a read-only system view, low CPU/I/O priority, `CPUQuota=5%`, 64 MiB maximum memory and a 10-second service deadline. It runs about every 30 seconds, not in the business request path.

The only application mount is `/var/lib/bosapi-monitor:/app/monitor:ro`. No Docker socket, privileged container, host PID namespace or host `/proc` is exposed to the application. Snapshot files contain resource data and process names/PIDs only; they never contain process command lines, environments, credentials or file contents. They are readable by local users, so do not put confidential data in this directory.

The collector reads `/proc` counters and `statvfs` for explicitly configured local filesystems. Root `/` is the default. Add `--disk /mount/path` in a systemd service override to observe separate data mounts; same-device paths are deduplicated. Avoid network filesystems because filesystem calls can block in the kernel. Node network totals exclude loopback, veth and container bridge interfaces and do not represent per-container bandwidth.

Process sampling is best-effort and bounded to 4096 entries / 2 seconds; only the ten largest RSS entries are retained. Permission restrictions such as `hidepid` limit visibility. No process start/stop/kill actions are provided.

`local.json` is written using an atomic rename, with up to 120 CPU/memory/network trend points. Snapshot continuity breaks on node reboot, node identity change or a gap of two minutes. Backend snapshots older than 90 seconds (or from a future clock beyond 30 seconds) are stale, not healthy.

For multiple nodes, run the collector independently with a unique `--node-id` and arrange an operator-managed authenticated transfer of its JSON file to a unique filename in this directory. No SSH credentials or fleet-discovery privileges are granted to the application. The reader accepts up to 16 regular JSON files (256 KiB each), inspects at most 64 directory entries, and rejects symlinks and oversized collections. Do not copy the same node under multiple filenames. This provides node visibility, not automatic Kubernetes/Docker discovery.

## Acceptance checks

Use a bounded verification window of up to 120 seconds after enablement. Normally successive host/backend samples become visible within 30-60 seconds. The first host sample can lack CPU percentage and network rates because those require a delta between two samples. Do not substitute synthetic zeros for missing readings.

### 1. Verify host snapshot metadata

Read only selected metadata rather than dumping process summaries or private deployment configuration:

```sh
python3 - <<'PY'
import datetime
import json
from pathlib import Path

path = Path('/var/lib/bosapi-monitor/local.json')
node = json.loads(path.read_text(encoding='utf-8'))
sample = datetime.datetime.fromisoformat(node['sampled_at'].replace('Z', '+00:00'))
age = (datetime.datetime.now(datetime.timezone.utc) - sample).total_seconds()
print('id:', node['id'])
print('sampled_at:', node['sampled_at'])
print('age_seconds:', round(age, 1))
print('status:', node['status'])
print('errors:', node.get('errors', []))
print('size_bytes:', path.stat().st_size)
assert node['id'] and sample.tzinfo is not None
assert -30 <= age <= 90, 'Snapshot is stale or clock is ahead'
assert path.stat().st_size <= 256 * 1024, 'Snapshot exceeds reader limit'
PY
```

Repeat after about 30-60 seconds and confirm `sampled_at` advances. The default node ID is the host's hostname. Investigate `partial`, nonempty `errors`, or unexpected identity instead of treating JSON existence as a complete pass. A partial process list can result from the bounded scan or host permission restrictions; document that limitation.

### 2. Verify container integration

Select only the monitoring variable and file metadata; never dump the full container environment or resolved Compose configuration:

```sh
test -f deploy/monitor.enabled
docker compose \
  --env-file deploy/stack.env \
  -f deploy/docker-compose.yml \
  -f deploy/monitor/compose.monitor.yml \
  config --quiet
docker compose \
  --env-file deploy/stack.env \
  -f deploy/docker-compose.yml \
  -f deploy/monitor/compose.monitor.yml \
  exec -T app sh -c '
    printf "BOS_MONITOR_NODE_DIR=%s\n" "$BOS_MONITOR_NODE_DIR"
    test "$BOS_MONITOR_NODE_DIR" = /app/monitor || exit 1
    test -r /app/monitor/local.json || exit 1
    ls -lh /app/monitor/local.json
  '
./deploy/stack.sh status
```

Expected: the variable equals `/app/monitor`, the file is readable, and the normal services remain healthy. The overlay declares the mount read-only. Do not attempt a write test against the snapshot directory or print unrelated container metadata.

### 3. Verify the authenticated page/API

Sign in as a super administrator and open System status. Wait for the next backend sample, then select Read latest snapshot. Verify:

- Expected node identity/count: normally one node for this single-host installation.
- A recent node sample, no stale state, and no node-source warning.
- CPU, memory, configured filesystems, and network fields; check CPU/network again after successive samples.
- No unexplained collection errors, and application/dependency metrics still work.

If inspecting the existing authenticated response, `GET /api/v1/admin/status` returns `data.nodes` and `data.node_source`. Expected source is `ok` for a complete valid directory; individual node status must also be reviewed. Use the existing secure browser session; do not paste tokens, session cookies, MFA seeds, or private configuration into tickets. A gateway API key is not a substitute for the super-admin session.

Record PASS / FAIL / NOT RUN independently for timer, snapshot freshness, container integration, node UI/API, and optional TLS targets. If host monitoring was intentionally excluded, record that choice as NOT RUN, not PASS. A green no-threshold-alert message is not proof of a configured collector: `not_configured` is not itself an alert in the current UI.

## Troubleshooting empty or stale nodes

Trace the same chain in order: timer -> host JSON -> container mount/environment -> backend snapshot -> page. Do not restart unrelated services or regenerate credentials.

| Evidence | Meaning and next check |
| --- | --- |
| App request charts work, but nodes are empty | App metrics are independent; verify the marker, collector installation, and snapshot mount. |
| `node_source=not_configured` | `BOS_MONITOR_NODE_DIR` is empty, or the configured directory has no accepted JSON candidates and no reader error. Check the runtime variable and directory contents; an empty directory also produces this state. |
| `node_source=unavailable` | The configured directory could not be opened/read. Check the path, bind mount, directory permissions, and host security policy. |
| `node_source=partial` | One or more files failed the reader checks. Check unreadable/non-regular/symlink files, size above 256 KiB, invalid JSON, missing/invalid ID or timestamp, duplicate IDs, and collection limits. Valid nodes may still be shown. |
| `node_source=truncated` | Directory scan/node limits were reached (64 entries or up to 16 accepted nodes). Remove unrelated files through an approved change or reduce the collection; do not simply raise limits. |
| Node exists but is `stale` | Snapshot is more than 90 seconds old, or its timestamp is over 30 seconds in the future. Check timer execution, advancing timestamps, and clock synchronization. Old valid JSON is retained as a stale node rather than hidden. |
| Host JSON exists, container file does not | The overlay is not applied to the current container, a different daemon/host is selected, or the bind source is wrong. Updating the marker alone does not update the running container. |
| Mount update fails before app startup | The overlay sets `create_host_path: false`; install the collector to create the source directory first. An empty placeholder directory does not replace a working collector. |
| JSON is visible but the page still shows no node | Wait within the bounded sample window; inspect the authenticated source state and check for an old backend image or another replica. `start` does not build new code. |
| CPU/network rates are initially absent | Wait for two continuous samples. A reboot, node ID change, or gap of two minutes resets continuity. |
| Expected data disk is absent | Only `/` is configured by default; add explicit local disk paths as below. Same-device paths are deduplicated. |

Useful bounded diagnostics are `systemctl show` for the last service result, `journalctl -u bosapi-monitor.service -n 30 --no-pager`, and `./deploy/stack.sh status`. App logs may contain sensitive material; capture only relevant bounded output and redact it before sharing. Do not dump `stack.env`, full `docker inspect`, process command lines, or shell environments.

## Extra filesystems and stable node identity

The packaged collector checks `/` and uses the hostname as the node ID. For a separate local data filesystem or an explicit stable ID, create a systemd drop-in on the selected host:

```sh
sudo systemctl edit bosapi-monitor.service
```

Example editor content (replace the ID/path with operator-selected values):

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/python3 /usr/local/lib/bosapi-monitor/collect.py --node-id bosapi-node-01 --disk /srv/bosapi
```

`/` remains included by the script's default. The extra path must already exist, be locally mounted, and be readable/traversable by the collector. Same-device paths are deliberately deduplicated. Do not use network filesystems. Node IDs must be 1-128 letters, numbers, dots, underscores, or hyphens; assign unique IDs for multiple hosts. Changing the ID resets local trend continuity.

Apply and verify in the approved change window:

```sh
sudo systemctl daemon-reload
sudo systemctl start bosapi-monitor.service
sudo systemctl show bosapi-monitor.service -p Result -p ExecMainStatus
```

Repeat host snapshot and page acceptance after successive samples. Keep overrides in the deployment inventory; packaged files alone do not capture the effective collector configuration.

## Updates and host migrations

- Preserve `deploy/monitor.enabled` on an already-enabled host. Git ignores it; Preserve it explicitly with other deployment mechanisms, especially those using deletion during synchronization.
- `stack.sh up/start` does not refresh the installed `/usr/local/lib/bosapi-monitor/collect.py` or systemd units. If the approved release changes collector files, back up the previous installed files/overrides and rerun `sudo bash deploy/monitor/install.sh`, then repeat acceptance. A code upload alone leaves the old installed collector running.
- Reinstallation replaces packaged files but does not intentionally remove systemd drop-ins. Review their effective `ExecStart` for compatibility; do not assume new packaged defaults override an existing drop-in.
- Replacing the app image still requires the normal application backup and migration procedure. Preserve all original secrets and data mounts. Collector history is local and bounded, not business database state.
- A new/migrated host needs its own collector installation, marker, and mount verification. Do not assume copying the app repository transfers systemd services or activates host monitoring. Decide whether to preserve an explicit node ID and which disks to monitor; do not blindly transfer old snapshots as healthy current data.
- For multiple app replicas/hosts, verify each instance that can serve the status page. The application cache/history is instance-local; this feature is not cluster aggregation or automatic fleet discovery.

## TLS checks

Set in the private deployment `deploy/stack.env` (with the monitor overlay enabled):

```dotenv
BOS_MONITOR_TLS_TARGETS=gateway.example.com:443
```

Use the operator's actual approved served endpoint, not the example. Apply the setting with `./deploy/stack.sh start` when a compatible image is already available. A container restart alone does not inject changed environment values. This can recreate the app; schedule it accordingly. No target is inferred from `BOS_PRIMARY_DOMAIN`, and an empty target list intentionally shows zero certificates. Initial checks run with the backend collector; subsequent checks are spaced ten minutes apart, so a page refresh does not trigger a new TLS connection.

Verify the target name, check time, validity, and expiry in System status's TLS certificates tab. Record this separately from node acceptance. This is not certificate issuance/renewal and does not inspect every certificate file on disk.

Up to four explicit operator-configured targets, port 443 only, are checked every ten minutes. Each verified TLS connection has a two-second deadline, with a five-second shared cycle budget. No target can be supplied by a status request, Host header, or public platform setting. Invalid/expired certificates show certificate metadata when available from verification failure; TLS verification is never disabled and private keys are never read. Alert threshold: 30 days to expiration. These checks cover served certificates, not every certificate file on disk.

## Alerts and performance

UI hints cover stale/incomplete collection, failed dependencies, queue occupancy >=80%, historical queue drops/failures, host CPU/memory >=90%, disk/inodes >=85%, HTTP error rate >=5% with at least 20 completed requests in the same rolling 5-minute window, Redis persistence errors, and TLS validity/expiry. Older backends without rolling-window metadata are not displayed or alerted as supplying 5-minute error metrics. These are current snapshot hints, not durable alert events, notifications or availability/SLA measurements.

The browser polls every 30 seconds only while visible, can pause polling, and retains stale results with a warning on error. The API keeps the original MySQL-unavailable 503 envelope with diagnostic details. Collection and node-scan durations are exposed for observing the monitoring overhead. No external alert delivery or long-term time-series service is installed.

## Verification and rollback

```sh
go test -race ./internal/platform/telemetry
python3 -m unittest discover -s deploy/monitor -p 'test_*.py'
sudo systemctl status bosapi-monitor.timer
sudo systemctl show bosapi-monitor.service -p CPUUsageNSec -p MemoryPeak
```

Run Go tests from `backend`, Python tests from the repository root. Unit tests validate code behavior, not the state of a deployment host; use the acceptance checks above for operational verification.

## Disable and rollback

For a newly enabled integration that should be completely disabled, run from the target repository root in an approved change window:

```sh
set -e
rm -f deploy/monitor.enabled
./deploy/stack.sh start
sudo systemctl disable --now bosapi-monitor.timer
sudo systemctl stop bosapi-monitor.service
```

Proceed to stopping the collector only after the app update succeeds. The app update can recreate its container and briefly interrupt service. `disable --now` stops the timer; the separate service stop handles an already-running oneshot. Verify the timer is inactive, app services are healthy, and the host-monitoring variable/mount are absent from the updated container. The page will show no host nodes after the next backend snapshot; application metrics remain available.

Removing the marker also removes the overlay's `BOS_MONITOR_TLS_TARGETS` forwarding, so served TLS checks stop too. If TLS checks must remain enabled, plan a separate configuration rather than claiming this full-disable procedure preserves them. Conversely, stopping only the timer leaves old JSON mounted, and nodes become stale rather than disappearing.

Leave the account, installed files, drop-ins, and JSON directory for investigation; deletion is a separate approved cleanup. Disabling monitoring does not alter database contents or application credentials.

If the collector was already enabled before the change, do not use full disable as an upgrade rollback. Restore the retained previous script/packaged units and any changed overrides, run `systemctl daemon-reload`, restore the recorded timer state, and verify fresh samples. Restore the marker to its prior state and update the app only if its integration changed. If recovering the previous application image is necessary, follow the normal image/schema compatibility and backup procedure; this monitoring rollback does not reverse database migrations.
