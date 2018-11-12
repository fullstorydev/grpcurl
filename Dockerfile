FROM golang:1.9-alpine as builder
RUN apk add --no-cache git gcc make musl-dev bash

WORKDIR /go/src/github.com/fullstorydev/grpcurl
COPY . .

RUN make deps install

FROM alpine:latest
RUN apk --no-cache add ca-certificates curl less nano bash bash-doc bash-completion
# Create a group and user
RUN addgroup -S appgroup && adduser -S user -G appgroup

# Tell docker that all future commands should run as the user user
USER user
COPY --from=builder /go/bin/grpcurl /usr/local/bin
WORKDIR /home/user