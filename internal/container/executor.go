package container

import (
	"context"
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
