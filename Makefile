# Developer shortcuts. Run from the repository root.
TEST_DB_URL    ?= postgres://test:test@localhost:5433/dbplatform_test?sslmode=disable

.PHONY: help build frontend-build frontend-dev dev vet test test-integration test-db-up test-db-down

help:
	@echo "make build             build frontend SPA and verify Go binary"
	@echo "make frontend-dev      run Vite dev server for frontend SPA"
	@echo "make frontend-build    build frontend SPA with Vite"
	@echo "make dev               run the Go app (needs the .env variables exported)"
	@echo "make vet               go vet"
	@echo "make test              unit tests (integration tests skip without TEST_DATABASE_URL)"
	@echo "make test-integration  start the test PostgreSQL and run every test against it"
	@echo "make test-db-down      stop the test PostgreSQL"

build: frontend-build
	go build .

frontend-build:
	cd frontend && npm run build

frontend-dev:
	cd frontend && npm run dev

dev:
	go run .

vet:
	go vet ./...

test:
	go test ./...

test-db-up:
	docker compose --profile test up -d postgres-test

test-db-down:
	docker compose --profile test down -v

test-integration: test-db-up
	@echo "Waiting for test PostgreSQL to be healthy..."
	@until docker compose --profile test exec -T postgres-test pg_isready -U test -d dbplatform_test >/dev/null 2>&1; do sleep 0.2; done
	@TEST_DATABASE_URL="$(TEST_DB_URL)" go test -v -race ./...
	@$(MAKE) test-db-down
