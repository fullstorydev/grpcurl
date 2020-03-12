FROM golang:1.11.10-alpine as builder
MAINTAINER FullStory Engineering

# currently, a module build requires gcc (so Go tool can build
# module-aware versions of std library; it ships only w/ the
# non-module versions)
RUN apk update && apk add --no-cache ca-certificates git gcc g++ libc-dev
# create non-privileged group and user
RUN addgroup -S grpcurl && adduser -S grpcurl -G grpcurl

WORKDIR /tmp/fullstorydev/grpcurl
# copy just the files/sources we need to build grpcurl
COPY VERSION *.go go.* /tmp/fullstorydev/grpcurl/
COPY cmd /tmp/fullstorydev/grpcurl/cmd
# and build a completely static binary (so we can use
# scratch as basis for the final image)
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GO111MODULE=on
RUN go build -o /grpcurl \
    -ldflags "-w -extldflags \"-static\" -X \"main.version=$(cat VERSION)\"" \
    ./cmd/grpcurl

# New FROM so we have a nice'n'tiny image
FROM scratch
WORKDIR /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /grpcurl /bin/grpcurl
USER grpcurl

ENTRYPOINT ["/bin/grpcurl"]
