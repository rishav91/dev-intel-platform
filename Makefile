.PHONY: up down logs build vet test test-isolation test-integration migrate run-gateway run-connector run-normalizer run-archiver run-relay send-sample ghcheck tidy

COMPOSE := docker compose -f deploy/docker-compose.dev.yml
# Each run-* target tees its JSON logs here so Promtail (in the dev stack) can
# tail them into Loki for trace-correlated log search. See docs/OBSERVABILITY.md.
LOGDIR := logs
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

# Incremental migrations against the *running* DB (the initdb hook only fires on a
# fresh volume). Tracked in schema_migrations + idempotent: each file is applied
# once; one already present in the DB (e.g. applied by the initdb hook, or a re-run)
# is adopted into the ledger instead of erroring. Run after pulling new migrations.
migrate: ## apply un-applied db/migrations/*.sql to the running Postgres (tracked, idempotent)
	@$(COMPOSE) exec -T postgres psql -U devintel -d devintel -v ON_ERROR_STOP=1 -qc \
	  "SET client_min_messages=warning; CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());" >/dev/null
	@for f in db/migrations/*.sql; do \
	  v=$$(basename $$f); \
	  if [ "$$($(COMPOSE) exec -T postgres psql -U devintel -d devintel -tAc "SELECT 1 FROM schema_migrations WHERE version='$$v'")" = "1" ]; then \
	    echo "  skip   $$v"; continue; \
	  fi; \
	  err=$$($(COMPOSE) exec -T postgres psql -U devintel -d devintel -v ON_ERROR_STOP=1 --single-transaction -qf - < $$f 2>&1); \
	  if [ $$? -eq 0 ]; then echo "  apply  $$v"; \
	  elif echo "$$err" | grep -qi "already exists"; then echo "  adopt  $$v (already present)"; \
	  else echo "  FAILED $$v"; echo "$$err"; exit 1; fi; \
	  $(COMPOSE) exec -T postgres psql -U devintel -d devintel -v ON_ERROR_STOP=1 -qc \
	    "INSERT INTO schema_migrations(version) VALUES ('$$v') ON CONFLICT DO NOTHING;" >/dev/null; \
	done
	@echo "migrations up to date"

run-gateway:
	@mkdir -p $(LOGDIR)
	go run ./services/webhook-gateway 2>&1 | tee $(LOGDIR)/webhook-gateway.log

run-connector: ## consume raw.github → enrich PRs via GraphQL → enriched.github (P1.C; pass-through without creds)
	@mkdir -p $(LOGDIR)
	go run ./services/connector-github 2>&1 | tee $(LOGDIR)/connector-github.log

run-normalizer: ## consume enriched.github → canonical events (needs run-connector upstream)
	@mkdir -p $(LOGDIR)
	go run ./services/normalizer 2>&1 | tee $(LOGDIR)/normalizer.log

run-archiver:  ## consume raw.github → archive payloads to SeaweedFS (replay net)
	@mkdir -p $(LOGDIR)
	go run ./services/archiver 2>&1 | tee $(LOGDIR)/archiver.log

run-relay:     ## drain the outbox → publish canonical events to Kafka (ADR-012)
	@mkdir -p $(LOGDIR)
	go run ./services/outbox-relay 2>&1 | tee $(LOGDIR)/outbox-relay.log

send-sample:   ## post the sample pull_request webhook to the gateway
	./scripts/send-sample-webhook.sh

ghcheck-list:  ## live: list this App's installations (needs GITHUB_APP_ID + key)
	go run ./cmd/ghcheck -list

ghcheck-repos: ## live: list repos accessible to an installation (INSTALL=<id>)
	go run ./cmd/ghcheck -repos -installation $(INSTALL)

ghcheck:       ## live smoke: mint token + detect caps (INSTALL=<id> REPO=<owner/name>)
	go run ./cmd/ghcheck -installation $(INSTALL) -repo $(REPO)
