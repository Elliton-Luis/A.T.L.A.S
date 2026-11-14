package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"atlas/pkg/email"
)

type emailReader interface {
	IsConfigured() bool
	Fetch(context.Context, email.Query, int) ([]email.MessageSummary, error)
}

func (s *Server) readEmails(ctx context.Context, text string) (string, error) {
	return s.emailResult(ctx, text, email.WantsSummary(text))
}

func (s *Server) emailResult(ctx context.Context, text string, summary bool) (string, error) {
	query := email.ParseQuery(text, time.Now().In(s.cfg.Location))
	messages, err := s.emailReader.Fetch(ctx, query, 7)
	if err != nil {
		return "", err
	}
	if summary {
		return email.CurateWithSummary(ctx, s.nluClient, messages, emailScope(query))
	}
	return email.CurateEmails(ctx, s.nluClient, messages)
}

func emailScope(query email.Query) string {
	var filters []string
	if query.Unread {
		filters = append(filters, "não lidas")
	}
	if query.From != "" {
		filters = append(filters, "do remetente "+query.From)
	}
	if query.Subject != "" {
		filters = append(filters, "com assunto "+query.Subject)
	}
	if !query.Since.IsZero() {
		filters = append(filters, "desde "+query.Since.Format("02/01/2006"))
	}
	if !query.Before.IsZero() {
		filters = append(filters, "antes de "+query.Before.Format("02/01/2006"))
	}
	if query.UID != "" {
		filters = append(filters, fmt.Sprintf("com UID %s", query.UID))
	}
	if len(filters) == 0 {
		return "entre as mais recentes"
	}
	return "(" + strings.Join(filters, ", ") + ")"
}
