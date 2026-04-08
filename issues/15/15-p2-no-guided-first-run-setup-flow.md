# Issue 15 - P2: There is no guided first-run setup flow for OpenRouter, Docker image readiness, or BitNet

**Status:** Open  
**Severity:** P2  
**Date opened:** 2026-04-08  
**Source:** Test 1 report and source inspection  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

Axiom currently depends on several environment prerequisites before the normal operator surfaces become useful:

- global OpenRouter credential setup
- Docker daemon availability
- Docker image readiness
- optional BitNet configuration and operating mode

But there is no guided first-run flow inside the product that helps the user satisfy those prerequisites.

What exists today:

- `axiom doctor` reports system state
- `docs/getting-started.md` explains manual setup steps
- `internal/session/manager.go:190-267` and `internal/tui/model.go` implement the normal project/session startup surface after the app opens successfully

What does not exist:

- a setup wizard
- a first-run config writer for `~/.axiom/config.toml`
- a guided Docker-image preparation step
- a BitNet mode chooser
- a recovery surface inside TUI when startup fails before the TUI can launch

---

## 2. Recreation

1. Start from a clean machine or partially configured machine.
2. Run `axiom status` or `axiom tui` before prerequisites are satisfied.
3. Observe that the command fails with a direct error such as:

```text
no inference provider available for configured orchestrator runtime: runtime "claw" requires an openrouter API key
```

4. Observe that the TUI itself cannot help because it is not reachable until startup succeeds.
5. Inspect the session/TUI code and note that the startup frame is aimed at run operation, not environment bootstrapping:
   - `internal/session/manager.go:233` -> `Describe what you want to build.`
   - `internal/session/manager.go:235` -> `Review the SRS and approve or reject it.`

The current product therefore assumes the user has already completed manual setup outside the tool.

---

## 3. Root Cause

The runtime has diagnostics and operator surfaces, but it does not have an environment-onboarding layer.

That leaves a gap between:

- source install
- prerequisite discovery
- actionable recovery

The user can learn what is wrong, but not fix it from inside Axiom.

---

## 4. User Impact

- First-run setup depends on careful doc reading and direct file editing.
- Errors that occur before `axiom tui` launches cannot be recovered from within the product.
- Missing image, missing key, and BitNet-mode questions all become manual side quests instead of a guided setup flow.

This is not the same as Issue 14. Issue 14 is specifically about Windows source-install documentation. This issue is about the product not containing a setup experience at all.

---

## 5. Plan To Fix

Possible implementation paths:

1. Add an `axiom setup` command that:
   - checks for `~/.axiom/config.toml`
   - prompts for OpenRouter key if required
   - detects Docker daemon status
   - checks or prepares the default Docker image
   - explains BitNet manual vs managed mode and writes config accordingly
2. Add a pre-TUI bootstrap path for setup and recovery when startup prerequisites are missing.
3. Make `axiom doctor` optionally emit machine-actionable remediation commands, not just health summaries.

The most pragmatic first step is probably `axiom setup`, because it can run before the full app composition is available.

---

## 6. Files Expected To Change

- `internal/cli/`
- `internal/config/`
- `internal/doctor/`
- optionally `internal/tui/` if setup becomes an interactive surface
- `docs/getting-started.md`
- `docs/session-tui.md`

---

## 7. Acceptance Criteria

- [ ] A user can complete first-run prerequisite setup without manually editing config files.
- [ ] Missing OpenRouter configuration produces a guided remediation path, not just a terminal error.
- [ ] Docker image readiness has an explicit setup path.
- [ ] BitNet setup distinguishes manual and managed modes in a guided flow.
- [ ] The setup flow is documented and reachable before normal run/session operation.
