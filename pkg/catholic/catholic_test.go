package catholic

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseReference(t *testing.T) {
	tests := []struct {
		input       string
		expectedPt  string
		expectedBg  string
		expectedCh  int
		expectedSt  int
		expectedEnd int
	}{
		{"João 3:16", "São João", "Jn", 3, 16, 16},
		{"joao 3, 16", "São João", "Jn", 3, 16, 16},
		{"Mateus 6:9-13", "São Mateus", "Mt", 6, 9, 13},
		{"Salmos 23:1", "Salmos", "Ps", 23, 1, 1},
		{"sl 91:1-2", "Salmos", "Ps", 91, 1, 2},
		{"Sabedoria 3:1", "Sabedoria", "Sap", 3, 1, 1},
		{"tb 4:7", "Tobias", "Tob", 4, 7, 7},
		{"1 Coríntios 13:4-7", "1 Coríntios", "1Cor", 13, 4, 7},
		{"Gênesis 1:1", "Gênesis", "Gn", 1, 1, 1},
		{"Apocalipse 21:4", "Apocalipse", "Apc", 21, 4, 4},
	}

	for _, tc := range tests {
		def, ch, st, end, err := ParseReference(tc.input)
		if err != nil {
			t.Errorf("ParseReference(%q) error: %v", tc.input, err)
			continue
		}
		if def.DisplayName != tc.expectedPt {
			t.Errorf("ParseReference(%q) DisplayName = %s, want %s", tc.input, def.DisplayName, tc.expectedPt)
		}
		if def.BibleGetCode != tc.expectedBg {
			t.Errorf("ParseReference(%q) BibleGetCode = %s, want %s", tc.input, def.BibleGetCode, tc.expectedBg)
		}
		if ch != tc.expectedCh || st != tc.expectedSt || end != tc.expectedEnd {
			t.Errorf("ParseReference(%q) = (%d, %d, %d), want (%d, %d, %d)", tc.input, ch, st, end, tc.expectedCh, tc.expectedSt, tc.expectedEnd)
		}
	}
}

func TestParseRandomReference(t *testing.T) {
	def, ch, st, end, err := ParseReference("")
	if err != nil {
		t.Fatalf("ParseReference(\"\") failed: %v", err)
	}
	if def.DisplayName == "" || ch <= 0 || st <= 0 || end <= 0 {
		t.Errorf("unexpected empty random reference: %+v, ch=%d, st=%d, end=%d", def, ch, st, end)
	}
}

func TestFetchPassageBothVersions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	result, err := FetchPassage(ctx, "João 3:16")
	if err != nil {
		t.Fatalf("FetchPassage(João 3:16) failed: %v", err)
	}

	// Must contain both Portuguese and Latin Vulgate
	if !strings.Contains(result, "Português") || !strings.Contains(result, "Vulgata Latina") {
		t.Errorf("expected both Portuguese and Latin Vulgate in output, got: %s", result)
	}
	if !strings.Contains(strings.ToLower(result), "filho") && !strings.Contains(strings.ToLower(result), "vida") {
		t.Errorf("expected Portuguese text content, got: %s", result)
	}
	if !strings.Contains(strings.ToLower(result), "filium") && !strings.Contains(strings.ToLower(result), "mundum") {
		t.Errorf("expected Latin Vulgate content, got: %s", result)
	}
}

func TestFetchSantoDoDia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	text, err := FetchSantoDoDia(ctx)
	if err != nil {
		t.Fatalf("FetchSantoDoDia failed: %v", err)
	}

	if !strings.Contains(text, "Santo") && !strings.Contains(text, "São") && !strings.Contains(text, "Santa") {
		t.Errorf("expected saint name in text, got: %s", text)
	}
}
