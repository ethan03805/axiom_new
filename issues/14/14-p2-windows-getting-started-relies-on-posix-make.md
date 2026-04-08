# Issue 14 - P2: Windows getting-started flow relies on POSIX `make` targets

**Status:** Open  
**Severity:** P2  
**Date opened:** 2026-04-08  
**Source:** startup review and Test 1 setup validation  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

The documented startup path is not well aligned with a normal Windows PowerShell environment.

Evidence:

- `docs/getting-started.md:22-23` tells the user to run `make build` and `make install`
- `Makefile:3` uses `date`
- `Makefile:25` uses `command -v`
- `Makefile:33` uses `rm -rf`

Those commands assume a POSIX shell environment. A normal Windows source user may not have GNU Make or compatible Unix utilities installed.

---

## 2. Recreation

1. Follow `docs/getting-started.md` from a Windows PowerShell shell.
2. Attempt to use the documented `make build` / `make install` path.
3. Compare that with the Makefile implementation.
4. Observe that the real working Windows path is `go install .\cmd\axiom`, not the documented `make` flow.

---

## 3. Root Cause

The docs present `make` as the primary source-build interface, but the checked-in Makefile is written for a Unix-like shell rather than PowerShell.

This is a documentation and tooling mismatch, not a compiler bug.

---

## 4. User Impact

- Windows users hit avoidable startup friction before even reaching Axiom runtime behavior.
- The docs do not clearly describe the working PowerShell-native path to:
  - build/install the binary
  - locate the Go bin directory
  - ensure `axiom` is on `PATH`
- This increases the chance that users rely on stale checked-in binaries instead of the current source build.

---

## 5. Plan To Fix

Recommended fix:

1. Update `docs/getting-started.md` with a first-class Windows section that uses:
   - `go install .\cmd\axiom`
   - `Get-Command axiom`
   - `C:\Users\<user>\go\bin` PATH guidance
2. Keep `make` as an optional Unix-like workflow rather than the default cross-platform instruction.
3. Optionally add a PowerShell helper script if the repo wants a one-command build/install flow on Windows.

---

## 6. Files Expected To Change

- `docs/getting-started.md`
- optionally `scripts/` with a PowerShell helper
- optionally `README` or contributor docs if they also point at `make`

---

## 7. Acceptance Criteria

- [ ] Windows users can follow one documented PowerShell-native path from source checkout to a working `axiom` command.
- [ ] The docs explain where the binary lands after `go install`.
- [ ] The docs explain how to put the Go bin directory on `PATH`.
- [ ] The docs no longer imply that POSIX `make` is the only normal setup path.
