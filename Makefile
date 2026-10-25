# Developer shortcuts. Run from the repository root.
TEMPL          := go run github.com/a-h/templ/cmd/templ@v0.3.1070
TAILWIND       := npx --no-install @tailwindcss/cli -i ./assets/css/input.css -o ./static/css/style.css
TEST_DB_URL    ?= postgres://test:test@localhost:5433/dbplatform_test?sslmode=disable

.PHONY: help gen templ css dev dev-templ dev-css dev-go vet test test-integration test-db-up test-db-down

help:
	@echo "make gen               generate templ code and Tailwind CSS"
	@echo "make dev               watch templ + Tailwind and run the app (needs the .env variables exported)"
	@echo "make vet               go vet"
	@echo "make test              unit tests (integration tests skip without TEST_DATABASE_URL)"
	@echo "make test-integration  start the test PostgreSQL and run every test against it"
	@echo "make test-db-down      stop the test PostgreSQL"

gen: templ css

templ:
	$(TEMPL) generate

css:
	$(TAILWIND)

dev:
	$(MAKE) -j3 dev-templ dev-css dev-go

dev-templ:
	$(TEMPL) generate --watch

dev-css:
	$(TAILWIND) --watch

dev-go:
	go run .

vet:
	go vet ./...

test:
	go test ./...

test-db-up:
	docker compose --profile test up -d --wait postgres-test

test-db-down:
	docker compose --profile test rm -sf postgres-test

test-integration: test-db-up
	TEST_DATABASE_URL='$(TEST_DB_URL)' go test -count=1 ./...
