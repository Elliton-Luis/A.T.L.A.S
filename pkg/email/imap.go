package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/textproto"
	"strings"
	"time"
)

// MessageSummary holds the high-level metadata and snippet of an email.
type MessageSummary struct {
	ID      string
	From    string
	Subject string
	Date    string
	Snippet string
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
// It uses EXAMINE instead of SELECT to guarantee that no flags or states can be altered on the server,
// and uses BODY.PEEK to prevent messages from automatically being marked as read.
type Reader struct {
	cfg Config
}

// NewReader creates a new read-only email reader.
func NewReader(cfg Config) *Reader {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Host = strings.TrimPrefix(cfg.Host, "ssl://")
	cfg.Host = strings.TrimPrefix(cfg.Host, "imaps://")
	cfg.Host = strings.TrimPrefix(cfg.Host, "imap://")
	cfg.Host = strings.TrimPrefix(cfg.Host, "https://")
	cfg.Host = strings.TrimPrefix(cfg.Host, "http://")

	// Auto-heal common typo: imap@gmail.com -> imap.gmail.com
	if strings.Contains(cfg.Host, "@") {
		cfg.Host = strings.ReplaceAll(cfg.Host, "@", ".")
	}

	// Handle host:port passed together in host
	if strings.Contains(cfg.Host, ":") {
		parts := strings.Split(cfg.Host, ":")
		cfg.Host = parts[0]
		if len(parts) > 1 && parts[1] != "" {
			cfg.Port = parts[1]
		}
	}

	if cfg.Port == "" {
		cfg.Port = "993"
	}
	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.User = strings.TrimSpace(cfg.User)

	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &Reader{cfg: cfg}
}

// IsConfigured checks if the required IMAP credentials are present.
func (r *Reader) IsConfigured() bool {
	return r.cfg.Host != "" && r.cfg.User != "" && r.cfg.Password != ""
}

// FetchRecentUnread connects via TLS, logs in, examines INBOX (read-only),
// and fetches up to maxCount unread or recent email headers.
func (r *Reader) FetchRecentUnread(ctx context.Context, maxCount int) ([]MessageSummary, error) {
	if !r.IsConfigured() {
		return nil, fmt.Errorf("credenciais IMAP não configuradas no arquivo .env (IMAP_HOST, IMAP_USER, IMAP_PASS)")
	}
	if maxCount <= 0 {
		maxCount = 5
	}

	addr := fmt.Sprintf("%s:%s", r.cfg.Host, r.cfg.Port)
	dialer := &tls.Dialer{
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

	// 1. Read initial server greeting
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

	// Ensure clean LOGOUT on return
	defer func() {
		logoutTag := nextTag()
		_ = tp.PrintfLine("%s LOGOUT", logoutTag)
	}()

	// 3. EXAMINE INBOX (Strictly READ-ONLY mode per RFC 3501)
	examineTag := nextTag()
	if err := tp.PrintfLine("%s EXAMINE INBOX", examineTag); err != nil {
		return nil, fmt.Errorf("examine inbox: %w", err)
	}
	if err := waitForTagOK(tp, examineTag); err != nil {
		return nil, fmt.Errorf("abertura somente-leitura da caixa de entrada: %w", err)
	}

	// 4. Search for UNSEEN messages first
	searchTag := nextTag()
	if err := tp.PrintfLine("%s SEARCH UNSEEN", searchTag); err != nil {
		return nil, fmt.Errorf("search unseen: %w", err)
	}

	ids, err := readSearchIDs(tp, searchTag)
	if err != nil {
		return nil, fmt.Errorf("ler ids unseen: %w", err)
	}

	// If no unseen messages, search for the last ALL messages so the user gets context
	if len(ids) == 0 {
		searchAllTag := nextTag()
		if err := tp.PrintfLine("%s SEARCH ALL", searchAllTag); err == nil {
			ids, _ = readSearchIDs(tp, searchAllTag)
		}
	}

	if len(ids) == 0 {
		return []MessageSummary{}, nil
	}

	// Take the most recent maxCount messages (last elements)
	if len(ids) > maxCount {
		ids = ids[len(ids)-maxCount:]
	}

	// Reverse so newest is first
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}

	var results []MessageSummary
	for _, id := range ids {
		fetchTag := nextTag()
		// Using BODY.PEEK so the message remains UNREAD on the server!
		fetchCmd := fmt.Sprintf("%s FETCH %s (BODY.PEEK[HEADER.FIELDS (SUBJECT FROM DATE)] BODY.PEEK[TEXT]<0.400>)", fetchTag, id)
		if err := tp.PrintfLine("%s", fetchCmd); err != nil {
			continue
		}

		summary := parseFetchResponse(tp, fetchTag, id)
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

func parseFetchResponse(tp *textproto.Conn, tag, id string) MessageSummary {
	summary := MessageSummary{ID: id}
	inHeaders := false

	for {
		line, err := tp.ReadLine()
		if err != nil {
			break
		}

		if strings.HasPrefix(line, tag+" OK") {
			break
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			break
		}

		lower := strings.ToLower(line)
		if strings.Contains(lower, "header.fields") {
			inHeaders = true
			continue
		}

		if inHeaders {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, ")") {
				inHeaders = false
				continue
			}

			if strings.HasPrefix(lower, "subject:") {
				summary.Subject = cleanHeaderValue(line[len("subject:"):])
			} else if strings.HasPrefix(lower, "from:") {
				summary.From = cleanHeaderValue(line[len("from:"):])
			} else if strings.HasPrefix(lower, "date:") {
				summary.Date = cleanHeaderValue(line[len("date:"):])
			}
		} else if !strings.HasPrefix(line, "*") && summary.Snippet == "" && strings.TrimSpace(line) != "" {
			snippet := strings.TrimSpace(line)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			summary.Snippet = snippet
		}
	}

	if summary.Subject == "" {
		summary.Subject = "(Sem assunto)"
	}
	if summary.From == "" {
		summary.From = "(Remetente desconhecido)"
	}

	return summary
}

func cleanHeaderValue(val string) string {
	val = strings.TrimSpace(val)
	// Basic RFC 2047 decode if needed or clean quotes
	val = strings.Trim(val, "\"")
	return val
}

func escapeIMAPString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}
