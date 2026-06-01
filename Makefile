APP := mailtail
GO := go
NPM := npm
WEB_DIR := web
DATA_DIR := data
GO_CACHE := $(CURDIR)/.cache/go-build
GO_MOD_CACHE := $(CURDIR)/.cache/gomod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)
HTTP_ADDR ?= :8025
SMTP_ADDR ?= :1025
WEB_DIST := $(WEB_DIR)/dist
DOCKER_IMAGE ?= mailtail:dev
DOCKER_CONTAINER ?= mailtail
DOCKER_VOLUME ?= mailtail-data
HTTP_PORT ?= 8025
SMTP_PORT ?= 1025
MEGALINTER_IMAGE ?= oxsecurity/megalinter:v9
MEGALINTER_WORKDIR := /tmp/lint
MEGALINTER_COMMON_ENV := -e DEFAULT_WORKSPACE=$(MEGALINTER_WORKDIR) -e GITHUB_ACTIONS=false -e REPORT_OUTPUT_FOLDER=none
MEGALINTER_COMMON_ARGS := --rm -v $(CURDIR):$(MEGALINTER_WORKDIR)

.PHONY: help setup install install-web tidy fmt test lint lint-fix build build-web clean \
	run dev-web dev docker-build docker-run docker-stop docker-rm docker-logs

help:
	@printf "%s\n" \
		"Available targets:" \
		"  make setup         Create local cache and data directories" \
		"  make install       Install frontend dependencies" \
		"  make tidy          Sync Go modules" \
		"  make fmt           Format Go code" \
		"  make test          Run Go tests" \
		"  make lint          Run MegaLinter in Docker" \
		"  make lint-fix      Run MegaLinter with automatic fixes enabled" \
		"  make build         Build backend binary and frontend assets" \
		"  make build-web     Build frontend assets only" \
		"  make run           Run MailTail with built frontend assets" \
		"  make dev-web       Start the Vite dev server" \
		"  make dev           Alias for make run" \
		"  make docker-build  Build the Docker image" \
		"  make docker-run    Run MailTail as a Docker container" \
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
	env $(GO_ENV) $(GO) build -o $(APP) ./cmd/mailtail

build-web:
	cd $(WEB_DIR) && $(NPM) run build

run: build-web
	MAILTAIL_DATA_DIR=$(DATA_DIR) MAILTAIL_HTTP_ADDR=$(HTTP_ADDR) MAILTAIL_SMTP_ADDR=$(SMTP_ADDR) ./$(APP)

dev-web:
	cd $(WEB_DIR) && $(NPM) run dev

dev: run

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-run: docker-build
	docker volume create $(DOCKER_VOLUME)
	docker rm -f $(DOCKER_CONTAINER) >/dev/null 2>&1 || true
	docker run \
		--name $(DOCKER_CONTAINER) \
		-p $(SMTP_PORT):1025 \
		-p $(HTTP_PORT):8025 \
		-v $(DOCKER_VOLUME):/data \
		$(DOCKER_IMAGE)

docker-stop:
	docker stop $(DOCKER_CONTAINER)

docker-rm:
	docker rm -f $(DOCKER_CONTAINER)

docker-logs:
	docker logs -f $(DOCKER_CONTAINER)

clean:
	rm -rf $(WEB_DIST)
