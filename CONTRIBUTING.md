# Contributing

BOS API welcomes bug reports, focused improvements, and translations. Open an
[issue](https://github.com/varium-ai/bosapi/issues) to discuss substantial changes
before implementation. Contributions are provided under the project's
[MIT License](LICENSE); preserve applicable third-party notices.

For a bug report, include the deployed version, reproduction steps, expected and
actual results, and a sanitized request ID or error code. Remove credentials,
customer payloads, private addresses and personal data from logs and screenshots.

Keep changes focused and add regression coverage when changing behavior. Use
English for source comments, developer documentation and new diagnostics.
Product translations and Unicode test fixtures are supported.

## Local checks

```sh
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

Tests requiring MySQL or Redis must use dedicated disposable databases. Never
point test database variables at a production instance. The default test run
skips integrations that have no explicit test-service configuration.

New features must be evaluated against the community feature scope before they
are copied from the internal product. Review an explicit patch rather than
mirroring the internal repository and its history.

## Release checks

Before publishing a revision, review the community scope, licensing notices,
and private vulnerability-reporting channel, then run:

```sh
python3 tools/check_community_source.py --release --frontend-dist
```

Build the frontend before running the production-bundle check.
