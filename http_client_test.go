package ts43

import (
	"crypto/tls"
	"io"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
)

func TestNewTransportTLSCipherSuites(t *testing.T) {
	tests := []struct {
		name string
		want uint16
	}{
		{name: "modern ecdhe rsa", want: tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		{name: "legacy rsa aes128", want: tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
		{name: "legacy rsa aes256", want: tls.TLS_RSA_WITH_AES_256_GCM_SHA384},
	}

	transport, ok := newTransport().(*nethttp.Transport)
	if !ok {
		t.Fatalf("newTransport() = %T, want *http.Transport", newTransport())
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Contains(transport.TLSClientConfig.CipherSuites, tt.want) {
				t.Fatalf("CipherSuites = %v, want %d", transport.TLSClientConfig.CipherSuites, tt.want)
			}
		})
	}
}

func TestLoggingTransportRestoresBodies(t *testing.T) {
	tests := []struct {
		name         string
		requestBody  string
		responseBody string
	}{
		{name: "request and response", requestBody: "hello", responseBody: "world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newLoggingTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), roundTripFunc(func(req *nethttp.Request) (*nethttp.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("ReadAll(request.Body) error = %v", err)
				}
				if string(body) != tt.requestBody {
					t.Fatalf("request body = %q, want %q", body, tt.requestBody)
				}
				return &nethttp.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Header:     make(nethttp.Header),
					Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
				}, nil
			}))

			req, err := nethttp.NewRequest(nethttp.MethodPost, "https://example.test", strings.NewReader(tt.requestBody))
			if err != nil {
				t.Fatalf("NewRequest error = %v", err)
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip error = %v", err)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("ReadAll(response.Body) error = %v", err)
			}
			if string(got) != tt.responseBody {
				t.Fatalf("response body = %q, want %q", got, tt.responseBody)
			}
		})
	}
}

type roundTripFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripFunc) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return f(req)
}
