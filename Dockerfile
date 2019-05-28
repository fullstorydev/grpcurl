FROM golang:1.11.10-alpine as builder
MAINTAINER FullStory Engineering

RUN apk update && apk add --no-cache ca-certificates git gcc g++ libc-dev
# create non-privileged group and user
RUN addgroup -S grpcurl && adduser -S grpcurl -G grpcurl

# build grpcurl
WORKDIR /tmp/fullstorydev/grpcurl
# copy just the files/sources we need to build grpcurl
COPY VERSION *.go go.* /tmp/fullstorydev/grpcurl/
COPY cmd /tmp/fullstorydev/grpcurl/cmd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -o /grpcurl -ldflags "-w -extldflags \"-static\" -X \"main.version=$(cat VERSION)\"" ./cmd/grpcurl

# New FROM so we have a nice'n'tiny image
FROM scratch
WORKDIR /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /grpcurl /bin/grpcurl
USER grpcurl

ENTRYPOINT ["/bin/grpcurl"]
