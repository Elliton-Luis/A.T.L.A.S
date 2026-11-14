package server

import (
	"context"
	"time"

	"atlas/pkg/email"
)

func (s *Server) readEmails(ctx context.Context, text string) (string, error) {
	query := email.ParseQuery(text, time.Now().In(s.cfg.Location))
	messages, err := s.emailReader.Fetch(ctx, query, 7)
	if err != nil {
		return "", err
	}
	return email.CurateEmails(ctx, s.nluClient, messages)
}
