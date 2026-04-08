# Test 1 Report

**Date:** 2026-04-08  
**Axiom source repo:** `C:\Users\ethan\Projects\axiom_new`  
**Source commit reviewed:** `9019c10`  
**External test project:** `C:\Users\ethan\axiom-test-projects\Test 1`  
**Platform:** Windows / PowerShell  
**Installed binary used for validation:** `C:\Users\ethan\go\bin\axiom.exe`

## Objective

Validate the real first-run experience for a source-built Axiom install on Windows:

- confirm how to get `axiom` onto `PATH`
- initialize or reopen a test project
- run `axiom doctor`
- run `axiom status`
- open `axiom tui`
- identify any repo-level defects that block or confuse setup

## Preconditions

Before the failing commands were investigated, the machine already had:

- Docker Desktop running and reachable from the shell
- a global Axiom config at `C:\Users\ethan\.axiom\config.toml`
- a non-empty global OpenRouter API key in that global config
- `bitnet.enabled = false` in that global config

The source build itself also installed correctly through Go:

- `go install .\cmd\axiom` produced `C:\Users\ethan\go\bin\axiom.exe`
- `C:\Users\ethan\go\bin` was already on the user's `PATH`

## Commands Run And Observed Behavior

The user ran the following inside `C:\Users\ethan\axiom-test-projects\Test 1`:

```powershell
git branch --show-current
axiom init --name "my-project"
git add .axiom
git commit -m "Initialize Axiom"
axiom doctor
axiom status
axiom tui
```

Observed output:

1. `git branch --show-current` returned `main`.
2. `axiom init --name "my-project"` returned `error: axiom project already initialized in C:\Users\ethan\axiom-test-projects\Test 1`.
3. `git commit -m "Initialize Axiom"` returned `nothing to commit, working tree clean`.
4. `axiom doctor` returned:
   - `PASS` for Docker
   - `FAIL` for BitNet: `BitNet is enabled but no start command is configured`
   - `PASS` for network
   - `PASS` for resources
   - `WARN` for cache: `Docker image axiom-meeseeks-multi:latest is not present locally`
   - `PASS` for security
5. `axiom status` failed with:

```text
error: no inference provider available for configured orchestrator runtime: runtime "claw" requires an openrouter API key
```

6. `axiom tui` failed with the same inference-provider error.

## Investigation

The global config was not the problem.

The machine-level file `C:\Users\ethan\.axiom\config.toml` already contained:

- a non-empty `inference.openrouter_api_key`
- `bitnet.enabled = false`

The project-level file `C:\Users\ethan\axiom-test-projects\Test 1\.axiom\config.toml` contained:

- `openrouter_api_key = ""`
- `bitnet.enabled = true`

That combination explains both failures:

1. `internal/project/project.go:54-60` writes a full default config into every initialized project.
2. `internal/config/config.go:312-344` loads global config first and project config second.
3. `internal/config/config.go:348-369` treats any explicitly present project field as authoritative, even when that field is an empty string or an unwanted default.
4. The empty project-level `openrouter_api_key` masked the valid global key.
5. The project-level `bitnet.enabled = true` masked the valid global disable.
6. `internal/app/app.go` then rejected startup for the `claw` runtime because the merged config appeared to have no usable OpenRouter provider.
7. `internal/doctor/doctor.go:139-149` reported BitNet as a `FAIL` because the merged config said BitNet was enabled but no managed start command was configured.

## Change Made During This Test

To prove the root cause, the external test project's `.axiom/config.toml` was edited so it no longer overrode the working global settings:

- removed the empty project-level `openrouter_api_key`
- removed the project-level `bitnet.enabled = true`

No source files inside `C:\Users\ethan\Projects\axiom_new` were changed to make the test project work.

## Retest Results After The External Project Config Fix

After removing those two project-level overrides, the following were verified in `C:\Users\ethan\axiom-test-projects\Test 1`:

- `axiom doctor` changed the BitNet check from `FAIL` to `SKIP`
- `axiom status` succeeded
- `axiom tui --plain` rendered the startup screen successfully

The remaining warning was:

```text
cache: Docker image axiom-meeseeks-multi:latest is not present locally
```

That warning was not resolved during this test because the source checkout does not currently provide an obvious, documented path to build or obtain that image.

## Successes Confirmed

- Building and installing the CLI from source works with `go install .\cmd\axiom`.
- The installed binary is usable from the shell once the Go bin directory is on `PATH`.
- Docker daemon detection works.
- Provider endpoint reachability checks work.
- `axiom status` and `axiom tui` both work once config inheritance is no longer broken by project defaults.

## Failures And Gaps Confirmed

This test confirmed the following repo-level issues:

1. Generated project config can mask a valid global OpenRouter key.
2. Generated project config can force BitNet on even when the user disabled it globally.
3. `axiom doctor` treats unmanaged/manual BitNet mode as a failure instead of a non-fatal state.
4. The default Docker image warning does not have a reproducible recovery path from this source checkout.
5. `axiom run` still hardcodes `main` as the base branch.
6. The documented Windows startup path relies on POSIX-style `make` targets instead of a native PowerShell / `go install` flow.
7. There is no guided first-run setup flow that helps the user configure OpenRouter, Docker image readiness, or BitNet from within Axiom itself.

Each confirmed issue has been broken out into its own tracker entry under `issues/09` through `issues/15`, and summarized in `issues.md`.

## Notes

- The external test project is now dirty until its `.axiom/config.toml` change is committed.
- `axiom init` should not be re-run in an already initialized repository; the "already initialized" response was expected and not itself a bug.
- This report documents the actual test outcome from the user's environment. It does not claim that later task execution, merge validation, or Meeseeks container execution were fully validated end to end.
