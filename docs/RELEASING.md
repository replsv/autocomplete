# Releasing

## Cut a release

```sh
git tag release/v0.1.0 && git push origin release/v0.1.0
```

The `release/v*` tag is only the trigger. The version consumers see, and that `go get` resolves, is the module
tag `v0.1.0` that the workflow creates on the same commit.

## What the workflow does

[.github/workflows/release.yml](../.github/workflows/release.yml), on a push of `release/vX.Y.Z`:

1. Strips `release/` and checks that the rest is strict semver: `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`.
2. Fails if the tag `vX.Y.Z` already exists.
3. Runs `go vet` and `go test -race ./...` at the tagged commit.
4. Creates the annotated tag `vX.Y.Z` on that commit as `github-actions[bot]` and pushes it.
5. Creates the GitHub release `vX.Y.Z` with generated notes (`gh release create --verify-tag --generate-notes`).
6. Asks `proxy.golang.org` for the version so pkg.go.dev picks it up. A failure here does not fail the release.

Runs are serialized per tag.

## Prereleases

A version with a suffix is published as a GitHub prerelease, and Go treats it as one too: `go get @latest` skips
it.

```sh
git tag release/v0.2.0-rc.1 && git push origin release/v0.2.0-rc.1
```

## Failures and retries

The workflow fails, and creates nothing, when:

- the suffix is not strict semver (`release/v1.2`, `release/1.2.3`, `release/v1.2.3+build`);
- `vX.Y.Z` already exists (Go module versions are immutable, so pick the next version instead);
- the tests fail.

To retry after fixing the cause, move the trigger tag:

```sh
git tag -d release/v0.1.0
git push origin :refs/tags/release/v0.1.0
git tag release/v0.1.0 <commit> && git push origin release/v0.1.0
```

## Repository settings

The workflow pushes a tag and creates a release with `GITHUB_TOKEN`, so the repository needs Actions enabled
and workflow permissions set to **Read and write** (Settings → Actions → General → Workflow permissions).
