# Issue 10 - P1: `axiom init` writes `bitnet.enabled = true` into project config and overrides a user's global disable

**Status:** Open  
**Severity:** P1  
**Date opened:** 2026-04-08  
**Source:** `Testing Results/Test 1/report.md`  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

A user can disable BitNet globally in `~/.axiom/config.toml`, initialize a project, and still have that project behave as if BitNet were enabled.

In Test 1:

- global config had `bitnet.enabled = false`
- project config had `bitnet.enabled = true`
- `axiom doctor` then reported BitNet as enabled and misconfigured

This override was not user intent. It was written automatically by `axiom init`.

---

## 2. Recreation

1. Set `bitnet.enabled = false` in `~/.axiom/config.toml`.
2. Run `axiom init --name "<project>"`.
3. Inspect `.axiom/config.toml`.
4. Observe that `internal/project/project.go:54-60` serialized the default config from `internal/config/config.go:160-169`, which sets `BitNet.Enabled = true`.
5. Load the project through `config.Load(projectRoot)`.
6. Observe that `internal/config/config.go:312-344` layers project over global and `internal/config/config.go:348-369` makes the explicit project boolean authoritative.

At that point the project no longer inherits the user's global BitNet disable.

---

## 3. Root Cause

This is the same template-generation mistake as Issue 09, but with a different field:

- `config.Default(...)` sets `BitNet.Enabled = true`
- `project.Init(...)` writes that value into every new `.axiom/config.toml`
- `config.Load(...)` then treats that project-level boolean as an intentional override

BitNet enablement is environment-specific, not inherently project-specific. Writing it into every project config by default breaks the config layering contract.

---

## 4. User Impact

- A user who intentionally disabled BitNet globally sees project-local behavior that contradicts their global setup.
- `axiom doctor` reports BitNet errors that the user thought they had already opted out of.
- The generated project config creates confusion about whether BitNet is required for cloud-only use.
- This increases first-run friction even when the user only wants OpenRouter-backed execution.

---

## 5. Plan To Fix

Recommended fix:

1. Stop emitting `bitnet.enabled = true` into generated project config by default.
2. Treat BitNet enablement the same way as other machine-local settings: inherit from `~/.axiom/config.toml` unless the user explicitly overrides it at the project level.
3. Keep project-level BitNet overrides available for advanced cases, but do not manufacture them during `axiom init`.
4. Add documentation that explains the intended layering:
   - global config for machine/runtime choices
   - project config for repo-specific behavior only

---

## 6. Files Expected To Change

- `internal/project/project.go`
- `internal/project/project_test.go`
- `internal/config/config_test.go`
- `docs/getting-started.md`

---

## 7. Acceptance Criteria

- [ ] A freshly initialized project's `.axiom/config.toml` does not force `bitnet.enabled = true` unless the user asked for a project-local override.
- [ ] With global `bitnet.enabled = false`, `config.Load(projectRoot)` preserves the global disable for a freshly initialized project.
- [ ] `axiom doctor` does not report BitNet as enabled in a freshly initialized project when the user disabled it globally.
