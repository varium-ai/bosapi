# Backend development

The Go 1.26 application uses Gin, GORM, MySQL and optional Redis. Start with the
source-built Docker Compose workflow in [the project README](../README.md) for a
self-contained instance. Supply your own upstream model credentials.

For direct development, review [.env.example](.env.example), generate a random
32-byte encryption key and a JWT signing key of at least 32 bytes, and use a
separate development database. Do not copy production environment files. Startup
applies embedded migrations and requires initial administrator credentials for a
new database. Existing credentials and encrypted data require their original key.

```sh
go test -race ./...
go vet ./...
go build ./...
```

Tests needing MySQL, Redis or live upstream access require explicit test
configuration and otherwise skip those integrations. Use disposable services and
synthetic credentials. Unit and protocol tests use local fixtures.

ChatGPT/Codex OAuth channels support a bounded Chat Completions bridge for text,
images, and function tools in JSON/SSE. Unsupported controls and output return
conversion errors. Use Responses for full Codex capabilities; see the
[subscription account guide](docs/subscription-accounts.md).

Community resource limits are implemented in `internal/platform/edition` and
checked in the creation transaction. The shared settings lock prevents concurrent
creators and batch imports from exceeding capacity. SQL migrations in this source
snapshot establish a community database baseline; they must not be applied to an
existing commercial database.

The community `main.go` matches `open_source/main.go` and calls the shared process
entry point in `internal/server`. The optional detector contract lives in
`internal/platform/securitycontract` and defaults to a no-op. The proprietary
detector package and its signature, normalization and decoding code are absent.
Existing Dockerfile and Makefile build targets continue to build `backend/main.go`.

Fixed credential checks, current-input redaction, output code markers, tool
authorization, event isolation and evidence privacy remain available. Retain
their regression coverage when changing security integrations. Run
`python3 ../tools/check_community_source.py` to check the public entry point and
excluded detector/table dependencies before exporting the source.
