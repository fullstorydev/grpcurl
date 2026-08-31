# Releases of gRPCurl

This document provides instructions for building a release of `grpcurl`.

The release process consists of a handful of tasks:
1. Drop a release tag in git.
2. Build binaries for various platforms. This is done using the local `go` tool and uses `GOOS` and `GOARCH` environment variables to cross-compile for supported platforms.
3. Creates a release in GitHub, uploads the binaries, and creates provisional release notes (in the form of a change log).
4. Build a docker image for the new release.
5. Push the docker image to `ghcr.io`, with both a version tag and the "latest" tag.

Most of this is automated via a script in this same directory. The main thing you will need is a GitHub personal access token, which will be used for creating the release in GitHub (so you need write access to the fullstorydev/grpcurl repo) and to open a Homebrew pull request.

## Creating a new release

Go to the [Release workflow](https://github.com/fullstorydev/grpcurl/actions/workflows/release.yml), click **Run workflow**, and provide:

* **Branch**: the branch or commit to release from (usually `master`).
* **version**: the version number for the new release, in sem-ver format: `v<Major>.<Minor>.<Patch>`, e.g. `v2.3.4`.
* **dry_run**: check this to build everything but publish nothing. Useful for validating a change to the release tooling. The binaries are attached to the workflow run as an artifact.

The workflow then:
1. Verifies the version is well-formed and that the tag does not already exist.
2. Runs the tests.
3. Creates and pushes the release tag.
4. Cross-compiles binaries for all supported platforms, creates the GitHub release with generated release notes, and uploads the archives. (This is goreleaser, driven by `.goreleaser.yml`.)
5. Builds a multi-arch (`linux/amd64`, `linux/arm64`) Docker image and pushes it to `ghcr.io`, tagged with both the version and `latest`.

## Release notes

Release notes are generated, never hand-written. `changelog.use: github-native` in `.goreleaser.yml` hands the job to GitHub's own release-notes generator, which lists every PR merged since the previous tag with its title, link, and author, and credits new contributors.

`.github/release.yml` controls how those PRs are grouped. Labels are optional — an unlabeled PR lands in a catch-all "Changes" section — so this needs no ongoing upkeep. Two things make the notes read better, if you want them:
