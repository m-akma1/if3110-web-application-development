# Developer shortcuts. Run from the repository root.
TEST_DB_URL    ?= postgres://test:test@localhost:5433/dbplatform_test?sslmode=disable

.PHONY: help build frontend-build frontend-dev dev vet test test-integration test-db-up test-db-down seed-large observability observability-down loadtest

help:
	@echo "make build             build frontend SPA and verify Go binary"
	@echo "make frontend-dev      run Vite dev server for frontend SPA"
	@echo "make frontend-build    build frontend SPA with Vite"
	@echo "make dev               run the Go app (needs the .env variables exported)"
	@echo "make vet               go vet"
	@echo "make test              unit tests (integration tests skip without TEST_DATABASE_URL)"
	@echo "make test-integration  start the test PostgreSQL and run every test against it"
	@echo "make test-db-down      stop the test PostgreSQL"
	@echo "make seed-large        create the 2,000,000-row events table on the demo target (batch mode demo)"
	@echo "make observability     start the stack with the collector, Tempo, Loki, Prometheus and Grafana (http://localhost:3000)"
	@echo "make observability-down stop it again"
	@echo "make loadtest          run the k6 stress test against the running stack (results in loadtest/results)"

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
	docker compose --profile test rm -sfv postgres-test

test-integration: test-db-up
	@echo "Waiting for test PostgreSQL to be healthy..."
	@until docker compose --profile test exec -T postgres-test pg_isready -U test -d dbplatform_test >/dev/null 2>&1; do sleep 0.2; done
	@TEST_DATABASE_URL="$(TEST_DB_URL)" go test -v -race ./...
	@$(MAKE) test-db-down

# Runs as the app role, so the platform (which connects as that role) owns the table.
seed-large:
	docker compose exec -T target-postgres sh -c 'PGPASSWORD="$$TARGET_APP_PASSWORD" psql -h 127.0.0.1 -U "$$TARGET_APP_USER" -d "$$POSTGRES_DB"' < docker/seed/large_events.sql

# The app exports traces and logs over OTLP to the collector; Prometheus scrapes app:9091.
observability:
	OTEL_TRACES_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp docker compose --profile observability up -d --build

observability-down:
	docker compose --profile observability down

loadtest:
	loadtest/run.sh
