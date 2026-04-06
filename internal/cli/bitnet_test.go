package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestBitnetStatusAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := bitnetStatusAction(application, buf)
	if err != nil {
		t.Fatalf("bitnetStatusAction: %v", err)
	}

	output := buf.String()
	// Should report status regardless of whether BitNet is running
	if !strings.Contains(output, "BitNet") {
		t.Errorf("expected output to contain 'BitNet', got: %s", output)
	}
}

func TestBitnetStatusAction_ShowsEndpoint(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	if err := bitnetStatusAction(application, buf); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	// Should show the configured endpoint
	if !strings.Contains(output, "localhost") {
		t.Errorf("expected output to contain endpoint, got: %s", output)
	}
}

func TestBitnetStartAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := bitnetStartAction(application, buf)
	// BitNet start currently returns a manual-mode message
	// It should not crash, but may return an informational message
	if err != nil {
		// Some errors are expected (e.g., manual mode)
		output := buf.String()
		if !strings.Contains(err.Error(), "manual") && !strings.Contains(output, "manual") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestBitnetStopAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := bitnetStopAction(application, buf)
	// BitNet stop may return errors when not running — that's expected
	if err != nil {
		errMsg := err.Error()
		if !strings.Contains(errMsg, "manual") &&
			!strings.Contains(errMsg, "not running") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestBitnetModelsAction(t *testing.T) {
	application := testApp(t)
	buf := new(bytes.Buffer)

	err := bitnetModelsAction(application, buf)
	// BitNet may not be running, so this might error
	// We just verify it doesn't panic and produces reasonable output
	if err != nil {
		// Expected when BitNet isn't running
		errMsg := err.Error()
		if !strings.Contains(errMsg, "not running") &&
			!strings.Contains(errMsg, "connection refused") &&
			!strings.Contains(errMsg, "connectex") &&
			!strings.Contains(errMsg, "disabled") &&
			!strings.Contains(errMsg, "request failed") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestBitnetStatusAction_DisabledConfig(t *testing.T) {
	application := testApp(t)
	application.Config.BitNet.Enabled = false
	buf := new(bytes.Buffer)

	err := bitnetStatusAction(application, buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "disabled") && !strings.Contains(output, "not running") {
		t.Errorf("expected disabled/not-running message, got: %s", output)
	}
}
