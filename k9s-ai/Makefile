NAME            := k9s-ai
VERSION         ?= v0.1.0
PACKAGE         := github.com/derailed/k9s
OUTPUT_BIN      ?= execs/${NAME}
GO_FLAGS        ?=
GO_TAGS         ?= netgo
CGO_ENABLED     ?=0
GIT_REV         ?= $(shell git rev-parse --short HEAD)

IMG_NAME        := derailed/k9s-ai
IMAGE           := ${IMG_NAME}:${VERSION}

SOURCE_DATE_EPOCH ?= $(shell date +%s)
ifeq ($(shell uname), Darwin)
DATE            ?= $(shell TZ=UTC /bin/date -j -f "%s" ${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
else
DATE            ?= $(shell date -u -d @${SOURCE_DATE_EPOCH} +"%Y-%m-%dT%H:%M:%SZ")
endif

default: help

bundler:                 ## Embed Copilot CLI binary
	@go tool bundler

build: bundler           ## Build the CLI
	@CGO_ENABLED=${CGO_ENABLED} go build ${GO_FLAGS} \
	-ldflags "-w -s -X ${PACKAGE}/cmd.version=${VERSION} -X ${PACKAGE}/cmd.commit=${GIT_REV} -X ${PACKAGE}/cmd.date=${DATE}" \
	-a -tags=${GO_TAGS} -o ${OUTPUT_BIN} ../main.go

test:                    ## Run all tests
	@go clean --testcache && go test ../...

release:                 ## Run goreleaser
	@goreleaser release --config .goreleaser.yml --clean

snapshot:                ## Run goreleaser snapshot
	@goreleaser release --config .goreleaser.yml --clean --snapshot

img:                     ## Build Docker image
	@docker build --rm -t ${IMAGE} -f Dockerfile ..

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":[^:]*?## "}; {printf "\033[38;5;69m%-30s\033[38;5;38m %s\033[0m\n", $$1, $$2}'
