
FROM golang:1.19.2-alpine3.16 as builder

ARG ${VERSION:-1.0.0}
MAINTAINER FullStory Engineering

# create non-privileged group and user
RUN addgroup -S grpcurl && adduser -S grpcurl -G grpcurl

WORKDIR /tmp/fullstorydev/grpcurl
# copy just the files/sources we need to build grpcurl
COPY *.go go.* /tmp/fullstorydev/grpcurl/
COPY cmd /tmp/fullstorydev/grpcurl/cmd
# and build a completely static binary (so we can use
# scratch as basis for the final image)
ENV CGO_ENABLED=0
ENV GO111MODULE=on
RUN go build -o /grpcurl \
    -ldflags "-w -extldflags \"-static\" -X \"main.version=${VERSION}\"" \
    ./cmd/grpcurl

# New FROM so we have a nice'n'tiny image
FROM scratch
WORKDIR /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /grpcurl /bin/grpcurl
USER grpcurl

ENTRYPOINT ["/bin/grpcurl"]
