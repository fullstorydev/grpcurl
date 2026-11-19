dev_build_version=$(shell git describe --tags --always --dirty)

export PATH := $(shell pwd)/.tmp/protoc/bin:$(PATH)
export PROTOC_VERSION := 22.0
# Disable CGO for improved compatibility across distros
export CGO_ENABLED=0
export GOFLAGS=-trimpath
export GOWORK=off

# TODO: run golint and errcheck, but only to catch *new* violations and
# decide whether to change code or not (e.g. we need to be able to whitelist
# violations already in the code). They can be useful to catch errors, but
# they are just too noisy to be a requirement for a CI -- we don't even *want*
# to fix some of the things they consider to be violations.
.PHONY: ci
ci: deps checkgenerate lint test

.PHONY: deps
deps:
	go get -d -v -t ./...
	go mod tidy

.PHONY: updatedeps
updatedeps:
	go get -d -v -t -u -f ./...
	go mod tidy

.PHONY: install
install:
	go install -ldflags '-X "main.version=dev build $(dev_build_version)"' ./...

GORELEASER_VERSION := v2.5.0
# Extra flags for goreleaser, e.g. "--skip=publish" for a dry run.
GORELEASER_ARGS ?=

.PHONY: release
release:
	@go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	goreleaser release --clean $(GORELEASER_ARGS)

.PHONY: docker
docker:
	@echo $(dev_build_version) > VERSION
	docker build -t fullstorydev/grpcurl:$(dev_build_version) .
	@rm VERSION

.PHONY: generate
generate: .tmp/protoc/bin/protoc
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@a709e31e5d12
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.1.0
	@go install github.com/jhump/protoreflect/desc/sourceinfo/cmd/protoc-gen-gosrcinfo@v1.14.1
	go generate ./...
	go mod tidy

.PHONY: checkgenerate
checkgenerate: generate
	git status --porcelain -- '**/*.go'
	@if [ -n "$$(git status --porcelain -- '**/*.go')" ]; then \
		git diff -- '**/*.go'; \
		exit 1; \
	fi

.PHONY: lint
lint:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
	golangci-lint run ./...

.PHONY: test
test: deps
	CGO_ENABLED=1 go test -race ./...

.tmp/protoc/bin/protoc: ./Makefile ./download_protoc.sh
	./download_protoc.sh

