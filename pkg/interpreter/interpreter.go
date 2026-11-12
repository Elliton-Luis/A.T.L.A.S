package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"atlas/pkg/sonar"
)

// Result holds the formatted outcome of a code execution.
type Result struct {
	Language string
	Code     string
	Output   string
	Error    string
	ExitCode int
	Duration time.Duration
	Success  bool
}

// FormatTelegramHUD returns a clean Markdown formatted HUD of the code execution.
func (r *Result) FormatTelegramHUD() string {
	icon := "🟢"
	status := "Sucesso"
	if !r.Success {
		icon = "🔴"
		status = "Falha"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s *Execução %s* `[%s]` *(%dms)*\n\n", icon, strings.ToUpper(r.Language), status, r.Duration.Milliseconds()))

	if r.Output != "" {
		sb.WriteString("📤 *Saída (stdout):*\n```\n")
		sb.WriteString(strings.TrimSpace(r.Output))
		sb.WriteString("\n```\n")
	}

	if r.Error != "" {
		sb.WriteString("⚠️ *Erros (stderr):*\n```\n")
		sb.WriteString(strings.TrimSpace(r.Error))
		sb.WriteString("\n```\n")
	}

	if r.Output == "" && r.Error == "" {
		sb.WriteString("_(Script executado com código de retorno 0 e nenhuma saída gerada)_\n")
	}

	return strings.TrimSpace(sb.String())
}

// Runner provides safe script execution for Python and Bash.
type Runner struct {
	sonarClient *sonar.Client
}

// NewRunner creates a new code interpreter runner.
func NewRunner(sonarClient *sonar.Client) *Runner {
	return &Runner{
		sonarClient: sonarClient,
	}
}

// RunPython writes code to a temporary file and executes it via python3.
func (r *Runner) RunPython(ctx context.Context, code string) (*Result, error) {
	startTime := time.Now()

	// If Sonar bridge is configured and online, use it
	if r.sonarClient != nil {
		// Escape or write via python -c
		res, err := r.sonarClient.Execute(ctx, "python3 -c "+escapeShellArg(code), "")
		if err == nil {
			return &Result{
				Language: "python",
				Code:     code,
				Output:   res.Output,
				Error:    res.Error,
				ExitCode: res.ExitCode,
				Duration: time.Since(startTime),
				Success:  res.OK,
			}, nil
		}
	}

	// Local fallback execution
	tmpFile, err := os.CreateTemp("", "atlas_run_*.py")
	if err != nil {
		return nil, fmt.Errorf("create temp python file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(code); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write python code: %w", err)
	}
	tmpFile.Close()

	cmd := exec.CommandContext(ctx, "python3", tmpFile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	success := true
	if err != nil {
		success = false
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return &Result{
		Language: "python",
		Code:     code,
		Output:   stdout.String(),
		Error:    stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
		Success:  success,
	}, nil
}

// RunBash executes a bash snippet.
func (r *Runner) RunBash(ctx context.Context, script string) (*Result, error) {
	startTime := time.Now()

	if r.sonarClient != nil {
		res, err := r.sonarClient.Execute(ctx, script, "")
		if err == nil {
			return &Result{
				Language: "bash",
				Code:     script,
				Output:   res.Output,
				Error:    res.Error,
				ExitCode: res.ExitCode,
				Duration: time.Since(startTime),
				Success:  res.OK,
			}, nil
		}
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	success := true
	if err != nil {
		success = false
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return &Result{
		Language: "bash",
		Code:     script,
		Output:   stdout.String(),
		Error:    stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
		Success:  success,
	}, nil
}

func escapeShellArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
