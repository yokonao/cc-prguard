# Install

cc-prguard is a single binary for linux and macOS (amd64/arm64). Install it into a directory in your `PATH` in one of these ways:

1. [GitHub Releases](#github-releases)
1. [mise](#mise)
1. [go install](#go-install)

## GitHub Releases

Download the archive for your platform from [GitHub Releases](https://github.com/yokonao/cc-prguard/releases), then install the binary into `PATH`:

```sh
asset=cc-prguard_darwin_arm64.tar.gz # cc-prguard_{linux,darwin}_{amd64,arm64}.tar.gz
gh release download -R yokonao/cc-prguard -p "$asset" # the latest release; pass a tag for another
tar -xzf "$asset" cc-prguard
mkdir -p ~/.local/bin
install -m 755 cc-prguard ~/.local/bin/
```

The macOS binaries are not notarized. `gh` and `curl` do not mark downloads as quarantined, but a browser does, and macOS then refuses to run the binary; remove the mark with `xattr -d com.apple.quarantine cc-prguard`.

### Verify the archive

Each archive has a [build provenance attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) from the release workflow. Verify it with the [GitHub CLI](https://cli.github.com/):

```sh
gh attestation verify "$asset" \
  -R yokonao/cc-prguard \
  --signer-workflow yokonao/cc-prguard/.github/workflows/release.yml
```

## mise

With [mise](https://mise.jdx.dev/):

```sh
mise use -g github:yokonao/cc-prguard
```

mise verifies the [build provenance attestation](#verify-the-archive) automatically.

## go install

With Go 1.27.1 or later:

```sh
go install github.com/yokonao/cc-prguard/cmd/cc-prguard@latest
```

The binary lands in `$(go env GOBIN)`, or `~/go/bin` when that is unset.
