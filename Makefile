.PHONY: all build build-frontend build-backend run stop restart dev dev-frontend dev-backend test test-backend test-frontend clean

all: build

# Build the frontend assets and Go server for local runs.
build: build-frontend build-backend

build-frontend:
	@echo "==> Building React Frontend with pnpm..."
	@cd frontend && pnpm run build

build-backend:
	@echo "==> Building Go Backend..."
	@cd backend && mkdir -p bin && go build -trimpath -o bin/server .

# Local service execution, shutdown, and restart
run: build
	@echo "==> Starting BOS API server on http://localhost:8081 ..."
	@cd backend && BOS_WEB_DIR="$(abspath frontend/dist)" ./bin/server

stop:
	@echo "==> Stopping running server on port 8081..."
	@-lsof -ti:8081 | xargs kill -9 2>/dev/null || true
	@echo "==> Stopped."

restart: stop run

# Development mode (run in separate terminals)
dev:
	@echo "Please run 'make dev-backend' and 'make dev-frontend' in separate terminals."

dev-backend:
	@cd backend && go run .

dev-frontend:
	@cd frontend && pnpm run dev

# Test suites
test: test-backend test-frontend

test-backend:
	@echo "==> Running Go backend unit and regression tests..."
	@cd backend && go test -race -count=1 ./...

test-frontend:
	@echo "==> Typechecking frontend with pnpm..."
	@cd frontend && pnpm run build

clean:
	@rm -rf backend/bin frontend/dist
