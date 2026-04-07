# Release Packaging Reference

Phase 20 introduces a release-bundle assembly path for candidate builds.

## Scope

The release bundle is intended to package the minimum operator-facing artifacts required to evaluate a release candidate on a clean machine:

- compiled `axiom` binary
- generated default `config.toml` template
- operator documentation from `docs/`
- Docker asset definitions from `docker/`
- fixture repositories from `testdata/fixtures/`
- a markdown test matrix
- a JSON manifest describing the bundled contents

## Package

**Package:** `internal/release/`

The package exposes a single high-level entry point:

```go
manifest, err := release.BuildBundle(release.BundleOptions{
    SourceRoot: repoRoot,
    OutputRoot: distDir,
    BinaryPath: filepath.Join(repoRoot, "bin", "axiom.exe"),
    Version:    "v1.0.0-rc1",
    GOOS:       runtime.GOOS,
    GOARCH:     runtime.GOARCH,
    TestMatrix: []release.TestSuite{
        {Name: "unit", Command: "go test ./...", Status: "passed"},
    },
})
```

There is currently no dedicated `axiom release` CLI command. Bundle assembly is a package/build-tool step for release engineering and test-matrix generation.

## Output Layout

Given version `v1.0.0-rc1`, GOOS `windows`, and GOARCH `amd64`, the bundle directory is:

```text
dist/axiom-v1.0.0-rc1-windows-amd64/
  bin/
    axiom.exe
  config/
    axiom.default.toml
  docs/
  docker/
  fixtures/
  test-matrix.md
  release-manifest.json
```

## Manifest

`release-manifest.json` records:

- release version
- target platform (`<goos>/<goarch>`)
- bundle directory
- relative paths to the binary, default config, and test matrix
- copied docs
- copied Docker assets
- copied fixture repositories
- the test matrix entries embedded in the bundle

## Fixture Repositories

Fixture repositories live in `testdata/fixtures/` and are used by phase-20 integration tests:

- `greenfield/` - a clean repository before `axiom init`
- `existing-go/` - a small pre-existing Go project that Axiom can adopt

`internal/testfixtures/` copies these fixtures into temporary directories, initializes git, configures a local test identity, and creates an initial commit on `main`.

## Current Limitation

The bundle builder packages docs, fixture repos, and Docker asset definitions, but it does not itself build or publish Docker images. Release bundles should still be treated as packaging validation rather than a final product sign-off mechanism, because image publication, external runtime provisioning, and release-process verification remain separate concerns.
