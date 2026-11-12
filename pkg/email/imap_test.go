package email

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"ATLAS Test Org"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}, nil
}

func TestIMAPReaderWorkflow(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatalf("failed to start mock tls listener: %v", err)
	}
	defer listener.Close()

	port := strings.Split(listener.Addr().String(), ":")[1]

	// Handle mock IMAP server in goroutine
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)

		// Greeting
		fmt.Fprintf(writer, "* OK Mock IMAP Server Ready\r\n")
		writer.Flush()

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			tag := parts[0]
			cmd := strings.ToUpper(parts[1])

			switch cmd {
			case "LOGIN":
				fmt.Fprintf(writer, "%s OK LOGIN completed\r\n", tag)
			case "EXAMINE":
				fmt.Fprintf(writer, "* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)\r\n")
				fmt.Fprintf(writer, "* 10 EXISTS\r\n")
				fmt.Fprintf(writer, "%s OK [READ-ONLY] EXAMINE completed\r\n", tag)
			case "SEARCH":
				fmt.Fprintf(writer, "* SEARCH 101 102\r\n")
				fmt.Fprintf(writer, "%s OK SEARCH completed\r\n", tag)
			case "FETCH":
				fmt.Fprintf(writer, "* %s FETCH (BODY[HEADER.FIELDS (SUBJECT FROM DATE)] {120}\r\n", parts[2])
				fmt.Fprintf(writer, "Subject: Fatura do Cartao Disponivel\r\nFrom: banco@exemplo.com\r\nDate: 12 Nov 2026 20:00:00 -0300\r\n\r\nPrezado cliente, sua fatura fechou.)\r\n")
				fmt.Fprintf(writer, "%s OK FETCH completed\r\n", tag)
			case "LOGOUT":
				fmt.Fprintf(writer, "* BYE Mock server signing off\r\n")
				fmt.Fprintf(writer, "%s OK LOGOUT completed\r\n", tag)
				writer.Flush()
				return
			default:
				fmt.Fprintf(writer, "%s BAD Unknown command\r\n", tag)
			}
			writer.Flush()
		}
	}()

	reader := NewReader(Config{
		Host:               "127.0.0.1",
		Port:               port,
		User:               "teste@atlas.ai",
		Password:           "segredo123",
		Timeout:            3 * time.Second,
		InsecureSkipVerify: true,
	})

	if !reader.IsConfigured() {
		t.Fatalf("expected reader to be configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := reader.FetchRecentUnread(ctx, 2)
	if err != nil {
		t.Fatalf("FetchRecentUnread failed: %v", err)
	}

	if len(res) == 0 {
		t.Fatalf("expected at least 1 email returned, got 0")
	}

	if res[0].Subject != "Fatura do Cartao Disponivel" {
		t.Errorf("expected subject 'Fatura do Cartao Disponivel', got '%s'", res[0].Subject)
	}
	if res[0].From != "banco@exemplo.com" {
		t.Errorf("expected from 'banco@exemplo.com', got '%s'", res[0].From)
	}
}
