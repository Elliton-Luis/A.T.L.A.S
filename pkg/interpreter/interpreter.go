package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Sandbox  string
}

// FormatTelegramHUD returns a clean Markdown formatted HUD of the code execution.
func (r *Result) FormatTelegramHUD() string {
	icon := "🟢"
	status := "Sucesso"
	if !r.Success {
		icon = "🔴"
		status = "Falha"
	}

	mode := ""
	if r.Sandbox != "" {
		mode = fmt.Sprintf(" `[%s]`", r.Sandbox)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s *Execução %s*%s `[%s]` *(%dms)*\n\n", icon, strings.ToUpper(r.Language), mode, status, r.Duration.Milliseconds()))

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

// Runner provides script execution for Python and Bash, supporting both local binaries and cloud sandboxes.
type Runner struct {
	sonarClient *sonar.Client
	httpClient  *http.Client
}

// NewRunner creates a new code interpreter runner.
func NewRunner(sonarClient *sonar.Client) *Runner {
	return &Runner{
		sonarClient: sonarClient,
		httpClient:  &http.Client{Timeout: 12 * time.Second},
	}
}

// RunPython runs Python code via local python binary, Sonar Bridge or Wandbox Cloud Sandbox.
func (r *Runner) RunPython(ctx context.Context, code string) (*Result, error) {
	startTime := time.Now()
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		return nil, fmt.Errorf("código python vazio")
	}

	// 1. Try Sonar Bridge if configured and responsive
	if r.sonarClient != nil {
		res, err := r.sonarClient.Execute(ctx, "python3 -c "+escapeShellArg(cleanCode), "")
		if err == nil {
			return &Result{
				Language: "python",
				Code:     cleanCode,
				Output:   res.Output,
				Error:    res.Error,
				ExitCode: res.ExitCode,
				Duration: time.Since(startTime),
				Success:  res.OK,
				Sandbox:  "Sonar Bridge",
			}, nil
		}
	}

	// 2. Check if local Python is available on host (python3 or python)
	localPython := ""
	if p, err := exec.LookPath("python3"); err == nil && p != "" {
		localPython = "python3"
	} else if p, err := exec.LookPath("python"); err == nil && p != "" {
		localPython = "python"
	}

	if localPython != "" {
		tmpFile, err := os.CreateTemp("", "atlas_run_*.py")
		if err == nil {
			defer os.Remove(tmpFile.Name())
			if _, err := tmpFile.WriteString(cleanCode); err == nil {
				_ = tmpFile.Close()

				cmd := exec.CommandContext(ctx, localPython, tmpFile.Name())
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				runErr := cmd.Run()
				duration := time.Since(startTime)

				exitCode := 0
				success := true
				if runErr != nil {
					success = false
					if exitErr, ok := runErr.(*exec.ExitError); ok {
						exitCode = exitErr.ExitCode()
					} else {
						exitCode = 1
						if stderr.Len() == 0 {
							stderr.WriteString(runErr.Error())
						}
					}
				}

				return &Result{
					Language: "python",
					Code:     cleanCode,
					Output:   stdout.String(),
					Error:    stderr.String(),
					ExitCode: exitCode,
					Duration: duration,
					Success:  success,
					Sandbox:  "Host Local",
				}, nil
			}
			_ = tmpFile.Close()
		}
	}

	// 3. Cloud Sandbox Fallback (Wandbox API - works 100% in serverless environments like Vercel)
	cloudRes, err := r.runWandboxPython(ctx, cleanCode)
	if err == nil {
		cloudRes.Duration = time.Since(startTime)
		return cloudRes, nil
	}

	return nil, fmt.Errorf("nenhum ambiente Python disponível (local sem python3 e sandbox em nuvem falhou: %w)", err)
}

// Wandbox API request and response structures
type wandboxReq struct {
	Compiler string `json:"compiler"`
	Code     string `json:"code"`
}

type wandboxResp struct {
	Status          string `json:"status"`
	ProgramOutput   string `json:"program_output"`
	ProgramError    string `json:"program_error"`
	CompilerOutput  string `json:"compiler_output"`
	CompilerError   string `json:"compiler_error"`
	ProgramMessage  string `json:"program_message"`
	CompilerMessage string `json:"compiler_message"`
}

func (r *Runner) runWandboxPython(ctx context.Context, code string) (*Result, error) {
	reqBody, err := json.Marshal(wandboxReq{
		Compiler: "cpython-3.12.7",
		Code:     code,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://wandbox.org/api/compile.json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requisição sandbox wandbox: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("wandbox status %d: %s", resp.StatusCode, string(body))
	}

	var wResp wandboxResp
	if err := json.NewDecoder(resp.Body).Decode(&wResp); err != nil {
		return nil, fmt.Errorf("decodificar resposta wandbox: %w", err)
	}

	exitCode := 0
	if wResp.Status != "0" {
		exitCode = 1
	}

	errText := strings.TrimSpace(wResp.ProgramError)
	if errText == "" {
		errText = strings.TrimSpace(wResp.CompilerError)
	}
	if errText == "" && exitCode != 0 {
		errText = strings.TrimSpace(wResp.ProgramMessage)
	}

	return &Result{
		Language: "python",
		Code:     code,
		Output:   wResp.ProgramOutput,
		Error:    errText,
		ExitCode: exitCode,
		Success:  exitCode == 0,
		Sandbox:  "Nuvem Sandbox",
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
				Sandbox:  "Sonar Bridge",
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
		Sandbox:  "Host Local",
	}, nil
}

func escapeShellArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
