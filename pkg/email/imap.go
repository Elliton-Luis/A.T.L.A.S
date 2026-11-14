package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

var wordDecoder = new(mime.WordDecoder)

// MessageSummary holds the high-level metadata and snippet of an email.
type MessageSummary struct {
	ID          string
	From        string
	Subject     string
	Date        string
	Snippet     string
	UIDValidity string
}

// Config defines the connection details for a read-only IMAP server.
type Config struct {
	Host               string
	Port               string
	User               string
	Password           string
	Timeout            time.Duration
	InsecureSkipVerify bool
}

// Reader provides strictly read-only access to an IMAP mailbox.
type Reader struct {
	cfg Config
}

// NewReader creates a new read-only email reader with robust host sanitization.
func NewReader(cfg Config) *Reader {
	host := strings.ToLower(strings.TrimSpace(cfg.Host))
	host = strings.TrimPrefix(host, "ssl://")
	host = strings.TrimPrefix(host, "imaps://")
	host = strings.TrimPrefix(host, "imap://")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")

	// Auto-heal common mistakes: imap@gmail.com or user@gmail.com in host
	if strings.Contains(host, "@") {
		parts := strings.Split(host, "@")
		host = parts[len(parts)-1]
	}

	// Auto-fill well known providers
	if host == "gmail.com" || host == "googlemail.com" {
		host = "imap.gmail.com"
	} else if host == "outlook.com" || host == "hotmail.com" || host == "office365.com" {
		host = "outlook.office365.com"
	} else if host == "yahoo.com" {
		host = "imap.mail.yahoo.com"
	}

	// Handle host:port
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		host = parts[0]
		if len(parts) > 1 && parts[1] != "" {
			cfg.Port = parts[1]
		}
	}

	cfg.Host = host
	if cfg.Port == "" {
		cfg.Port = "993"
	}
	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.User = strings.TrimSpace(cfg.User)

	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Reader{cfg: cfg}
}

// IsConfigured checks if the required IMAP credentials are present.
func (r *Reader) IsConfigured() bool {
	return r.cfg.Host != "" && r.cfg.User != "" && r.cfg.Password != ""
}

// FetchRecentUnread connects via TLS, logs in, examines INBOX (read-only),
// and fetches up to maxCount recent emails without blocking.
func (r *Reader) FetchRecentUnread(ctx context.Context, maxCount int) ([]MessageSummary, error) {
	return r.Fetch(ctx, Query{Unread: true}, maxCount)
}

func (r *Reader) Fetch(ctx context.Context, query Query, maxCount int) ([]MessageSummary, error) {
	if !r.IsConfigured() {
		return nil, fmt.Errorf("credenciais IMAP não configuradas no arquivo .env (IMAP_HOST, IMAP_USER, IMAP_PASS)")
	}
	if maxCount <= 0 {
		maxCount = 5
	}

	addr := fmt.Sprintf("%s:%s", r.cfg.Host, r.cfg.Port)
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout: 5 * time.Second,
		},
		Config: &tls.Config{
			ServerName:         r.cfg.Host,
			InsecureSkipVerify: r.cfg.InsecureSkipVerify,
		},
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao servidor de e-mail (%s): %w", addr, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(r.cfg.Timeout))

	tp := textproto.NewConn(conn)
	tagIdx := 0
	nextTag := func() string {
		tagIdx++
		return fmt.Sprintf("A%03d", tagIdx)
	}

	// 1. Read initial greeting
	_, err = tp.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("erro lendo saudação do servidor: %w", err)
	}

	// 2. Authenticate (LOGIN)
	loginTag := nextTag()
	loginCmd := fmt.Sprintf("%s LOGIN %s %s", loginTag, escapeIMAPString(r.cfg.User), escapeIMAPString(r.cfg.Password))
	if err := tp.PrintfLine("%s", loginCmd); err != nil {
		return nil, fmt.Errorf("envio login: %w", err)
	}
	if err := waitForTagOK(tp, loginTag); err != nil {
		return nil, fmt.Errorf("autenticação recusada: %w", err)
	}

	defer func() {
		logoutTag := nextTag()
		_ = tp.PrintfLine("%s LOGOUT", logoutTag)
	}()

	// 3. EXAMINE INBOX (Strictly READ-ONLY mode per RFC 3501)
	examineTag := nextTag()
	if err := tp.PrintfLine("%s EXAMINE INBOX", examineTag); err != nil {
		return nil, fmt.Errorf("examine inbox: %w", err)
	}

	totalExists := 0
	uidValidity := ""
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return nil, fmt.Errorf("examine inbox read: %w", err)
		}
		if strings.HasPrefix(line, examineTag+" OK") {
			break
		}
		if strings.HasPrefix(line, examineTag+" NO") || strings.HasPrefix(line, examineTag+" BAD") {
			return nil, fmt.Errorf("abertura somente-leitura recusada: %s", line)
		}
		// Capture * N EXISTS (e.g. * 124 EXISTS)
		if idx := strings.Index(line, "[UIDVALIDITY "); idx >= 0 {
			uidValidity = strings.SplitN(line[idx+len("[UIDVALIDITY "):], "]", 2)[0]
		}
		if strings.HasPrefix(line, "*") && strings.Contains(line, "EXISTS") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if count, err := strconv.Atoi(parts[1]); err == nil {
					totalExists = count
				}
			}
		}
	}

	if totalExists == 0 {
		return []MessageSummary{}, nil
	}

	// 4. Search for UNSEEN messages
	searchTag := nextTag()
	if err := tp.PrintfLine("%s UID SEARCH CHARSET UTF-8 %s", searchTag, query.searchCriteria()); err != nil {
		return nil, fmt.Errorf("envio de busca: %w", err)
	}
	ids, err := readSearchIDs(tp, searchTag)
	if err != nil {
		return nil, fmt.Errorf("busca de emails: %w", err)
	}

	// Do not substitute read messages when an unread/filter query returns no matches.
	if len(ids) > 0 {
		// Newest first
		if len(ids) > maxCount {
			ids = ids[len(ids)-maxCount:]
		}
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}

	var results []MessageSummary
	for _, id := range ids {
		fetchTag := nextTag()
		fetchCmd := fmt.Sprintf("%s UID FETCH %s (UID BODY.PEEK[]<0.16384>)", fetchTag, id)
		if err := tp.PrintfLine("%s", fetchCmd); err != nil {
			return nil, fmt.Errorf("envio fetch: %w", err)
		}

		summary, err := parseFetchResponse(tp, fetchTag, id)
		if err != nil {
			return nil, err
		}
		summary.UIDValidity = uidValidity
		if summary.Subject != "" || summary.From != "" {
			results = append(results, summary)
		}
	}

	return results, nil
}

func waitForTagOK(tp *textproto.Conn, tag string) error {
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return err
		}
		if strings.HasPrefix(line, tag+" OK") {
			return nil
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return fmt.Errorf("servidor respondeu: %s", line)
		}
	}
}

func readSearchIDs(tp *textproto.Conn, tag string) ([]string, error) {
	var ids []string
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "* SEARCH") {
			parts := strings.Fields(line)
			if len(parts) > 2 {
				ids = append(ids, parts[2:]...)
			}
		}
		if strings.HasPrefix(line, tag+" OK") {
			break
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return nil, fmt.Errorf("erro na busca: %s", line)
		}
	}
	return ids, nil
}

func parseFetchResponse(tp *textproto.Conn, tag, id string) (MessageSummary, error) {
	summary := MessageSummary{ID: id}
	found := false

	for {
		line, err := tp.ReadLine()
		if err != nil {
			return summary, fmt.Errorf("leitura fetch: %w", err)
		}

		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			return summary, fmt.Errorf("fetch recusado: %s", line)
		}
		if strings.HasPrefix(line, tag+" OK") {
			break
		}

		// Handle literal length: e.g. {142}
		trimmed := strings.TrimSpace(line)
		literalBytes := 0
		if strings.HasSuffix(trimmed, "}") {
			idx := strings.LastIndex(trimmed, "{")
			if idx != -1 {
				numStr := trimmed[idx+1 : len(trimmed)-1]
				if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
					literalBytes = n
				}
			}
		}

		if literalBytes > 0 {
			found = true
			if literalBytes > 65536 {
				return summary, fmt.Errorf("resposta IMAP excede limite de leitura")
			}
			buf := make([]byte, literalBytes)
			_, err := io.ReadFull(tp.R, buf)
			if err != nil {
				return summary, fmt.Errorf("conteúdo IMAP incompleto: %w", err)
			}
			readMessageContent(buf, &summary)
			continue
		}

		parseHeaderLine(line, &summary)
	}
	if !found {
		return summary, fmt.Errorf("UID %s não retornou conteúdo", id)
	}

	if summary.Subject == "" {
		summary.Subject = "(Sem assunto)"
	}
	if summary.From == "" {
		summary.From = "(Remetente desconhecido)"
	}

	return summary, nil
}

func parseHeaderLine(line string, summary *MessageSummary) {
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "subject:") && summary.Subject == "" {
		summary.Subject = decodeHeader(line[len("subject:"):])
	} else if strings.HasPrefix(lower, "from:") && summary.From == "" {
		summary.From = decodeHeader(line[len("from:"):])
	} else if strings.HasPrefix(lower, "date:") && summary.Date == "" {
		summary.Date = strings.TrimSpace(line[len("date:"):])
	}
}

func decodeHeader(val string) string {
	val = strings.TrimSpace(val)
	val = strings.Trim(val, "\"")
	if decoded, err := wordDecoder.DecodeHeader(val); err == nil && decoded != "" {
		return strings.TrimSpace(decoded)
	}
	return val
}

func escapeIMAPString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
