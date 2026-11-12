package sonar

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
)

type ExecResult struct {
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Workspace string `json:"workspace"`
	Mode      string `json:"mode"`
	Error     string `json:"error,omitempty"`
}

type Client struct {
	BridgeURL    string // e.g. "http://127.0.0.1:8765" ou URL pública de túnel
	BridgeToken  string
	LocalCLIPath string // e.g. "/home/chocotonilitz/Programacao/projetos/sonar/sonar_bridge.php"
	ProjectsDir  string // e.g. "/home/chocotonilitz/Programacao/projetos"
	httpClient   *http.Client
}

func NewClient(bridgeURL, bridgeToken, localCLIPath, projectsDir string) *Client {
	if bridgeToken == "" {
		bridgeToken = "Li3cht3nst3in2"
	}
	if localCLIPath == "" {
		localCLIPath = "/home/chocotonilitz/Programacao/projetos/sonar/sonar_bridge.php"
	}
	if projectsDir == "" {
		projectsDir = "/home/chocotonilitz/Programacao/projetos"
	}
	return &Client{
		BridgeURL:    strings.TrimSuffix(strings.TrimSpace(bridgeURL), "/"),
		BridgeToken:  bridgeToken,
		LocalCLIPath: localCLIPath,
		ProjectsDir:  projectsDir,
		httpClient: &http.Client{
			Timeout: 40 * time.Second,
		},
	}
}

// Execute executa um comando através do Sonar (tentando HTTP primeiro se configurado, ou CLI local).
func (c *Client) Execute(ctx context.Context, cmd, dir string) (*ExecResult, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, fmt.Errorf("comando vazio")
	}

	// 1. Tentar via HTTP se BridgeURL estiver configurada
	if c.BridgeURL != "" {
		res, err := c.executeHTTP(ctx, cmd, dir)
		if err == nil {
			return res, nil
		}
	}

	// 2. Tentar via CLI local se o arquivo sonar_bridge.php existir na máquina
	if c.LocalCLIPath != "" {
		if _, err := os.Stat(c.LocalCLIPath); err == nil {
			return c.executeLocalCLI(ctx, cmd, dir)
		}
	}

	return nil, fmt.Errorf("Sonar Bridge indisponível (nem URL HTTP nem CLI local encontrados)")
}

func (c *Client) executeHTTP(ctx context.Context, cmd, dir string) (*ExecResult, error) {
	url := fmt.Sprintf("%s/api/execute", c.BridgeURL)

	payload := map[string]string{
		"command": cmd,
	}
	if dir != "" {
		payload["dir"] = dir
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.BridgeToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BridgeToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha na conexão HTTP com Sonar: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var res ExecResult
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("resposta inválida do Sonar: %s", string(respBytes))
	}
	return &res, nil
}

func (c *Client) executeLocalCLI(ctx context.Context, cmd, dir string) (*ExecResult, error) {
	args := []string{c.LocalCLIPath, "--cmd", cmd}
	if dir != "" {
		args = append(args, "--dir", dir)
	}

	cmdExec := exec.CommandContext(ctx, "php", args...)
	outputBytes, err := cmdExec.CombinedOutput()
	if err != nil && len(outputBytes) == 0 {
		return nil, fmt.Errorf("erro ao invocar php sonar_bridge: %w", err)
	}

	var res ExecResult
	if jsonErr := json.Unmarshal(outputBytes, &res); jsonErr == nil {
		return &res, nil
	}

	// Se não for JSON, encapsula a saída direta
	return &ExecResult{
		OK:       err == nil,
		ExitCode: 0,
		Output:   string(outputBytes),
		Mode:     "cli_fallback",
	}, nil
}
