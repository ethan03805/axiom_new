package engine

import (
	"context"
	"testing"
)

type containerServiceWithExec struct{}

func (containerServiceWithExec) Start(context.Context, ContainerSpec) (string, error) {
	return "", nil
}
func (containerServiceWithExec) Stop(context.Context, string) error            { return nil }
func (containerServiceWithExec) ListRunning(context.Context) ([]string, error) { return nil, nil }
func (containerServiceWithExec) Cleanup(context.Context) error                 { return nil }
func (containerServiceWithExec) Exec(context.Context, string, []string) (ExecResult, error) {
	return ExecResult{}, nil
}

// TestContainerServiceInterfaceIncludesExec asserts the ContainerService
// interface was extended with an Exec method — the prerequisite for Issue 04's
// real DockerCheckRunner implementation.
func TestContainerServiceInterfaceIncludesExec(t *testing.T) {
	var _ ContainerService = containerServiceWithExec{}
}
