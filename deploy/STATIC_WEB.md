# Host Nginx static frontend delivery

Phase one defaults to **Nginx static delivery** for this project, while retaining the Docker frontend build, `/app/web` and `BOS_WEB_DIR` for recovery and compatible Go-only installations. Deployment scripts publish files but never install, edit or reload Nginx automatically.

## Delivery modes

Configure the private `deploy/stack.env` on the deployment host:

```dotenv
BOS_WEB_DELIVERY=nginx
BOS_WEB_ROOT=/var/www/bosapi
```

Missing delivery settings also default to `nginx` and `/var/www/bosapi`, including older environment files. `up` creates a missing static root before building or starting containers. If its parent requires administrator permissions, it invokes sudo to create a directory owned by the deployment user. New directories use mode 0755; existing directories and their permissions are preserved. Prepare the Nginx configuration below, or explicitly set `BOS_WEB_DELIVERY=go` to retain Go delivery. New community `init` files use Go delivery by default.

`BOS_WEB_DELIVERY=go` skips all static publication and requires neither Python nor Nginx. Go continues to serve the frontend and APIs. This also supports an existing catch-all reverse proxy. Compose retains its loopback port binding; an SSH tunnel can access a Go-only installation remotely. Public TLS and public port exposure remain deployment choices.

The two settings may also be overridden by the shell environment for one invocation. Private configuration is read as data, never sourced as shell code. Use simple literal values, optionally quoted, without variable interpolation.

## Initial host preparation

Requirements: Python 3.9+, Docker access for the deployment user, an existing host Nginx, and a dedicated writable static root **outside the checkout**. `./deploy/stack.sh up --source` creates a missing root automatically; sudo access is needed when the parent is not writable. An existing non-writable root must have its permissions corrected separately. Nginx requires read and directory traversal permissions, not write access. To prepare the directory manually or publish an already running application, replace the account names below with the actual host accounts:

```sh
sudo install -d -o DEPLOY_USER -g DEPLOY_GROUP -m 0755 /var/www/bosapi
# Configure nginx delivery and the root in the existing private stack.env.
./deploy/stack.sh publish-web
```

The first export uses the current app container's immutable frontend bytes, without rebuilding or restarting it. A read-only container with no mount overlapping `/app/web` is exported by its exact container ID. This remains valid when Docker 29 retags an image index and the running container's old image index is no longer creatable. Writable roots or overlapping mounts use a separate container created from the exact image ID with pulling disabled; the exporter never substitutes `latest`. Verify `current/index.html`, public documents, logos and assets before changing Nginx. Export validation checks HTML references, JS imports, Vite preload dependencies, CSS resources and public files from this checkout. Use a checkout matching the running release for the initial export; newer public files can intentionally fail that check.

Review and adapt these templates:

- [bosapi-connection.conf](nginx/bosapi-connection.conf) belongs once in the Nginx `http` context. Reuse an existing equivalent map instead of defining it twice.
- [bosapi-proxy.conf](nginx/bosapi-proxy.conf) belongs at `/etc/nginx/snippets/bosapi-proxy.conf`. Adjust the application port and preserve the site's forwarding, real-IP, request buffering and timeout policy.
- [bosapi-static.conf](nginx/bosapi-static.conf) replaces the catch-all `location /` in the existing app server. Install it at `/etc/nginx/snippets/bosapi-static.conf` and include it there. Retain TLS, server names, redirects, upload limits and maintenance gates. Adjust both roots if using another static directory.
- Production builds include precompressed `.gz` and `.br` sidecars. `bosapi-static.conf` serves `.gz` files with `gzip_static`. To serve the smaller Brotli files, install and load the Nginx Brotli filter/static modules, then include [bosapi-brotli.conf](nginx/bosapi-brotli.conf) in the application server. Run `nginx -t` before reload; do not include that file on a host without the Brotli modules.

The `http` context must include `mime.types`. Back up site files, validate with `sudo nginx -t`, then gracefully reload with `sudo systemctl reload nginx`. No deployment script runs these commands. Later version switches require no Nginx reload.

## Deployment sequence

In Nginx mode, `up` creates a missing publication root and validates it before updating containers; `start` validates an existing root. Both wait for the app to become healthy, then export and publish that app image's frontend. `status` never publishes. `publish-web` exports an already deployed image without updating the stack.

If publication fails after `up/start`, the backend may already be updated while the frontend remains old. The command exits unsuccessfully. Inspect the error and current link, resolve the problem, then retry `./deploy/stack.sh publish-web`. Deploy mutually compatible frontend/backend changes with this sequence; incompatible API migrations require a coordinated rollout.

### Desktop metadata in build output

`hidden frontend entry: docs/.DS_Store` means Finder metadata entered the image through `frontend/public`. Container startup may already have succeeded; the frontend publication failed before switching `current`. The Bake/Buildx warning is separate when the build subsequently completes.

Uploads and Docker contexts exclude `.DS_Store`, `Thumbs.db`, and AppleDouble `._*` files at every depth. The production frontend build also removes these regular files from its generated output, leaving source files untouched. The public-file completeness check omits only these metadata files, so an older copy left in the remote source tree cannot require them in a new image. Hidden files, symlinks, and private files in the exported artifact remain subject to strict validation.

After updating the checkout, use `./deploy/stack.sh up --source` on the intended host to rebuild and republish the frontend. Retrying only `publish-web` against the contaminated image will fail again. Do not clear build caches, delete shared assets, or bypass hidden-file validation. Check the successful publication message and `current` link afterward.

### Compressed asset collisions

An error such as `immutable asset collision: assets/xlsx-B2eTCt_Q.js.br` means the same asset filename has different bytes in the image and the shared static directory. Vite hashes the original JavaScript or CSS before the compression plugin runs. A different Node/Brotli/zlib version or compression setting can therefore produce a different `.br` or `.gz` file for identical source bytes.

The publisher preserves the existing compressed file when the uncompressed shared file matches the incoming file's SHA-256 and a retained manifest records that exact original/encoding pair. It also supports legacy sidecars generated directly on the host: if no retained manifest records the compressed filename, a manifest must record the matching original, and decoding the existing sidecar must reproduce that original exactly. Decoding is bounded by the expected original size. A compressed file already recorded with different bytes is still rejected, even if it decodes successfully.

The publisher copies the preserved encoding into the new release and records its checksum in the new manifest, keeping both old and new releases valid for rollback. Existing shared assets, old releases and old manifests are not rewritten. All other immutable collisions still fail before publication. A release using preserved encodings can differ from its image's compression bytes; its manifest describes the files actually published.

Verifying an unrecorded `.br` requires the system `libbrotlidec` library, loaded through Python's standard-library `ctypes`; no Python package or Node runtime is required. The publisher reports a missing library before switching `current`. Recorded Brotli files need no decoder, and gzip verification uses Python's standard library.

If this error came from an older publisher (including the version that only reuses manifest-recorded encodings), back up and update only `deploy/publish-web.py` on the deployment host with the fixed version from this checkout. Then inspect the selected frontend and retry publication only, using the actual `BOS_WEB_ROOT` if it differs from the default:

```sh
readlink /var/www/bosapi/current
./deploy/stack.sh status
./deploy/stack.sh publish-web
readlink /var/www/bosapi/current
```

No image rebuild, backend restart or Nginx reload is needed for this retry. A successful command prints the newly selected release; verify that `current` points to it, then refresh the browser to load its HTML. Do not delete shared assets, releases or manifests, or overwrite the conflicting file to bypass the check: existing tabs and rollback depend on those bytes. If the fixed publisher still reports a collision, compare the uncompressed asset in `/app/web` with the shared asset and check the retained manifest; changed originals, missing original history or corrupted files require investigation rather than automatic reuse.

## Versions and retention

```text
/var/www/bosapi/
  releases/<release>/       complete build with ordinary public files
  current -> releases/<release>
  previous -> releases/<previous-release>
  assets/                   retained content-hash resources
  manifests/<release>.json
  .publish.lock
```

The manifest records the immutable image ID and SHA-256 of every file. Only names matching Go's cache policy (`assets/*-HASH.extension`, at least eight hash characters) enter shared assets. Unversioned files stay in the release and are revalidated. Different content at an existing immutable URL fails publication. Directories use 755 and files 644; root and its parents must also be traversable by Nginx. Symlinked publication roots are rejected.

An OS lock serializes publishers and rollbacks. New files are validated before atomic replacement of `current`. Failure before the switch leaves the old entry intact; harmless new shared files or an unselected release can remain. Extraction copies the verified read-only frontend snapshot, or creates and removes a separate image container when snapshot immutability cannot be proven. It does not execute the image or change the running container. Docker failures report the daemon's underlying error. After a publication-only failure, retry `./deploy/stack.sh publish-web` without rebuilding or restarting the app.

Phase one performs **no automatic deletion**, preserving old lazy chunks and rollback candidates. Adopt a minimum 30-day window after a release was last active and review disk use periodically. Cleanup is a separate explicit maintenance operation: retain current and previous, retain every release within the window, and remove shared hashes only when no retained manifest references them. Do not age hashes by mtime, because current may still reference an old hash. Long-lived tabs beyond an eventual cleanup window can require a reload.

## Rollback

Read the target from `readlink /var/www/bosapi/previous` or list `manifests`:

```sh
python3 deploy/publish-web.py rollback --root /var/www/bosapi --release RELEASE_ID
```

Rollback verifies the retained release and shared hashes using its stored manifest, then swaps current and previous without reloading Nginx. It does not roll back the backend or database. The first publication has no previous release; recover using the original full-proxy Nginx configuration.

To return to Go delivery, restore the backed-up catch-all proxy, validate and reload Nginx, and set `BOS_WEB_DELIVERY=go`. Changing the delivery setting alone does not change Nginx. Keep the static directory for recovery. `/app/web` remains in the application image throughout phase one.

## Acceptance

Use the actual deployment address. If TLS terminates on an external edge, check that edge separately; this host's Nginx cannot change edge behavior. Compare a current asset URL and checksum against the host files and loopback response.

```sh
curl --fail --silent --show-error --connect-timeout 5 --max-time 20 -I https://YOUR_HOST/health/ready
curl --fail --silent --show-error --connect-timeout 5 --max-time 20 -I https://YOUR_HOST/docs
curl --fail --silent --show-error --connect-timeout 5 --max-time 20 -D - -o /dev/null -H 'Accept-Encoding: br, gzip' https://YOUR_HOST/assets/CURRENT_HASHED_FILE.js
curl --silent --show-error --connect-timeout 5 --max-time 20 -D - -o /dev/null https://YOUR_HOST/assets/missing.js
```

Check `/`, `/login`, `/admin/*`, `/key-portal/*`, `/docs`, `/docs/:slug` and refreshes. HTML must be uncached, hash assets immutable, ordinary public files revalidated, and missing resources 404 without SPA fallback or immutable error caching. Verify MIME, `Content-Encoding: br` when Brotli is enabled (otherwise `gzip`) on an actual compressed GET, and `Vary: Accept-Encoding`.

Check `/api/v1`, `/v1`, `/health`, `/models`, `/responses`, `/messages`, `/images`, `/chat/completions`, `/embeddings` and used child routes, query strings, streaming and WebSockets. Root GET aliases now bypass Go's SPA wrapper too; slash-boundary matching preserves lookalike frontend routes. Unauthenticated checks should return backend auth responses, not HTML. Verify actual login/MFA and inference separately from readiness.

After two releases, request an old lazy chunk and exercise rollback. Confirm static bypass through the effective Nginx locations and source access logs. Local checks do not establish public performance improvement or validate deployment credentials.

## Local verification

```sh
python3 -m unittest discover -s deploy/tests -p 'test_publish_web.py'
bash deploy/tests/stack-script-test.sh
NGINX_BIN=/path/to/nginx python3 -m unittest discover -s deploy/tests -p 'test_nginx_static.py'
```

The optional Nginx integration uses a temporary prefix and loopback ports; without a Nginx binary it reports a skip. It does not change system Nginx.
