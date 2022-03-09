#!/bin/bash

# strict mode
set -euo pipefail
IFS=$'\n\t'

if [[ -z ${DRY_RUN:-} ]]; then
    PREFIX=""
else
    PREFIX="echo"
fi

# input validation
if [[ -z ${GITHUB_TOKEN:-} ]]; then
    echo "GITHUB_TOKEN environment variable must be set before running." >&2
    exit 1
fi
if [[ $# -ne 1 || $1 == "" ]]; then
    echo "This program requires one argument: the version number, in 'vM.N.P' format." >&2
    exit 1
fi
VERSION=$1

# Change to root of the repo
cd "$(dirname "$0")/.."

# GitHub release

$PREFIX git tag "$VERSION"
# make sure GITHUB_TOKEN is exported, for the benefit of this next command
export GITHUB_TOKEN
GO111MODULE=on $PREFIX make release
# if that was successful, it could have touched go.mod and go.sum, so revert those
$PREFIX git checkout go.mod go.sum

# Docker release

# make sure credentials are valid for later push steps; this might
# be interactive since this will prompt for username and password
# if there are no valid current credentials.
$PREFIX docker login
echo "$VERSION" > VERSION

# Docker Buildx support is included in Docker 19.03
# Below step installs emulators for different architectures on the host
# This enables running and building containers for below architectures mentioned using --platforms
$PREFIX docker run --privileged --rm tonistiigi/binfmt:qemu-v6.1.0 --install all
# Create a new builder instance
export DOCKER_CLI_EXPERIMENTAL=enabled
$PREFIX docker buildx create --use --name multiarch-builder --node multiarch-builder0
# push to docker hub, both the given version as a tag and for "latest" tag
$PREFIX docker buildx build --platform linux/amd64,linux/s390x,linux/arm64,linux/ppc64le --tag fullstorydev/grpcurl:${VERSION} --tag fullstorydev/grpcurl:latest --push --progress plain --no-cache .
rm VERSION

# Homebrew release

URL="https://github.com/fullstorydev/grpcurl/archive/${VERSION}.tar.gz"
curl -L -o tmp.tgz "$URL"
SHA="$(sha256sum < tmp.tgz | awk '{ print $1 }')"
rm tmp.tgz
HOMEBREW_GITHUB_API_TOKEN="$GITHUB_TOKEN" $PREFIX brew bump-formula-pr --url "$URL" --sha256 "$SHA" grpcurl
