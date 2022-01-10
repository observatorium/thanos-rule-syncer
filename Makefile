SHELL=/usr/bin/env bash -o pipefail
TMP_DIR := $(shell pwd)/tmp
BIN_DIR ?= $(TMP_DIR)/bin
LIB_DIR ?= $(TMP_DIR)/lib
FIRST_GOPATH := $(firstword $(subst :, ,$(shell go env GOPATH)))
OS ?= $(shell uname -s | tr '[A-Z]' '[a-z]')
ARCH ?= $(shell uname -m)

VERSION := $(strip $(shell [ -d .git ] && git describe --always --tags --dirty))
BUILD_DATE := $(shell date -u +"%Y-%m-%d")
BUILD_TIMESTAMP := $(shell date -u +"%Y-%m-%dT%H:%M:%S%Z")
VCS_BRANCH := $(strip $(shell git rev-parse --abbrev-ref HEAD))
VCS_REF := $(strip $(shell [ -d .git ] && git rev-parse --short HEAD))
DOCKER_REPO ?= quay.io/observatorium/thanos-rule-syncer

THANOS ?= $(BIN_DIR)/thanos
THANOS_VERSION ?= 0.17.0
OBSERVATORIUM ?= $(BIN_DIR)/observatorium
HYDRA ?= $(BIN_DIR)/hydra
GOLANGCILINT ?= $(FIRST_GOPATH)/bin/golangci-lint
GOLANGCILINT_VERSION ?= v1.21.0
EMBEDMD ?= $(BIN_DIR)/embedmd
SHELLCHECK ?= $(BIN_DIR)/shellcheck

default: thanos-rule-syncer
all: clean lint test thanos-rule-syncer

tmp/help.txt: thanos-rule-syncer
	./thanos-rule-syncer --help &> tmp/help.txt || true

README.md: $(EMBEDMD) tmp/help.txt
	$(EMBEDMD) -w README.md

thanos-rule-syncer: main.go $(wildcard *.go) $(wildcard */*.go)
	go mod tidy
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=amd64 GO111MODULE=on GOPROXY=https://proxy.golang.org go build -mod mod -a -ldflags '-s -w' -o $@ .

.PHONY: build
build: thanos-rule-syncer

.PHONY: format
format: $(GOLANGCILINT)
	$(GOLANGCILINT) run --fix --enable-all -c .golangci.yml

.PHONY: go-fmt
go-fmt:
	@fmt_res=$$(gofmt -d -s $$(find . -type f -name '*.go' -not -path './jsonnet/vendor/*' -not -path '${TMP_DIR}/*')); if [ -n "$$fmt_res" ]; then printf '\nGofmt found style issues. Please check the reported issues\nand fix them if necessary before submitting the code for review:\n\n%s' "$$fmt_res"; exit 1; fi

.PHONY: shellcheck
shellcheck: $(SHELLCHECK)
	$(SHELLCHECK) $(shell find . -type f -name "*.sh" -not -path "${TMP_DIR}/*")

.PHONY: lint
lint: $(GOLANGCILINT) go-fmt shellcheck
	$(GOLANGCILINT) run -v --enable-all -c .golangci.yml

.PHONY: test
test: build test-unit test-integration

.PHONY: test-unit
test-unit:
	CGO_ENABLED=1 GO111MODULE=on go test -mod mod -v -race -short ./...

.PHONY: test-integration
test-integration: build integration-test-dependencies
	PATH=$(BIN_DIR):$(FIRST_GOPATH)/bin:$$PATH LD_LIBRARY_PATH=$$LD_LIBRARY_PATH:$(LIB_DIR) ./test/integration.sh

.PHONY: clean
clean:
	-rm tmp/help.txt
	-rm -rf tmp/bin
	-rm thanos-rule-syncer

.PHONY: container
container: Dockerfile
	@docker build --build-arg BUILD_DATE="$(BUILD_TIMESTAMP)" \
		--build-arg VERSION="$(VERSION)" \
		--build-arg VCS_REF="$(VCS_REF)" \
		--build-arg VCS_BRANCH="$(VCS_BRANCH)" \
		--build-arg DOCKERFILE_PATH="/Dockerfile" \
		-t $(DOCKER_REPO):$(VCS_BRANCH)-$(BUILD_DATE)-$(VERSION) .
	@docker tag $(DOCKER_REPO):$(VCS_BRANCH)-$(BUILD_DATE)-$(VERSION) $(DOCKER_REPO):latest

.PHONY: container-push
container-push: container
	docker push $(DOCKER_REPO):$(VCS_BRANCH)-$(BUILD_DATE)-$(VERSION)
	docker push $(DOCKER_REPO):latest

.PHONY: container-release
container-release: VERSION_TAG = $(strip $(shell [ -d .git ] && git tag --points-at HEAD))
container-release: container
	# https://git-scm.com/docs/git-tag#Documentation/git-tag.txt---points-atltobjectgt
	@docker tag $(DOCKER_REPO):$(VCS_BRANCH)-$(BUILD_DATE)-$(VERSION) $(DOCKER_REPO):$(VERSION_TAG)
	docker push $(DOCKER_REPO):$(VERSION_TAG)
	docker push $(DOCKER_REPO):latest

.PHONY: integration-test-dependencies
integration-test-dependencies: $(THANOS) $(HYDRA) $(OBSERVATORIUM)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(LIB_DIR):
	mkdir -p $@

$(THANOS): | $(BIN_DIR)
	@echo "Downloading Thanos"
	curl -L "https://github.com/thanos-io/thanos/releases/download/v$(THANOS_VERSION)/thanos-$(THANOS_VERSION).$$(go env GOOS)-$$(go env GOARCH).tar.gz" | tar --strip-components=1 -xzf - -C $(BIN_DIR)

$(OBSERVATORIUM): | $(BIN_DIR)
	go build -mod=mod -o $@ github.com/observatorium/api

$(HYDRA): | $(BIN_DIR)
	@echo "Downloading Hydra"
	curl -L "https://github.com/ory/hydra/releases/download/v1.7.4/hydra_1.7.4_linux_64-bit.tar.gz" | tar -xzf - -C $(BIN_DIR) hydra

$(EMBEDMD): | $(BIN_DIR)
	go build -mod=mod -o $@ github.com/campoy/embedmd

$(GOLANGCILINT):
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCILINT_VERSION)/install.sh \
		| sed -e '/install -d/d' \
		| sh -s -- -b $(FIRST_GOPATH)/bin $(GOLANGCILINT_VERSION)

$(SHELLCHECK): | $(BIN_DIR)
	curl -sNL "https://github.com/koalaman/shellcheck/releases/download/stable/shellcheck-stable.$(OS).$(ARCH).tar.xz" | tar --strip-components=1 -xJf - -C $(BIN_DIR)
