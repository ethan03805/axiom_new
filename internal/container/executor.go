package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CLIExecutor runs docker CLI commands against the local host.
type CLIExecutor struct{}

// Run executes `docker <args...>` and returns trimmed combined output.
func (CLIExecutor) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// RunWithExit behaves like Run but returns stdout, stderr, and the raw exit
// code separately. A non-zero exit code is returned without an error so that
// callers (DockerCheckRunner, for one) can distinguish a clean "test failed"
// result from an infrastructure failure.
func (CLIExecutor) RunWithExit(ctx context.Context, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return outBuf.String(), errBuf.String(), ee.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}
