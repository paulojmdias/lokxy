BUILD := build
GO ?= go
GOFILES := $(shell find . -name "*.go" -type f ! -path "./vendor/*")
GOFMT ?= gofmt
GOIMPORTS ?= goimports -local=github.com/paulojmdias/lokxy
STATICCHECK ?= staticcheck
VERSION := $(shell git describe --tags --abbrev=0)
REVISION := $(shell git rev-parse --short HEAD)

.PHONY: clean
clean:
	$(GO) clean -i ./...
	rm -rf $(BUILD)

.PHONY: static-check
static-check:
	$(STATICCHECK) ./...

.PHONY: fmt
fmt:
	$(GOFMT) -w -s $(GOFILES)

.PHONY: imports
imports:
	$(GOIMPORTS) -w $(GOFILES)

.PHONY: test
test:
	GO111MODULE=on $(GO) test -race -mod=mod -tags netgo,builtinassets ./...

.PHONY: build
build:
	CGO_ENABLED=0 go build -mod=mod -tags netgo,builtinassets \
		-ldflags="-X main.Version=$(VERSION) \
		          -X main.Revision=$(REVISION)" \
		-x -o lokxy ./cmd/

testlocal-build:
	docker build -f Dockerfile.local --load -t lokxy:latest .
