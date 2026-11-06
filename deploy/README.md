# Community deployment

The supported default is one source-built Docker Compose stack: application,
MySQL 8.4 and Redis 7.4. The application image includes the frontend and serves it
directly. A Docker-ready host needs Compose v2, Buildx, Bash, OpenSSL and curl.

## Start a new instance

```sh
./deploy/stack.sh init
# Edit deploy/stack.env privately if custom settings are needed.
./deploy/stack.sh up --source
./deploy/stack.sh status
curl --fail http://127.0.0.1:18080/health/ready
```

`up` also initializes missing configuration. Existing service credentials are
preserved. Read the administrator credentials from `deploy/stack.env` privately;
never paste the file into an issue. Sign in, enroll MFA, then configure your own
upstream provider and gateway Key.

## HTTPS and static delivery

Keep the application loopback-bound behind a trusted HTTPS reverse proxy. The
optional `compose.https.yml` and `Caddyfile` provide one configuration path; inspect
their domain and port requirements before enabling them. Set `BOS_PRIMARY_DOMAIN`
to the intended public HTTPS address. Check proxy body and timeout settings for
large or streaming model requests.

The default `BOS_WEB_DELIVERY=go` needs no host static root. Operators choosing
host Nginx static delivery should follow [STATIC_WEB.md](STATIC_WEB.md). Host
monitoring is optional and described in [monitor/README.md](monitor/README.md).

## Operations

- `./deploy/stack.sh up --source`: build and deploy this checkout; never fetch Git.
- `./deploy/stack.sh start`: start existing configured images.
- `./deploy/stack.sh logs`: inspect service logs without sharing secrets.
- `./deploy/stack.sh down`: stop services, retaining named data volumes.
- `./deploy/stack.sh up --image YOUR_IMAGE`: opt into an explicitly selected image.

No official community image registry is selected in this preparation snapshot.
Use the source workflow until release images and their provenance are confirmed.
Community upgrades use the manual source or explicitly selected image workflow
above. Online system updates and host updater agents are excluded.

Back up MySQL, gateway data and the original environment file before upgrades.
Database migrations can prevent an old image from reading new data; do not assume
that changing images reverses a migration. Never generate replacement encryption
keys for existing encrypted data. Test restoration on a disposable instance.

Blue/green, HA and failover controllers are excluded. The community schema is for
new databases and must not be applied to a commercial database.
