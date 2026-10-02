# Releasing

Add the release to `CHANGELOG.md` first; it has to be in the tagged tree.

```sh
git tag -a vX.Y.Z -m vX.Y.Z
git push origin vX.Y.Z
```

Pushing the tag runs `.github/workflows/release.yml`, which builds the binaries with GoReleaser, publishes them to GitHub Releases, and attests their build provenance. Releases are immutable, so fix a bad release with a new patch version. `go install ...@latest` picks up the tag through the Go module proxy.

`goreleaser release --snapshot --clean` builds the same archives into `dist/` locally without publishing.
