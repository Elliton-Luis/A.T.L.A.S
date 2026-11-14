package nlu

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

type ProviderError struct {
	Provider, Model, Code string
	Status                int
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s model=%s HTTP=%d code=%s", e.Provider, e.Model, e.Status, e.Code)
}

type TransportError struct {
	Provider string
	Cause    error
}

func (e *TransportError) Error() string {
	return e.Provider + ": falha de conexão ou tempo de resposta"
}
func (e *TransportError) Unwrap() error { return e.Cause }

var providerCode = regexp.MustCompile(`^[a-zA-Z0-9_]{1,80}$`)
var ErrNoProvider = errors.New("nenhum provedor de IA configurado")
var ErrTruncated = errors.New("resposta truncada pelo limite de tokens")
var ErrNoFinalAnswer = errors.New("provedor não retornou uma resposta final")

func modelFallbackAllowed(err error) bool {
	var api *ProviderError
	return errors.As(err, &api) && (api.Status == 429 || api.Status == 404 || api.Status >= 500) || errors.Is(err, ErrTruncated) || errors.Is(err, ErrNoFinalAnswer)
}

func IsProviderFailure(err error) bool {
	var api *ProviderError
	var transport *TransportError
	return errors.As(err, &api) || errors.As(err, &transport) || errors.Is(err, ErrNoProvider) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func FailureMessage(err error) string {
	var api *ProviderError
	if errors.Is(err, ErrNoProvider) {
		return "⚠️ Falta configurar um provedor de IA para interpretar esse pedido. Nenhuma ação foi executada."
	}
	if errors.As(err, &api) {
		switch api.Status {
		case 429:
			return "⚠️ A IA atingiu um limite de uso e não consegui uma resposta válida das alternativas. Nenhuma ação foi executada; tente novamente em instantes. Não precisa encurtar a mensagem."
		case 401, 403:
			return "⚠️ A IA recusou a credencial ou o acesso ao modelo. Nenhuma ação foi executada; a configuração do provedor precisa ser conferida."
		}
	}
	return "⚠️ Não consegui obter uma interpretação válida da IA agora. Nenhuma ação desse pedido foi executada. Não precisa encurtar a mensagem."
}
