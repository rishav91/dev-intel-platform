.PHONY: up down logs build vet test test-isolation run-gateway run-normalizer send-sample tidy

COMPOSE := docker compose -f deploy/docker-compose.dev.yml
# Runtime/test connections use the least-privilege app role so RLS engages
# (the superuser `devintel` bypasses RLS). See db/migrations/0003_app_role.sql.
DSN ?= postgres://devintel_app:devintel_app@localhost:5432/devintel

up:            ## start local stack (Redpanda, Postgres/Citus, Redis, MinIO)
	$(COMPOSE) up -d

down:          ## stop stack and wipe volumes (re-applies migrations next up)
	$(COMPOSE) down -v

logs:
	$(COMPOSE) logs -f

tidy:
	go mod tidy

build:
	go build ./...

vet:
	go vet ./...

test:          ## unit tests (isolation test skips without POSTGRES_DSN)
	go test ./...

test-isolation: ## red-team RLS isolation test (requires running Postgres)
	POSTGRES_DSN=$(DSN) go test ./tests/red-team/... -run TestTenantIsolation -v

run-gateway:
	go run ./services/webhook-gateway

run-normalizer:
	go run ./services/normalizer

send-sample:   ## post the sample pull_request webhook to the gateway
	./scripts/send-sample-webhook.sh
