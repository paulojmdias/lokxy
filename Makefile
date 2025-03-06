BUILD := build
GO ?= go
GOFILES := $(shell find . -name "*.go" -type f ! -path "./vendor/*")
GOLANGCI_LINT ?= golangci-lint
VERSION := $(shell git describe --tags --abbrev=0)
REVISION := $(shell git rev-parse --short HEAD)

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
	$(GOLANGCI_LINT) run --timeout=5m

.PHONY: build
build:
	CGO_ENABLED=0 go build -mod=mod -tags netgo,builtinassets \
		-ldflags="-X main.Version=$(VERSION) \
		          -X main.Revision=$(REVISION)" \
		-x -o lokxy ./cmd/

testlocal-build:
	docker build -f Dockerfile.local --load -t lokxy:latest .
