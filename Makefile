# home-projects
#
#   make dev       backend and frontend, locally, against a throwaway database
#   make test      everything the brief says has to be green before a deploy
#   make sweep     the endpoint sweep against a running server
#   make up        the whole thing in Docker

SHELL := /bin/bash

# .env is local and never committed. It carries the owner password the test
# scripts sign in with, so nothing in this repository is a working credential.
# .env is local and never committed. It is read for the few values the targets
# below need — and only HP_PASSWORD is exported. A blanket `export` would push
# GIT_DIR into every `git` this Makefile runs, and that git would then take the
# server's repository directory for its own.
-include .env

# The check scripts sign in as the owner, so they take the password from the
# same place the server does. Nothing in this repository is a credential.
HP_PASSWORD ?= $(OWNER_PASSWORD)
export HP_PASSWORD
# The SSH check speaks to the server the way sshd and the wrapper do.
export GIT_SSH_SECRET
TEST_DB_PORT ?= 5545
TEST_DB_URL  ?= postgres://hp:hp@127.0.0.1:$(TEST_DB_PORT)/hptest?sslmode=disable
API          ?= http://127.0.0.1:5000

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- development

.PHONY: dev-db
dev-db: ## start a throwaway postgres for local work
	@docker start home-projects-devdb 2>/dev/null || \
	docker run -d --name home-projects-devdb \
		-e POSTGRES_USER=hp -e POSTGRES_PASSWORD=hp -e POSTGRES_DB=hp \
		-p 127.0.0.1:5544:5432 postgres:16-alpine
	@echo "postgres on 127.0.0.1:5544"

.PHONY: backend
backend: ## run the backend against the dev database
	cd backend && \
	DATABASE_URL="postgres://hp:hp@127.0.0.1:5544/hp?sslmode=disable" \
	JWT_SECRET="dev-jwt-secret-that-is-long-enough" \
	SECRET_KEY="dev-secret-key-that-is-long-enough" \
	DATA_DIR="$(PWD)/srv/data" GIT_DIR="$(PWD)/srv/git" \
	PUBLIC_URL="http://localhost:5173" COOKIE_SECURE=false ENV=development \
	OWNER_USERNAME=$${OWNER_USERNAME:-offlinebot} OWNER_PASSWORD=$${OWNER_PASSWORD:?set OWNER_PASSWORD, e.g. in .env} \
	go run ./cmd/server

.PHONY: frontend
frontend: ## run the frontend with hot reload
	cd frontend && npm install && npm run dev

# ----------------------------------------------------------------------- test

.PHONY: test-db
test-db:
	@docker start home-projects-testdb 2>/dev/null || \
	docker run -d --name home-projects-testdb \
		-e POSTGRES_USER=hp -e POSTGRES_PASSWORD=hp -e POSTGRES_DB=hptest \
		-p 127.0.0.1:$(TEST_DB_PORT):5432 postgres:16-alpine >/dev/null
	@for i in $$(seq 1 30); do \
		docker exec home-projects-testdb pg_isready -U hp -d hptest >/dev/null 2>&1 && break; \
		sleep 1; \
	done

.PHONY: test
test: test-db ## go vet, go test (incl. the single-use credential test), tsc
	cd backend && go vet ./...
	cd backend && TEST_DATABASE_URL="$(TEST_DB_URL)" go test ./...
	cd frontend && npm run typecheck

.PHONY: sweep
sweep: ## walk every endpoint and complain at any non-2xx
	python3 scripts/sweep.py --url $(API)

.PHONY: git-roundtrip
git-roundtrip: ## clone, push, and check the working tree followed
	python3 scripts/git_roundtrip.py --url $(API)

.PHONY: ssh-access
ssh-access: ## check what a key over SSH may see and write
	python3 scripts/ssh_access.py --url $(API)

.PHONY: password-push
password-push: ## check pushing with the repository password, and its limits
	python3 scripts/password_push.py --url $(API)

.PHONY: check
check: test sweep git-roundtrip ssh-access password-push ## what has to be green before a deploy

.PHONY: build
build: ## compile both halves
	cd backend && go build ./...
	cd frontend && npm run build

# ------------------------------------------------------------------ operation

.PHONY: up
up: ## start everything in Docker
	docker compose up -d --build

.PHONY: down
down:
	docker compose down

.PHONY: logs
logs:
	docker compose logs -f --tail=100

.PHONY: clean-test
clean-test:
	-docker rm -f home-projects-testdb home-projects-devdb
