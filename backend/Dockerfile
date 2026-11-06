ARG BASE_IMAGE_REGISTRY=docker.io/library
FROM --platform=$BUILDPLATFORM ${BASE_IMAGE_REGISTRY}/node:22-alpine AS frontend-builder

WORKDIR /frontend
ENV NODE_OPTIONS=--max-old-space-size=768
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,id=bosapi-pnpm-store,target=/pnpm/store,sharing=locked \
    pnpm install --frozen-lockfile --prefer-offline --store-dir=/pnpm/store --package-import-method=copy
COPY frontend/ ./
RUN pnpm run build && touch /frontend-ready

FROM --platform=$BUILDPLATFORM ${BASE_IMAGE_REGISTRY}/golang:1.26-alpine AS builder

# Keep compilation serial on low-memory VMs. This empty marker has a stable
# checksum, so changed frontend assets do not invalidate the Go build layers.
COPY --from=frontend-builder /frontend-ready /tmp/frontend-ready

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org
ENV GOPROXY=${GOPROXY}
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,id=bosapi-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download
# Runtime resources and local binaries are not inputs to the Go compiler.
COPY backend/*.go ./
COPY backend/internal/ ./internal/
COPY backend/migrations/ ./migrations/
# Only compilation depends on the target architecture; share dependency layers.
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,id=bosapi-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -v -p 2 -trimpath -ldflags="-s -w" -o /gateway .

FROM ${BASE_IMAGE_REGISTRY}/alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S gateway && adduser -S -G gateway gateway && \
    mkdir -p /app/data && chown gateway:gateway /app/data
WORKDIR /app
COPY --from=builder /gateway /app/gateway
COPY --from=frontend-builder /frontend/dist /app/web
COPY THIRD_PARTY_NOTICES.txt LICENSING.md /app/licenses/
COPY third_party/licenses/ /app/licenses/third_party/licenses/
ENV BOS_WEB_DIR=/app/web
USER gateway
EXPOSE 8081
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8081/health || exit 1
ENTRYPOINT ["/app/gateway"]

# Release metadata changes every publish; keep it after all filesystem layers.
ARG BOS_VERSION=dev
ARG BOS_REVISION=unknown
LABEL org.opencontainers.image.version=$BOS_VERSION \
      org.opencontainers.image.revision=$BOS_REVISION
ENV BOS_BUILD_VERSION=$BOS_VERSION BOS_BUILD_REVISION=$BOS_REVISION
