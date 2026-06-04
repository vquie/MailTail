APP := mailtail
ENV_FILE ?= .env
GO := go
NPM := npm
WEB_DIR := web
DATA_DIR := data
GO_CACHE := $(CURDIR)/.cache/go-build
GO_MOD_CACHE := $(CURDIR)/.cache/gomod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)
HTTP_ADDR ?= :8080
SMTP_ADDR ?= :8025
WEB_DIST := $(WEB_DIR)/dist
APP_VERSION ?= dev
DOCKER_IMAGE ?= mailtail:dev
DOCKER_CONTAINER ?= mailtail
DOCKER_VOLUME ?= mailtail-data
HTTP_PORT ?= 8080
SMTP_PORT ?= 8025
MAILFAIL_RULES_CONTAINER_PATH ?= /app/mailfail.yaml
MEGALINTER_IMAGE ?= oxsecurity/megalinter:v9
MEGALINTER_WORKDIR := /tmp/lint
MEGALINTER_COMMON_ENV := -e DEFAULT_WORKSPACE=$(MEGALINTER_WORKDIR) -e GITHUB_ACTIONS=false -e REPORT_OUTPUT_FOLDER=none -e SKIP_CLI_LINT_MODES=project
MEGALINTER_COMMON_ARGS := --rm -v $(CURDIR):$(MEGALINTER_WORKDIR)
DOCKER_ENV_FILE = $(if $(wildcard $(ENV_FILE)),--env-file $(ENV_FILE),)
MAILFAIL_RULES_MOUNT = $(if $(strip $(MAILTAIL_MAILFAIL_RULES_FILE)),-v $(abspath $(MAILTAIL_MAILFAIL_RULES_FILE)):$(MAILFAIL_RULES_CONTAINER_PATH):ro,)

ifneq ($(wildcard $(ENV_FILE)),)
include $(ENV_FILE)
export
endif

.PHONY: help setup install install-web tidy fmt test lint lint-fix build build-web clean \
	run dev-web dev docker-build docker-run docker-stop docker-rm docker-logs

help:
	@printf "%s\n" \
		"Available targets:" \
		"  make run           Run MailTail with .env-aware local config" \
		"  make docker-run    Run MailTail as a Docker container with .env-aware config and optional MailFail rule mount" \
		"  make setup         Create local cache and data directories" \
		"  make install       Install frontend dependencies" \
		"  make tidy          Sync Go modules" \
		"  make fmt           Format Go code" \
		"  make test          Run Go tests" \
		"  make lint          Run MegaLinter in Docker" \
		"  make lint-fix      Run MegaLinter with automatic fixes enabled" \
		"  make build         Build backend binary and frontend assets" \
		"  make build-web     Build frontend assets only" \
		"  make dev-web       Start the Vite dev server" \
		"  make dev           Alias for make run" \
		"  make docker-build  Build the Docker image" \
		"  make docker-stop   Stop the MailTail container" \
		"  make docker-rm     Remove the MailTail container" \
		"  make docker-logs   Follow MailTail container logs" \
		"  make clean         Remove generated frontend assets"

setup:
	mkdir -p $(GO_CACHE) $(GO_MOD_CACHE) $(DATA_DIR)

install: install-web

install-web:
	cd $(WEB_DIR) && $(NPM) install

tidy: setup
	env $(GO_ENV) $(GO) mod tidy

fmt:
	$(GO) fmt ./...

test: setup
	env $(GO_ENV) $(GO) test ./...

lint:
	docker run $(MEGALINTER_COMMON_ARGS) $(MEGALINTER_COMMON_ENV) $(MEGALINTER_IMAGE)

lint-fix:
	docker run $(MEGALINTER_COMMON_ARGS) $(MEGALINTER_COMMON_ENV) -e APPLY_FIXES=all $(MEGALINTER_IMAGE)

build: setup build-web
	env $(GO_ENV) $(GO) build -ldflags "-X main.version=$(APP_VERSION)" -o $(APP) ./cmd/mailtail

build-web:
	cd $(WEB_DIR) && $(NPM) run build

run: build-web
	MAILTAIL_DATA_DIR=$(DATA_DIR) \
	MAILTAIL_HTTP_ADDR=$(HTTP_ADDR) \
	MAILTAIL_SMTP_ADDR=$(SMTP_ADDR) \
	MAILTAIL_ADMIN_USERNAME="$(MAILTAIL_ADMIN_USERNAME)" \
	MAILTAIL_ADMIN_PASSWORD="$(MAILTAIL_ADMIN_PASSWORD)" \
	MAILTAIL_ALLOWED_ORIGINS="$(MAILTAIL_ALLOWED_ORIGINS)" \
	MAILTAIL_MAILFAIL_ENABLED="$(MAILTAIL_MAILFAIL_ENABLED)" \
	MAILTAIL_MAILFAIL_RULES_FILE="$(MAILTAIL_MAILFAIL_RULES_FILE)" \
	MAILTAIL_ALLOWED_REMOTE_IPS="$(MAILTAIL_ALLOWED_REMOTE_IPS)" \
	MAILTAIL_ACCEPTED_RCPT_DOMAINS="$(MAILTAIL_ACCEPTED_RCPT_DOMAINS)" \
	MAILTAIL_ACCEPTED_FROM_DOMAINS="$(MAILTAIL_ACCEPTED_FROM_DOMAINS)" \
	./$(APP)

dev-web:
	cd $(WEB_DIR) && $(NPM) run dev

dev: run

docker-build:
	docker build --build-arg APP_VERSION=$(APP_VERSION) -t $(DOCKER_IMAGE) .

docker-run: docker-build
	docker volume create $(DOCKER_VOLUME)
	docker rm -f $(DOCKER_CONTAINER) >/dev/null 2>&1 || true
	docker run \
		--name $(DOCKER_CONTAINER) \
		-p $(SMTP_PORT):8025 \
		-p $(HTTP_PORT):8080 \
		-v $(DOCKER_VOLUME):/data \
		$(MAILFAIL_RULES_MOUNT) \
		$(DOCKER_ENV_FILE) \
		-e MAILTAIL_ADMIN_USERNAME="$(MAILTAIL_ADMIN_USERNAME)" \
		-e MAILTAIL_ADMIN_PASSWORD="$(MAILTAIL_ADMIN_PASSWORD)" \
		-e MAILTAIL_ALLOWED_ORIGINS="$(MAILTAIL_ALLOWED_ORIGINS)" \
		-e MAILTAIL_MAILFAIL_ENABLED="$(MAILTAIL_MAILFAIL_ENABLED)" \
		-e MAILTAIL_MAILFAIL_RULES_FILE="$(if $(strip $(MAILTAIL_MAILFAIL_RULES_FILE)),$(MAILFAIL_RULES_CONTAINER_PATH),)" \
		-e MAILTAIL_ALLOWED_REMOTE_IPS="$(MAILTAIL_ALLOWED_REMOTE_IPS)" \
		-e MAILTAIL_ACCEPTED_RCPT_DOMAINS="$(MAILTAIL_ACCEPTED_RCPT_DOMAINS)" \
		-e MAILTAIL_ACCEPTED_FROM_DOMAINS="$(MAILTAIL_ACCEPTED_FROM_DOMAINS)" \
		$(DOCKER_IMAGE)

docker-stop:
	docker stop $(DOCKER_CONTAINER)

docker-rm:
	docker rm -f $(DOCKER_CONTAINER)

docker-logs:
	docker logs -f $(DOCKER_CONTAINER)

clean:
	rm -rf $(WEB_DIST)
