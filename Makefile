BUILD := build
CONTAINER_ENGINE := $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
GO ?= go
GOFILES := $(shell find . -name "*.go" -type f ! -path "./vendor/*")
GOLANGCI_LINT ?= golangci-lint
VERSION := $(shell git describe --tags --abbrev=0)
REVISION := $(shell git rev-parse --short HEAD)

.PHONY: info
info:
	@echo "Using container engine: $(CONTAINER_ENGINE)"
	@echo "Using Go: $(GO)"
	@echo "Using GolangCI-Lint: $(GOLANGCI_LINT)"

.PHONY: clean
clean:
	$(GO) clean -i ./...
	rm -rf $(BUILD)

.PHONY: test
test:
	GO111MODULE=on $(GO) test -race -mod=mod -tags netgo,builtinassets ./...

.PHONY: run
run:
	$(GO) run \
		-ldflags="-X main.Version=$(VERSION) \
	  			  -X main.Revision=$(REVISION)" \
		cmd/main.go

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run --timeout=10m

.PHONY: build
build:
	CGO_ENABLED=0 go build -mod=mod -tags netgo,builtinassets \
		-ldflags="-X main.Version=$(VERSION) \
		          -X main.Revision=$(REVISION)" \
		-x -o lokxy ./cmd/

testlocal-build:
	$(CONTAINER_ENGINE) build -f Dockerfile.local --load -t lokxy:latest .

.PHONY: tag
tag:
	@if [ -z "$(TAG)" ]; then \
		echo "TAG is required. Usage: TAG=v0.4.0 make release"; \
		exit 1; \
	fi

	# Ensure we're on the default branch (main)
	@git checkout main

	# Ensure it's up to date
	@git pull origin main

	# Ensure version tag does not already exist
	@if git rev-parse "$(TAG)" >/dev/null 2>&1; then \
		echo "Tag $(TAG) already exists."; \
		exit 1; \
	fi

	# Create annotated tag and push it
	@git commit --allow-empty -m "chore: release $(TAG)"
	@git tag -a $(TAG) -m "chore(release): $(TAG)"
	@git push origin $(TAG)
	$(CONTAINER_ENGINE) build -f Dockerfile.local --load -t lokxy:latest .


.PHONY: helm-docs
helm-docs:
	$(CONTAINER_ENGINE) run --rm \
		-v "$(PWD):/helm-docs" \
		-u $(shell id -u):$(shell id -g) \
		jnorwood/helm-docs:v1.11.0
