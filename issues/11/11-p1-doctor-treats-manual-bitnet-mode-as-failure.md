# Issue 11 - P1: `axiom doctor` treats unmanaged/manual BitNet mode as a failure

**Status:** Open  
**Severity:** P1  
**Date opened:** 2026-04-08  
**Source:** `Testing Results/Test 1/report.md`  
**Base commit:** `main` @ `9019c10`

---

## 1. Issue

`axiom doctor` reports BitNet as a hard failure when BitNet is enabled but no managed start command is configured:

```text
[FAIL] bitnet: BitNet is enabled but no start command is configured
```

That classification conflicts with the rest of the product:

- `docs/getting-started.md:321-330` says manual BitNet servers are supported.
- `internal/bitnet/service.go:215-220` explicitly returns "manual setup required" rather than treating the state as invalid.

Manual BitNet operation is therefore a supported mode, but `doctor` presents it as a broken configuration.

---

## 2. Recreation

1. Set `bitnet.enabled = true`.
2. Leave `bitnet.command` empty.
3. Do not start a BitNet server yet.
4. Run `axiom doctor`.
5. Observe that `internal/doctor/doctor.go:139-149` returns:

```go
if s.cfg.BitNet.Command == "" {
    return CheckResult{Name: "bitnet", Status: StatusFail, Summary: "BitNet is enabled but no start command is configured"}
}
```

This happens even though `internal/bitnet/service.go:215-220` treats the same condition as a supported manual-setup path.

---

## 3. Root Cause

`doctor.checkBitNet(...)` currently collapses three different states into one failure:

1. BitNet is enabled and running.
2. BitNet is enabled, not running, but can be started by a managed command.
3. BitNet is enabled, not running, and intended to be started manually.

State 3 is not an invalid configuration. It is just an unmanaged operating mode. The doctor logic does not represent that distinction.

---

## 4. User Impact

- New users see a red failure for a supported setup path.
- The default diagnostic output makes BitNet look mandatory or broken even when the user intends manual control.
- This is especially confusing when combined with Issue 10, because a generated project config can accidentally turn BitNet on and then `doctor` immediately reports a hard failure.

---

## 5. Plan To Fix

Change `checkBitNet(...)` to report state more accurately:

1. If `bitnet.enabled = false`, keep `SKIP`.
2. If BitNet is reachable, keep `PASS`.
3. If BitNet is enabled, not running, and `bitnet.command` is configured:
   - keep `WARN` with "configured but not currently running"
4. If BitNet is enabled, not running, and `bitnet.command` is empty:
   - return `WARN` or `SKIP`, not `FAIL`
   - summary should explain that manual setup is supported and tell the user what to do next

Recommended summary text:

```text
BitNet is enabled in manual mode; start the server manually or configure [bitnet].command
```

---

## 6. Files Expected To Change

- `internal/doctor/doctor.go`
- `internal/doctor/doctor_test.go`
- `docs/getting-started.md`
- `docs/operations-diagnostics.md`

---

## 7. Acceptance Criteria

- [ ] Manual BitNet mode is not reported as `FAIL` when `bitnet.command` is unset.
- [ ] The doctor output distinguishes disabled, manual, managed-but-stopped, and running states.
- [ ] The summary message tells the user exactly how to proceed in manual mode.
- [ ] Documentation matches the runtime behavior.
