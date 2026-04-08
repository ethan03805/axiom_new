# Issue 13 - P1: `axiom run` hardcodes `main` as the base branch

**Status:** Open  
**Severity:** P1  
**Date opened:** 2026-04-08  
**Source:** startup review and source inspection  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

`axiom run` currently assumes that every repository's base branch is `main`.

The relevant code is:

- `internal/cli/run.go:51` sets `BaseBranch: "main"`
- `internal/gitops/gitops.go:288-299` switches to the base branch before creating the work branch

If the repository uses `master`, `develop`, or any other default branch name, run creation will fail when the git layer tries to check out `main`.

---

## 2. Recreation

1. Create or use a repository whose default branch is not `main`.
2. Initialize Axiom in that repository.
3. Run:

```powershell
axiom run "Build a REST API"
```

4. Observe that the run path ultimately attempts to switch to `main` because the CLI hardcoded that branch name before calling the git layer.

---

## 3. Root Cause

Branch selection is not derived from the actual repository state.

Instead:

- the CLI chooses a constant base branch value
- the git service trusts that value and tries to use it

There is no detection of:

- current checked-out branch
- repo default branch
- project-level branch override
- CLI flag override

---

## 4. User Impact

- Any repository not using `main` is incompatible with the default `axiom run` path.
- The failure occurs during run startup, before the user gets any value out of the system.
- This is common enough to matter: many real repos still use `master`, release branches, or trunk names that are not `main`.

---

## 5. Plan To Fix

Recommended order of resolution:

1. Detect the current branch or repo default branch when starting a run.
2. Use that discovered branch as the default `BaseBranch`.
3. Add an explicit override flag for advanced cases, for example `axiom run --base-branch develop "<prompt>"`.
4. Persist the chosen base branch in run state so all later git operations use the same branch consistently.

---

## 6. Files Expected To Change

- `internal/cli/run.go`
- `internal/gitops/gitops.go`
- `internal/gitops/gitops_test.go`
- `internal/cli/run_test.go`
- `docs/getting-started.md`
- `docs/git-operations.md`

---

## 7. Acceptance Criteria

- [ ] `axiom run` succeeds in repositories whose base branch is not `main`.
- [ ] The chosen base branch is discovered from real repo state or an explicit user override, not a constant.
- [ ] Documentation explains how base-branch selection works.
