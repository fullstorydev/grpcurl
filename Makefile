dev_build_version=$(shell git describe --tags --always --dirty)

# TODO: run golint and errcheck, but only to catch *new* violations and
# decide whether to change code or not (e.g. we need to be able to whitelist
# violations already in the code). They can be useful to catch errors, but
# they are just too noisy to be a requirement for a CI -- we don't even *want*
# to fix some of the things they consider to be violations.
.PHONY: ci
ci: deps checkgofmt vet staticcheck ineffassign predeclared test

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
	@go install github.com/goreleaser/goreleaser@v1.5.0
	goreleaser release --rm-dist

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

.PHONY: staticcheck
staticcheck:
	@go install honnef.co/go/tools/cmd/staticcheck@v0.3.3
	staticcheck ./...

.PHONY: ineffassign
ineffassign:
	@go install github.com/gordonklaus/ineffassign@7953dde2c7bf
	ineffassign .

.PHONY: predeclared
predeclared:
	@go install github.com/nishanths/predeclared@5f2f810c9ae6
	predeclared ./...

# Intentionally omitted from CI, but target here for ad-hoc reports.
.PHONY: golint
golint:
	@go install golang.org/x/lint/golint@v0.0.0-20210508222113-6edffad5e616
	golint -min_confidence 0.9 -set_exit_status ./...

# Intentionally omitted from CI, but target here for ad-hoc reports.
.PHONY: errcheck
errcheck:
	@go install github.com/kisielk/errcheck@v1.2.0
	errcheck ./...

.PHONY: test
test:
	go test -race ./...
