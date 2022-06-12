"""Bazel macro to run a binary copy of gRPCurl."""


# sample link (for `_bindist("1.8.6", "osx", "arm64")`):
# https://github.com/fullstorydev/grpcurl/releases/download/v1.8.6/grpcurl_1.8.6_osx_arm64.tar.gz
def _bindist(version, os, arch):
    return "https://github.com/fullstorydev/grpcurl/releases/download/v%s/grpcurl_%s_%s_%s.tar.gz" % (
        version,
        version,
        os,
        arch
    )

# sample bundle (for `_bindist_bundle("1.8.6", "linux", archs = ["arm64", "s390x", "x86_32", "x86_64"])`):
# "linux": {
#     "arm64": _bindist("1.8.6", "linux", "arm64"),
#     "s390x": _bindist("1.8.6", "linux", "s390x"),
#     "x86_32": _bindist("1.8.6", "linux", "x86_32"),
#     "x86_64": _bindist("1.8.6", "linux", "x86_64"),
# },
def _bindist_bundle(version, os, archs = []):
    return dict([
        (arch, _bindist(version, os, arch))
        for arch in archs
    ])

# sample version: (for `_bindist_bundle("1.8.6", bundles = {"osx": ["arm64"], "linux": ["arm64"]})`)
# "1.8.6": {
#     "linux": {
#         "arm64": _bindist("1.8.6", "linux", "arm64"),
#     },
#     "darwin": {
#         "arm64": _bindist("1.8.6", "osx", "arm64"),
#     },
# },
def _bindist_version(version, bundles = {}):
    return dict([
        (os, _bindist_bundle(version, os, archs))
        for os, archs in bundles.items()
    ])


# version checkums (static)
_grpcurl_version_checksums = {
    "1.8.6_linux_x86_64": "5d6768248ea75b30fba09c09ff8ba91fbc0dd1a33361b847cdaf4825b1b514a7",
    "1.8.6_linux_arm64": "8e68cef2b493e79ebf8cb6d867678cbba0b9c5dea75f238575fca4f3bcc539b2",
    "1.8.6_linux_s390x": "45ffd4a01c330176a4f5727667571973c60d0b4d670d4fbba31b3cf86922f889",
    "1.8.6_osx_arm64": "fe3ce63efb168e894f4af58512b1bd9e3327166f1616975a7dbb249a990ce6cf",
    "1.8.6_osx_x86_64": "f908d8d2006efaf702097593a2e030ddc9274c7d349b85bee9d3cfa099018854",
    "1.8.6_linux_x86_32": "7840189ecac1f8c7d102fe947a73299abe87435b246a2f679e285d3160a49104",
}

# version configs (static)
_grpcurl_version_configs = {
    "1.8.6": _bindist_version(
        version = "1.8.6",
        bundles = {
            "linux": ["arm64", "s390x", "x86_32", "x86_64"],
            "osx": ["arm64", "x86_64"],
        },
    ),
}

_grpcurl_latest_version = "1.8.6"

def _get_platform(ctx):
    res = ctx.execute(["uname", "-p"])
    arch = "amd64"
    if res.return_code == 0:
        uname = res.stdout.strip()
        if uname == "arm":
            arch = "arm64"
        elif uname == "aarch64":
            arch = "aarch64"

    if ctx.os.name == "linux":
        return ("linux", arch)
    elif ctx.os.name == "mac os x":
        if arch == "arm64" or arch == "aarch64":
            return ("osx", "arm64")
        return ("osx", "x86_64")
    else:
        fail("Unsupported operating system: " + ctx.os.name)

def _grpcurl_bindist_repository_impl(ctx):
    platform = _get_platform(ctx)
    version = ctx.attr.version

    # resolve dist
    config = _grpcurl_version_configs[version]
    link = config[platform[0]][platform[1]]
    sha = _grpcurl_version_checksums["%s_%s_%s" % (version, platform[0], platform[1])]

    urls = [link]
    ctx.download_and_extract(
        url = urls,
        sha256 = sha,
    )

    ctx.file("BUILD", """exports_files(glob(["**/*"]))""")
    ctx.file("WORKSPACE", "workspace(name = \"{name}\")".format(name = ctx.name))


grpcurl_bindist_repository = repository_rule(
    attrs = {
        "version": attr.string(
            mandatory = True,
            default = _grpcurl_latest_version,
        ),
    },
    implementation = _grpcurl_bindist_repository_impl,
)
