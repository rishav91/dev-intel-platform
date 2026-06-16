.PHONY: up down logs build vet test test-isolation test-integration run-gateway run-connector run-normalizer run-archiver run-relay send-sample ghcheck tidy

COMPOSE := docker compose -f deploy/docker-compose.dev.yml
# Runtime/test connections use the least-privilege app role so RLS engages
# (the superuser `devintel` bypasses RLS). See db/migrations/0003_app_role.sql.
DSN ?= postgres://devintel_app:devintel_app@localhost:5432/devintel

# Auto-load .env (gitignored) so targets like `make ghcheck` pick up
# GITHUB_APP_ID / GITHUB_APP_PRIVATE_KEY_PATH without manual exporting.
# `export` propagates these to each recipe's environment. .env.example is the template.
ifneq (,$(wildcard .env))
include .env
export
endif

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

test-integration: ## live GitHub API test (needs GITHUB_APP_ID + key + GITHUB_TEST_* env)
	go test -tags=integration ./libs/go/githubapp/ -run Live -v

run-gateway:
	go run ./services/webhook-gateway

run-connector: ## consume raw.github → enrich PRs via GraphQL → enriched.github (P1.C; pass-through without creds)
	go run ./services/connector-github

run-normalizer: ## consume enriched.github → canonical events (needs run-connector upstream)
	go run ./services/normalizer

run-archiver:  ## consume raw.github → archive payloads to SeaweedFS (replay net)
	go run ./services/archiver

run-relay:     ## drain the outbox → publish canonical events to Kafka (ADR-012)
	go run ./services/outbox-relay

send-sample:   ## post the sample pull_request webhook to the gateway
	./scripts/send-sample-webhook.sh

ghcheck-list:  ## live: list this App's installations (needs GITHUB_APP_ID + key)
	go run ./cmd/ghcheck -list

ghcheck:       ## live smoke: mint token + detect caps (INSTALL=<id> REPO=<owner/name>)
	go run ./cmd/ghcheck -installation $(INSTALL) -repo $(REPO)
