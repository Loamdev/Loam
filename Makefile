# Loam developer tasks.
#
# Portable between Git Bash on Windows and any POSIX shell on Linux/macOS:
# recipes use only plain shell commands and let the Go toolchain decide the
# binary suffix (`go env GOEXE`), so `make build` produces bin/loam.exe on
# Windows and bin/loam elsewhere.

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
BINARY        ?= loam
BIN_DIR       ?= bin
DIST_DIR      ?= dist
PKGS          ?= ./...
COVERPROFILE  ?= coverage.out

GOEXE := $(shell $(GO) env GOEXE)
BIN   := $(BIN_DIR)/$(BINARY)$(GOEXE)

.DEFAULT_GOAL := help

.PHONY: help build install fmt vet lint test test-race cover tidy ci e2e clean

help: ## Show this help.
	@echo "Loam - make targets"
	@echo ""
	@echo "  build       Build the CLI into $(BIN)"
	@echo "  install     go install the CLI into GOBIN"
	@echo "  fmt         Format all Go code (gofmt -w)"
	@echo "  vet         go vet $(PKGS)"
	@echo "  lint        golangci-lint run $(PKGS)"
	@echo "  test        go test $(PKGS)"
	@echo "  test-race   go test -race $(PKGS)"
	@echo "  cover       Test with coverage, then print the per-function report"
	@echo "  tidy        go mod tidy"
	@echo "  ci          fmt + vet + test + build (run before pushing)"
	@echo "  e2e         go test -tags=e2e $(PKGS) -v (end-to-end suite; arrives in phase 6)"
	@echo "  clean       Remove build output and coverage profiles"
	@echo ""
	@echo "Override tools/paths, e.g.:  make build BIN_DIR=out"

build: ## Build the CLI.
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN) .
	@echo "built $(BIN)"

install: ## Install the CLI into GOBIN.
	$(GO) install .

fmt: ## Format all Go code.
	gofmt -w .

vet: ## Run go vet.
	$(GO) vet $(PKGS)

lint: ## Run golangci-lint.
	$(GOLANGCI_LINT) run $(PKGS)

test: ## Run the unit tests.
	$(GO) test $(PKGS)

test-race: ## Run the unit tests with the race detector.
	$(GO) test -race $(PKGS)

cover: ## Run tests with coverage and print the report.
	$(GO) test -covermode=atomic -coverprofile=$(COVERPROFILE) $(PKGS)
	$(GO) tool cover -func=$(COVERPROFILE)
	@echo "HTML report: $(GO) tool cover -html=$(COVERPROFILE)"

tidy: ## Tidy go.mod / go.sum.
	$(GO) mod tidy

ci: fmt vet test build ## Everything CI checks, locally.
	@echo "ci: ok"

e2e: ## Run the end-to-end suite (needs Docker; tests land in phase 6).
	$(GO) test -tags=e2e $(PKGS) -v

clean: ## Remove build output and coverage profiles.
	$(RM) -r $(BIN_DIR) $(DIST_DIR)
	$(RM) $(COVERPROFILE)
