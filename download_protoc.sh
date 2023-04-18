#!/usr/bin/env bash

set -e

cd $(dirname $0)

if [[ -z "$PROTOC_VERSION" ]]; then
  echo "Set PROTOC_VERSION env var to indicate the version to download" >&2
  exit 1
fi
PROTOC_OS="$(uname -s)"
PROTOC_ARCH="$(uname -m)"
case "${PROTOC_OS}" in
  Darwin) PROTOC_OS="osx" ;;
  Linux) PROTOC_OS="linux" ;;
  *)
    echo "Invalid value for uname -s: ${PROTOC_OS}" >&2
    exit 1
esac

# This is for macs with M1 chips. Precompiled binaries for osx/amd64 are not available for download, so for that case
# we download the x86_64 version instead. This will work as long as rosetta2 is installed.
if [ "$PROTOC_OS" = "osx" ] && [ "$PROTOC_ARCH" = "arm64" ]; then
  PROTOC_ARCH="x86_64"
fi

PROTOC="${PWD}/.tmp/protoc/bin/protoc"

if [[ "$(${PROTOC} --version 2>/dev/null)" != "libprotoc 3.${PROTOC_VERSION}" ]]; then
  rm -rf ./.tmp/protoc
  mkdir -p .tmp/protoc
  curl -L "https://github.com/google/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-${PROTOC_OS}-${PROTOC_ARCH}.zip" > .tmp/protoc/protoc.zip
  pushd ./.tmp/protoc && unzip protoc.zip && popd
fi

