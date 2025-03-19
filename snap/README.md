# packing and releasing
To pack the current branch to a snap package:

`snapcraft pack`

To install the package locally:

`snap install ./grpcurl_v[version tag]_amd64.snap --devmode`

To upload the snap to the edge channel:

`snapcraft upload --release edge ./grpcurl_v[version tag]_amd64.snap`

(you need to own the package name registration for this!)

# ownership
The snap's current owner is `pietro.pasotti@canonical.com`; who is very happy to support with maintaining the snap distribution and/or transfer its ownership to the developers.

Please reach out to me for questions regarding the snap; including:
- adding support for other architectures
- automating the release

Cheers and thanks for the awesome tool!