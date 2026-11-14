package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type LiturgiaResponse struct {
	Data            string `json:"data"`
	Liturgia        string `json:"liturgia"`
	Cor             string `json:"cor"`
	PrimeiraLeitura struct {
		Referencia string `json:"referencia"`
		Titulo     string `json:"titulo"`
		Texto      string `json:"texto"`
	} `json:"primeiraLeitura"`
	SegundaLeitura interface{} `json:"segundaLeitura"`
	Salmo          struct {
		Referencia string `json:"referencia"`
		Refrao     string `json:"refrao"`
		Texto      string `json:"texto"`
	} `json:"salmo"`
	Evangelho struct {
		Referencia string `json:"referencia"`
		Titulo     string `json:"titulo"`
		Texto      string `json:"texto"`
	} `json:"evangelho"`
}

// FetchLiturgiaDiaria queries the public Catholic liturgy API and formats a clean text summary.
func FetchLiturgiaDiaria(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://liturgia.up.railway.app/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "AtlasBot/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao conectar na liturgia: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("liturgia api status %d", resp.StatusCode)
	}

	var data LiturgiaResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("decode liturgia: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📖 *Liturgia Diária — %s*\n", data.Data))
	if data.Liturgia != "" {
		sb.WriteString(fmt.Sprintf("✝️ *%s* (Cor: %s)\n\n", data.Liturgia, data.Cor))
	}

	// 1ª Leitura
	if data.PrimeiraLeitura.Referencia != "" {
		sb.WriteString(fmt.Sprintf("📜 *1ª Leitura (%s)*\n", data.PrimeiraLeitura.Referencia))
		if data.PrimeiraLeitura.Titulo != "" {
			sb.WriteString(fmt.Sprintf("*%s*\n", data.PrimeiraLeitura.Titulo))
		}
		sb.WriteString(fmt.Sprintf("%s\n\n", strings.TrimSpace(data.PrimeiraLeitura.Texto)))
	}

	// Segunda leitura is absent on weekdays and returned as an object on solemnities.
	if reading, ok := data.SegundaLeitura.(map[string]interface{}); ok {
		if reference, ok := reading["referencia"].(string); ok && reference != "" {
			fmt.Fprintf(&sb, "📜 *2ª Leitura (%s)*\n", reference)
			if title, ok := reading["titulo"].(string); ok && title != "" {
				fmt.Fprintf(&sb, "*%s*\n", title)
			}
			if text, ok := reading["texto"].(string); ok {
				fmt.Fprintf(&sb, "%s\n\n", strings.TrimSpace(text))
			}
		}
	}
	// Salmo
	if data.Salmo.Referencia != "" {
		sb.WriteString(fmt.Sprintf("🎵 *Salmo Responsorial (%s)*\n", data.Salmo.Referencia))
		if data.Salmo.Refrao != "" {
			sb.WriteString(fmt.Sprintf("👉 *R:* %s\n", data.Salmo.Refrao))
		}
		if data.Salmo.Texto != "" {
			sb.WriteString(fmt.Sprintf("_%s_\n\n", strings.TrimSpace(data.Salmo.Texto)))
		} else {
			sb.WriteString("\n")
		}
	}

	// Evangelho
	if data.Evangelho.Referencia != "" {
		sb.WriteString(fmt.Sprintf("✨ *Evangelho (%s)*\n", data.Evangelho.Referencia))
		if data.Evangelho.Titulo != "" {
			sb.WriteString(fmt.Sprintf("*%s*\n", data.Evangelho.Titulo))
		}
		sb.WriteString(fmt.Sprintf("%s\n\n", strings.TrimSpace(data.Evangelho.Texto)))
	}

	sb.WriteString("🔗 [Acessar Liturgia Completa no Portal Canção Nova](https://liturgia.cancaonova.com/)")

	// Telegram delivery already splits long messages; preserve all readings.
	return sb.String(), nil
}

func trimText(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "..."
}
