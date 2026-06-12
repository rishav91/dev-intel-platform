.PHONY: up down logs build vet test test-isolation run-gateway run-normalizer run-archiver run-relay send-sample tidy

COMPOSE := docker compose -f deploy/docker-compose.dev.yml
# Runtime/test connections use the least-privilege app role so RLS engages
# (the superuser `devintel` bypasses RLS). See db/migrations/0003_app_role.sql.
DSN ?= postgres://devintel_app:devintel_app@localhost:5432/devintel

up:            ## start local stack (Redpanda, Postgres/Citus, Redis, SeaweedFS)
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

run-archiver:  ## consume raw.github → archive payloads to SeaweedFS (replay net)
	go run ./services/archiver

run-relay:     ## drain the outbox → publish canonical events to Kafka (ADR-012)
	go run ./services/outbox-relay

send-sample:   ## post the sample pull_request webhook to the gateway
	./scripts/send-sample-webhook.sh
