package sonar

import (
	"context"
	"testing"
)

func TestLocalCLIExecution(t *testing.T) {
	client := NewClient("", "Li3cht3nst3in2", "/home/chocotonilitz/Programacao/projetos/sonar/sonar_bridge.php", "/home/chocotonilitz/Programacao/projetos")

	res, err := client.Execute(context.Background(), "echo 'ATLAS_SONAR_PONG'", "")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !res.OK {
		t.Errorf("expected OK true, got false")
	}

	if res.Output != "ATLAS_SONAR_PONG" {
		t.Errorf("expected output ATLAS_SONAR_PONG, got: %s", res.Output)
	}
}
