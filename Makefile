dev_build_version=$(shell git describe --tags --always --dirty)

# TODO: run golint and errcheck, but only to catch *new* violations and
# decide whether to change code or not (e.g. we need to be able to whitelist
# violations already in the code). They can be useful to catch errors, but
# they are just too noisy to be a requirement for a CI -- we don't even *want*
# to fix some of the things they consider to be violations.
.PHONY: ci
ci: backup_gomod deps checkgofmt vet staticcheck ineffassign predeclared restore_gomod test

.PHONY: backup_gomod
backup_gomod:
	cp go.mod go.mod.bk

.PHONY: restore_gomod
restore_gomod:
	if [ -f go.mod.bk ]; then \
	    mv go.mod.bk go.mod \
	fi

.PHONY: deps
deps:
	go get -d -v -t ./...

.PHONY: updatedeps
updatedeps:
	go get -d -v -t -u -f ./...

.PHONY: install
install:
	go install -ldflags '-X "main.version=dev build $(dev_build_version)"' ./...

.PHONY: release
release:
	@GO111MODULE=off go get github.com/goreleaser/goreleaser
	goreleaser --rm-dist

.PHONY: docker
docker:
	@echo $(dev_build_version) > VERSION
	docker build -t fullstorydev/grpcurl:$(dev_build_version) .
	@rm VERSION

.PHONY: checkgofmt
checkgofmt:
	gofmt -s -l .
	@if [ -n "$$(gofmt -s -l .)" ]; then \
		exit 1; \
	fi

.PHONY: vet
vet:
	go vet ./...

# TODO: remove the ignored check; need it for now because it
# is complaining about a deprecated comment added to grpc,
# but it's not yet released. Once the new (non-deprecated)
# API is included in a release, we can move to that new
# version and fix the call site to no longer use deprecated
# method.
# This all works fine with Go modules, but without modules,
# CI is just getting latest master for dependencies like grpc.
.PHONY: staticcheck
staticcheck:
	@go get honnef.co/go/tools/cmd/staticcheck
	staticcheck ./...

.PHONY: ineffassign
ineffassign:
	@go get github.com/gordonklaus/ineffassign
	ineffassign .

.PHONY: predeclared
predeclared:
	@go get github.com/nishanths/predeclared
	predeclared .

# Intentionally omitted from CI, but target here for ad-hoc reports.
.PHONY: golint
golint:
	@go get golang.org/x/lint/golint
	golint -min_confidence 0.9 -set_exit_status ./...

# Intentionally omitted from CI, but target here for ad-hoc reports.
.PHONY: errcheck
errcheck:
	@go get github.com/kisielk/errcheck
	errcheck ./...

.PHONY: test
test:
	go test -race ./...
