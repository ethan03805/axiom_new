# Issue 12 - P1: Source checkout has no reproducible path to prepare the default Docker image

**Status:** Open  
**Severity:** P1  
**Date opened:** 2026-04-08  
**Source:** `Testing Results/Test 1/report.md`  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

The default runtime configuration expects the Docker image `axiom-meeseeks-multi:latest`, but the source checkout does not currently provide a clear, reproducible way to build or obtain it.

Evidence from the source tree:

- `internal/config/config.go:172` defaults `docker.image` to `axiom-meeseeks-multi:latest`
- `internal/doctor/doctor.go:191-199` warns when that image is not present locally
- `docs/development.md` describes a `docker/` directory in the repo structure
- `docs/release-packaging.md` says release bundles should include `docker/`
- the actual repo currently has no `docker/` directory

The result is a real setup gap between "image missing" and "image ready".

---

## 2. Recreation

1. Use a normal source checkout of this repo.
2. Run `axiom doctor` in an initialized project.
3. Observe the warning:

```text
[WARN] cache: Docker image axiom-meeseeks-multi:latest is not present locally
```

4. Attempt to find the documented Docker assets in the repo.
5. Observe that `docker/` does not exist in the current checkout even though the docs and release packaging reference it.

---

## 3. Root Cause

The repo is in a split state:

- runtime code still expects a specific local image name
- diagnostics still check for that image
- documentation and release packaging still refer to Docker assets that are not present in the checkout

That means the codebase can detect the missing image but cannot reliably tell a source-build operator how to resolve it.

---

## 4. User Impact

- Users can reach a partially healthy state (`status` / `tui` working) but still not know how to prepare the execution image.
- The warning is actionable only in theory; the repo does not expose the next step.
- This likely blocks real task execution and validation on clean machines.

Because it threatens the actual execution path, this is a P1 issue.

---

## 5. Plan To Fix

One of these paths needs to become true and documented:

1. Restore `docker/` with the Dockerfiles and build scripts needed to produce `axiom-meeseeks-multi:latest`.
2. Or, if the image is meant to be pulled from a registry, document the exact pull/tag process and stop implying that the source repo itself contains the Docker asset definitions.
3. Or, if the default image name changed, update:
   - `internal/config/config.go`
   - `internal/doctor/doctor.go`
   - `docs/development.md`
   - `docs/release-packaging.md`

The repo should also have one explicit operator path such as:

- `docker build -t axiom-meeseeks-multi:latest ...`
- or `docker pull <published-image>`
- or `axiom setup docker`

---

## 6. Files Expected To Change

- `internal/config/config.go`
- `docs/development.md`
- `docs/release-packaging.md`
- `docs/getting-started.md`
- optionally a restored `docker/` tree or new setup command documentation

---

## 7. Acceptance Criteria

- [ ] A clean source checkout exposes one documented way to prepare the default Docker image.
- [ ] `docs/development.md` and `docs/release-packaging.md` match the actual repo contents.
- [ ] `axiom doctor` warning text points to a real next step.
- [ ] A contributor following the documented steps can make the warning disappear on a clean machine.
