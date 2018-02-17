SRCS := $(shell find . -name '*.go')
PKGS := $(shell go list ./...)

.PHONY: all
all: deps lint test

.PHONY: deps
deps:
	go get -d -v -t $(PKGS)

.PHONY: updatedeps
updatedeps:
	go get -d -v -t -u -f $(PKGS)

.PHONY: install
install:
	go install $(PKGS)

.PHONY: golint
golint:
	go get github.com/golang/lint/golint
	for file in $(SRCS); do \
		golint $${file}; \
		if [ -n "$$(golint $${file})" ]; then \
			exit 1; \
		fi; \
	done

.PHONY: checkgofmt
checkgofmt:
	gofmt -s -l $(SRCS)
	if [ -n "$$(gofmt -s -l $(SRCS))" ]; then \
		exit 1; \
	fi

.PHONY: vet
vet:
	go vet $(PKGS)

.PHONY:
errcheck:
	go get github.com/kisielk/errcheck
	errcheck $(PKGS)

.PHONY: staticcheck
staticcheck:
	@go get honnef.co/go/tools/cmd/staticcheck
	staticcheck $(PKGS)

.PHONY: unused
unused:
	@go get honnef.co/go/tools/cmd/unused
	unused $(PKGS)

.PHONY: lint
lint: golint checkgofmt vet errcheck staticcheck unused

.PHONY: test
test:
	go test -race $(PKGS)

.PHONY: clean
clean:
	go clean -i $(PKGS)
