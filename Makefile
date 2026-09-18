# Misc
.DEFAULT_GOAL := help

# Application
APP := mailtail
ENV_FILE ?= .env
APP_VERSION ?= dev

# Tooling
GO := go
NPM := npm
WEB_DIR := web
WEB_DIST := $(WEB_DIR)/dist
DATA_DIR := data
GO_CACHE := $(CURDIR)/.cache/go-build
GO_MOD_CACHE := $(CURDIR)/.cache/gomod
GO_ENV := GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE)
# renovate: datasource=docker depName=node versioning=docker
NODE_BUILD_IMAGE ?= node:24-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd

# Runtime
HTTP_ADDR ?= :8080
SMTP_ADDR ?= :8025
HTTP_PORT ?= 8080
SMTP_PORT ?= 8025

# Docker
DOCKER_IMAGE ?= mailtail:dev
DOCKER_CONTAINER ?= mailtail
DOCKER_VOLUME ?= mailtail-data
DOCKER_ENV_FILE = $(if $(wildcard $(ENV_FILE)),--env-file $(ENV_FILE),)

# MegaLinter version
# renovate: datasource=docker depName=ghcr.io/oxsecurity/megalinter versioning=docker
_ML_VERSION := v10

# Files changed relative to the default branch, formatted for MegaLinter.
_FILES = $$(git diff --name-only origin/main | tr '\n' ',')
_CWD := $(CURDIR)

ifneq ($(wildcard $(ENV_FILE)),)
include $(ENV_FILE)
export
endif

#########################################################################
# MegaLinter helpers
#########################################################################

_check-for-all:
	@printf '%s\n' \
		"================================== Warning! ==================================" \
		"== Linting the whole codebase can take a very long time." \
		"==============================================================================="
	@printf "Are you sure? [y/N] " && read ans && [ "$${ans:-N}" = y ]

_check-for-changed-files:
	@if [ -z "$(_FILES)" ]; then \
		printf '%s\n' "No files to lint"; \
		exit 1; \
	fi

define mega-linter-runner
	docker run --rm \
		-v /var/run/docker.sock:/var/run/docker.sock:rw \
		-v "$(_CWD):/tmp/lint:rw" \
		-e DEFAULT_WORKSPACE=/tmp/lint \
		-e GITHUB_ACTIONS=false \
		-e MEGALINTER_CONFIG=.mega-linter.yml \
		-e REPORT_OUTPUT_FOLDER=none \
		$(strip $(2)) \
		ghcr.io/oxsecurity/$(strip $(1)):$(_ML_VERSION)
endef

#########################################################################
# Development
#########################################################################

## Create local cache and data directories
.PHONY: setup
setup:
	mkdir -p $(GO_CACHE) $(GO_MOD_CACHE) $(DATA_DIR)

## Install all project dependencies
.PHONY: install
install: install-web

## Install frontend dependencies
.PHONY: install-web
install-web:
	cd $(WEB_DIR) && $(NPM) install

## Synchronize Go module files
.PHONY: tidy
tidy: setup
	env $(GO_ENV) $(GO) mod tidy

## Format all Go packages
.PHONY: fmt
fmt:
	$(GO) fmt ./...

## Verify that tracked Go files are formatted
.PHONY: fmt-check
fmt-check:
	@unformatted="$$(git ls-files -z '*.go' | xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		printf "The following Go files are not formatted:\n%s\n" "$$unformatted"; \
		exit 1; \
	fi

## Verify that go.mod and go.sum are tidy
.PHONY: mod-check
mod-check: setup
	env $(GO_ENV) $(GO) mod tidy -diff

## Run Go static analysis
.PHONY: vet
vet: setup
	env $(GO_ENV) $(GO) vet ./...

## Run Go tests
.PHONY: test
test: setup
	env $(GO_ENV) $(GO) test ./...

## Run Go tests with the race detector and randomized order
.PHONY: test-race
test-race: setup
	env $(GO_ENV) $(GO) test -race -shuffle=on -count=1 ./...

## Run Go tests and enforce the CI coverage floor
.PHONY: coverage
coverage: setup
	env $(GO_ENV) $(GO) test -coverprofile=/tmp/mailtail-coverage.out ./...
	@coverage="$$(env $(GO_ENV) $(GO) tool cover -func=/tmp/mailtail-coverage.out | awk '/^total:/ { gsub(/%/, "", $$3); print $$3 }')"; \
	awk -v actual="$$coverage" -v minimum="30.0" 'BEGIN { \
		printf "Total coverage: %.1f%% (minimum: %.1f%%)\n", actual, minimum; \
		exit !(actual >= minimum); \
	}'

## Run the local backend merge checks
.PHONY: check
check: fmt-check mod-check vet test-race coverage

#########################################################################
# MegaLinter
#########################################################################

## Lint changed files with all available file linters
.PHONY: lint
lint: _check-for-changed-files
	$(call mega-linter-runner,megalinter,-e SKIP_CLI_LINT_MODES=project -e MEGALINTER_FILES_TO_LINT=$(_FILES))

## Lint changed files and apply automatic fixes where possible
.PHONY: lint-fix
lint-fix: _check-for-changed-files
	$(call mega-linter-runner,megalinter,-e SKIP_CLI_LINT_MODES=project -e MEGALINTER_FILES_TO_LINT=$(_FILES) -e APPLY_FIXES=all)

## Lint changed CI-related files with the lighter ci_light flavor
.PHONY: lint-ci
lint-ci: _check-for-changed-files
	$(call mega-linter-runner,megalinter-ci_light,-e SKIP_CLI_LINT_MODES=project -e MEGALINTER_FILES_TO_LINT=$(_FILES))

## Alias for lint-ci
.PHONY: lint-dev
lint-dev: lint-ci

## Lint changed CI-related files and apply automatic fixes
.PHONY: lint-ci-fix
lint-ci-fix: _check-for-changed-files
	$(call mega-linter-runner,megalinter-ci_light,-e SKIP_CLI_LINT_MODES=project -e MEGALINTER_FILES_TO_LINT=$(_FILES) -e APPLY_FIXES=all)

## Alias for lint-ci-fix
.PHONY: lint-dev-fix
lint-dev-fix: lint-ci-fix

## Lint all files with the lighter ci_light flavor
.PHONY: lint-ci-all
lint-ci-all:
	$(call mega-linter-runner,megalinter-ci_light,-e SKIP_CLI_LINT_MODES=project)

## Alias for lint-ci-all
.PHONY: lint-dev-all
lint-dev-all: lint-ci-all

## Lint and fix all files with the lighter ci_light flavor
.PHONY: lint-ci-all-fix
lint-ci-all-fix:
	$(call mega-linter-runner,megalinter-ci_light,-e SKIP_CLI_LINT_MODES=project -e APPLY_FIXES=all)

## Alias for lint-ci-all-fix
.PHONY: lint-dev-all-fix
lint-dev-all-fix: lint-ci-all-fix

## Lint every file with all file-based linters
.PHONY: lint-all-files
lint-all-files: _check-for-all
	$(call mega-linter-runner,megalinter,-e SKIP_CLI_LINT_MODES=project)

## Lint and fix every file with all file-based linters
.PHONY: lint-all-files-fix
lint-all-files-fix: _check-for-all
	$(call mega-linter-runner,megalinter,-e SKIP_CLI_LINT_MODES=project -e APPLY_FIXES=all)

## Lint the whole repository with file and project linters
.PHONY: lint-all
lint-all: _check-for-all
	$(call mega-linter-runner,megalinter,)

## Lint and fix the whole repository with file and project linters
.PHONY: lint-all-fix
lint-all-fix: _check-for-all
	$(call mega-linter-runner,megalinter,-e APPLY_FIXES=all)

#########################################################################
# Build and run
#########################################################################

## Build the backend binary and frontend assets
.PHONY: build
build: test build-web
	env $(GO_ENV) $(GO) build -ldflags "-X main.version=$(APP_VERSION)" -o $(APP) ./cmd/mailtail

## Build frontend assets
.PHONY: build-web
build-web:
	@if node -e 'const [major, minor] = process.versions.node.split(".").map(Number); process.exit((major === 20 && minor >= 19) || major >= 22 ? 0 : 1)' >/dev/null 2>&1; then \
		cd $(WEB_DIR) && $(NPM) run build; \
	else \
		printf "%s\n" "Local Node.js is too old for Vite; building frontend assets in Docker using $(NODE_BUILD_IMAGE)."; \
		docker run --rm \
			--user $$(id -u):$$(id -g) \
			-v "$(CURDIR)/$(WEB_DIR):/src/web" \
			-w /src/web \
			-e HOME=/tmp \
			-e npm_config_cache=/tmp/npm-cache \
			$(NODE_BUILD_IMAGE) \
			sh -lc 'npm ci && npm run build'; \
	fi

## Build and run MailTail with environment-aware local configuration
.PHONY: run
run: build
	MAILTAIL_DATA_DIR=$(DATA_DIR) \
	MAILTAIL_HTTP_ADDR=$(HTTP_ADDR) \
	MAILTAIL_SMTP_ADDR=$(SMTP_ADDR) \
	MAILTAIL_ADMIN_USERNAME="$(MAILTAIL_ADMIN_USERNAME)" \
	MAILTAIL_ADMIN_PASSWORD="$(MAILTAIL_ADMIN_PASSWORD)" \
	MAILTAIL_ALLOWED_ORIGINS="$(MAILTAIL_ALLOWED_ORIGINS)" \
	MAILTAIL_MAILFAIL_ENABLED="$(MAILTAIL_MAILFAIL_ENABLED)" \
	MAILTAIL_ALLOWED_REMOTE_IPS="$(MAILTAIL_ALLOWED_REMOTE_IPS)" \
	MAILTAIL_ACCEPTED_RCPT_DOMAINS="$(MAILTAIL_ACCEPTED_RCPT_DOMAINS)" \
	MAILTAIL_ACCEPTED_FROM_DOMAINS="$(MAILTAIL_ACCEPTED_FROM_DOMAINS)" \
	./$(APP)

## Start the Vite development server
.PHONY: dev-web
dev-web:
	cd $(WEB_DIR) && $(NPM) run dev

## Alias for run
.PHONY: dev
dev: run

#########################################################################
# Docker
#########################################################################

## Build the production Docker image
.PHONY: docker-build
docker-build: test
	docker build --build-arg APP_VERSION=$(APP_VERSION) -t $(DOCKER_IMAGE) .

## Build and run MailTail in Docker
.PHONY: docker-run
docker-run: docker-build
	docker volume create $(DOCKER_VOLUME)
	docker rm -f $(DOCKER_CONTAINER) >/dev/null 2>&1 || true
	docker run \
		--name $(DOCKER_CONTAINER) \
		-p $(SMTP_PORT):8025 \
		-p $(HTTP_PORT):8080 \
		-v $(DOCKER_VOLUME):/data \
		$(DOCKER_ENV_FILE) \
		-e MAILTAIL_ADMIN_USERNAME="$(MAILTAIL_ADMIN_USERNAME)" \
		-e MAILTAIL_ADMIN_PASSWORD="$(MAILTAIL_ADMIN_PASSWORD)" \
		-e MAILTAIL_ALLOWED_ORIGINS="$(MAILTAIL_ALLOWED_ORIGINS)" \
		-e MAILTAIL_MAILFAIL_ENABLED="$(MAILTAIL_MAILFAIL_ENABLED)" \
		-e MAILTAIL_ALLOWED_REMOTE_IPS="$(MAILTAIL_ALLOWED_REMOTE_IPS)" \
		-e MAILTAIL_ACCEPTED_RCPT_DOMAINS="$(MAILTAIL_ACCEPTED_RCPT_DOMAINS)" \
		-e MAILTAIL_ACCEPTED_FROM_DOMAINS="$(MAILTAIL_ACCEPTED_FROM_DOMAINS)" \
		$(DOCKER_IMAGE)

## Stop the MailTail container
.PHONY: docker-stop
docker-stop:
	docker stop $(DOCKER_CONTAINER)

## Remove the MailTail container
.PHONY: docker-rm
docker-rm:
	docker rm -f $(DOCKER_CONTAINER)

## Follow MailTail container logs
.PHONY: docker-logs
docker-logs:
	docker logs -f $(DOCKER_CONTAINER)

## Remove generated frontend assets
.PHONY: clean
clean:
	rm -rf $(WEB_DIST)

#########################################################################
# Help
#########################################################################

## Print this help message
.PHONY: help
help:
	@awk '{ \
		if ($$0 ~ /^.PHONY: [a-zA-Z\-_\.0-9]+$$/) { \
			helpCommand = substr($$0, index($$0, ":") + 2); \
			if (helpMessage) { \
				printf "\033[36m%-24s\033[0m %s\n", helpCommand, helpMessage; \
				helpMessage = ""; \
			} \
		} else if ($$0 ~ /^## /) { \
			if (helpMessage) { \
				helpMessage = helpMessage " " substr($$0, 4); \
			} else { \
				helpMessage = substr($$0, 4); \
			} \
		} else if ($$0 !~ /^$$/) { \
			helpMessage = ""; \
		} \
	}' $(MAKEFILE_LIST)
