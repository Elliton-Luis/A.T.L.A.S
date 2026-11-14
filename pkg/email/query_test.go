package email

import (
	"bufio"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestQueryPreservesFilters(t *testing.T) {
	now := time.Date(2026, 11, 14, 8, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	q := ParseQuery("leia os emails do João de ontem", now)
	if q.From != "João" || q.Unread || q.Since.Format("2006-01-02") != "2026-11-13" || q.Before.Format("2006-01-02") != "2026-11-14" {
		t.Fatalf("filtros perdidos: %+v", q)
	}
	q = ParseQuery(`remetente: banco@exemplo.com assunto: fatura desde 2026-11-01 não lidos`, now)
	for _, part := range []string{`FROM "banco@exemplo.com"`, `SUBJECT "fatura"`, "SINCE 01-Nov-2026", "UNSEEN"} {
		if !strings.Contains(q.searchCriteria(), part) {
			t.Fatalf("filtro ausente %s: %s", part, q.searchCriteria())
		}
	}
	if !ParseQuery("", now).Unread {
		t.Fatal("consulta padrão deve buscar não lidos")
	}
}

func TestReadMIMEContent(t *testing.T) {
	raw := "Subject: =?UTF-8?Q?Fatura_de_amanh=C3=A3?=\r\nFrom: Banco <banco@example.com>\r\nContent-Type: multipart/alternative; boundary=atlas\r\n\r\n--atlas\r\nContent-Type: text/html\r\n\r\n<p>Versão HTML</p>\r\n--atlas\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nVence amanh=C3=A3: R$ 150.\r\n--atlas--\r\n"
	var summary MessageSummary
	readMessageContent([]byte(raw), &summary)
	if summary.Subject != "Fatura de amanhã" || !strings.Contains(summary.Snippet, "Vence amanhã: R$ 150.") || strings.Contains(summary.Snippet, "Versão HTML") {
		t.Fatalf("MIME incorreto: %+v", summary)
	}
}

func TestIMAPFailuresAreNotEmptyInbox(t *testing.T) {
	tp := &textproto.Conn{Reader: textproto.Reader{R: bufio.NewReader(strings.NewReader("A1 NO search failed\r\n"))}}
	if _, err := readSearchIDs(tp, "A1"); err == nil {
		t.Fatal("erro de busca ignorado")
	}
	tp = &textproto.Conn{Reader: textproto.Reader{R: bufio.NewReader(strings.NewReader("A2 NO fetch failed\r\n"))}}
	if _, err := parseFetchResponse(tp, "A2", "10"); err == nil {
		t.Fatal("fetch recusado virou mensagem fictícia")
	}
}
