# Releases of gRPCurl

This document provides instructions for building a release of `grpcurl`.

The release process consists of a handful of tasks:
1. Drop a release tag in git.
2. Build binaries for various platforms. This is done using the local `go` tool and uses `GOOS` and `GOARCH` environment variables to cross-compile for supported platforms.
3. Creates a release in GitHub, uploads the binaries, and creates provisional release notes (in the form of a change log).
4. Build a docker image for the new release.
5. Push the docker image to Docker Hub, with both a version tag and the "latest" tag.
6. Submits a PR to update the [Homebrew](https://brew.sh/) recipe with the latest version.

Most of this is automated via a script in this same directory. The main thing you will need is a GitHub personal access token, which will be used for creating the release in GitHub (so you need write access to the fullstorydev/grpcurl repo) and to open a Homebrew pull request.

## Creating a new release

So, to actually create a new release, just run the script in this directory.

First, you need a version number for the new release, following sem-ver format: `v<Major>.<Minor>.<Patch>`. Second, you need a personal access token for GitHub.

We'll use `v2.3.4` as an example version and `abcdef0123456789abcdef` as an example GitHub token:

```sh
# from the root of the repo
GITHUB_TOKEN=abcdef0123456789abcd \
    ./releasing/do-release.sh v2.3.4
```

Wasn't that easy! There is one last step: update the release notes in GitHub. By default, the script just records a change log of commit descriptions. Use that log (and, if necessary, drill into individual PRs included in the release) to flesh out notes in the format of the `RELEASE_NOTES.md` file _in this directory_. Then login to GitHub, go to the new release, edit the notes, and paste in the markdown you just wrote.

That should be all there is to it! If things go wrong and you have to re-do part of the process, see the sections below.

----

### GitHub Releases
The GitHub release is the first step performed by the `do-release.sh` script. So generally, if there is an issue with that step, you can re-try the whole script.

Note, if running the script did something wrong, you may have to first login to GitHub and remove uploaded artifacts for a botched release attempt. In general, this is _very undesirable_. Releases should usually be considered immutable. Instead of removing uploaded assets and providing new ones, it is often better to remove uploaded assets (to make bad binaries no longer available) and then _release a new patch version_. (You can edit the release notes for the botched version explaining why there are no artifacts for it.)

The steps to do a GitHub-only release (vs. running the entire script) are the following:

```sh
# from the root of the repo
git tag v2.3.4
GITHUB_TOKEN=abcdef0123456789abcdef \
    GO111MODULE=on \
    make release
```

The `git tag ...` step is necessary because the release target requires that the current SHA have a sem-ver tag. That's the version it will use when creating the release.

This will create the release in GitHub with provisional release notes that just include a change log of commit messages. You still need to login to GitHub and revise those notes to adhere to the recommended format. (See `RELEASE_NOTES.md` in this directory.)

### Docker Hub Releases

To re-run only the Docker Hub release steps, you can manually run through each step in the "Docker" section of `do_release.sh`.

If the `docker push ...` steps fail, you may need to run `docker login`, enter your Docker Hub login credentials, and then try to push again.

### Homebrew Releases

The last step is to update the Homebrew recipe to use the latest version. First, we need to compute the SHA256 checksum for the source archive:

```sh
# download the source archive from GitHub
URL=https://github.com/fullstorydev/grpcurl/archive/v2.3.4.tar.gz
curl -L -o tmp.tgz $URL
# and compute the SHA
SHA="$(sha256sum < tmp.tgz | awk '{ print $1 }')"
```

To actually create the brew PR, you need your GitHub personal access token again, as well as the URL and SHA from the previous step:

```sh
HOMEBREW_GITHUB_API_TOKEN=abcdef0123456789abcdef \
    brew bump-formula-pr --url $URL --sha256 $SHA grpcurl
```

This creates a PR to bump the formula to the new version. When this PR is merged by brew maintainers, the new version becomes available!
