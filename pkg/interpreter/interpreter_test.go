package interpreter

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInterpreterPython(t *testing.T) {
	runner := NewRunner(nil) // Local fallback test

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code := `
import math
print(f"PI={math.pi:.4f}")
`
	res, err := runner.RunPython(ctx, code)
	if err != nil {
		t.Fatalf("RunPython failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	if !strings.Contains(res.Output, "PI=3.1416") {
		t.Errorf("expected PI=3.1416 in output, got: %s", res.Output)
	}

	hud := res.FormatTelegramHUD()
	if !strings.Contains(hud, "PYTHON") || !strings.Contains(hud, "Sucesso") {
		t.Errorf("unexpected HUD output: %s", hud)
	}
}

func TestInterpreterWandboxCloudSandbox(t *testing.T) {
	runner := NewRunner(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	code := `print("ATLAS WANDBOX PYTHON RUNNING")`
	res, err := runner.runWandboxPython(ctx, code)
	if err != nil {
		t.Logf("Wandbox network skip or error: %v", err)
		return
	}

	if !res.Success || !strings.Contains(res.Output, "ATLAS WANDBOX PYTHON RUNNING") {
		t.Errorf("unexpected wandbox output: %s (err: %s)", res.Output, res.Error)
	}
}

func TestInterpreterBash(t *testing.T) {
	runner := NewRunner(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `echo "ATLAS_INTERPRETER_ONLINE"`
	res, err := runner.RunBash(ctx, script)
	if err != nil {
		t.Fatalf("RunBash failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}

	if !strings.Contains(res.Output, "ATLAS_INTERPRETER_ONLINE") {
		t.Errorf("expected ATLAS_INTERPRETER_ONLINE in output, got: %s", res.Output)
	}
}
