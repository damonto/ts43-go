package ts43

import (
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	nethttp "net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTS43TransportDefaultTLSCipherSuites(t *testing.T) {
	tests := []struct {
		name string
		want uint16
	}{
		{name: "modern ecdhe rsa", want: tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		{name: "legacy rsa aes128", want: tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
		{name: "legacy rsa aes256", want: tls.TLS_RSA_WITH_AES_256_GCM_SHA384},
	}

	got := ts43Transport(nil)
	transport, ok := got.(*nethttp.Transport)
	if !ok {
		t.Fatalf("ts43Transport(nil) = %T, want *http.Transport", got)
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

func TestTS43TLSConfigPreservesExplicitCipherSuites(t *testing.T) {
	tests := []struct {
		name   string
		suites []uint16
	}{
		{name: "single explicit suite", suites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}},
		{name: "explicit empty suite list", suites: []uint16{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ts43TLSConfig(&tls.Config{CipherSuites: tt.suites})
			if got.CipherSuites == nil || !slices.Equal(got.CipherSuites, tt.suites) {
				t.Fatalf("CipherSuites = %v, want explicit %v", got.CipherSuites, tt.suites)
			}
		})
	}
}

func TestTS43TransportPreservesCallerTransport(t *testing.T) {
	custom := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	custom.MaxIdleConns = 17
	custom.TLSClientConfig = &tls.Config{ServerName: "carrier.example"}
	passthrough := new(closeIdleTransport)
	tests := []struct {
		name           string
		transport      nethttp.RoundTripper
		wantSame       bool
		wantHTTP       bool
		wantMaxIdle    int
		wantServerName string
	}{
		{
			name:           "HTTP transport is cloned and configured",
			transport:      custom,
			wantHTTP:       true,
			wantMaxIdle:    17,
			wantServerName: "carrier.example",
		},
		{
			name:      "arbitrary round tripper passes through",
			transport: passthrough,
			wantSame:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ts43Transport(tt.transport)
			if same := got == tt.transport; same != tt.wantSame {
				t.Fatalf("same transport = %t, want %t", same, tt.wantSame)
			}
			if !tt.wantHTTP {
				return
			}
			transport, ok := got.(*nethttp.Transport)
			if !ok {
				t.Fatalf("ts43Transport() = %T, want *http.Transport", got)
			}
			if transport.MaxIdleConns != tt.wantMaxIdle {
				t.Fatalf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, tt.wantMaxIdle)
			}
			if transport.TLSClientConfig.ServerName != tt.wantServerName {
				t.Fatalf("ServerName = %q, want %q", transport.TLSClientConfig.ServerName, tt.wantServerName)
			}
			if !slices.Contains(transport.TLSClientConfig.CipherSuites, tls.TLS_RSA_WITH_AES_128_GCM_SHA256) {
				t.Fatalf("CipherSuites = %v, want legacy RSA suite", transport.TLSClientConfig.CipherSuites)
			}
		})
	}
	if len(custom.TLSClientConfig.CipherSuites) != 0 {
		t.Fatalf("caller CipherSuites = %v, want unchanged", custom.TLSClientConfig.CipherSuites)
	}
}

func TestWithTS43Transport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	transport := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	transport.MaxIdleConns = 17
	client := &nethttp.Client{Transport: transport, Timeout: 19 * time.Second}
	customDoer := &stubDoer{}
	customClient := &nethttp.Client{Transport: roundTripFunc(func(*nethttp.Request) (*nethttp.Response, error) {
		return nil, errors.New("unexpected round trip")
	})}
	configured := NewHTTPClient(logger, 23*time.Second)
	tests := []struct {
		name       string
		doer       HTTPDoer
		wantSame   bool
		wantClient bool
	}{
		{name: "nil remains nil", wantSame: true},
		{name: "arbitrary doer remains unchanged", doer: customDoer, wantSame: true},
		{name: "client with arbitrary transport remains unchanged", doer: customClient, wantSame: true, wantClient: true},
		{name: "HTTP client is cloned and wrapped", doer: client, wantClient: true},
		{name: "configured TS43 client remains unchanged", doer: configured, wantSame: true, wantClient: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withTS43Transport(logger, tt.doer)
			if tt.doer == nil {
				if got != nil {
					t.Fatalf("withTS43Transport() = %T, want nil", got)
				}
				return
			}
			if same := got == tt.doer; same != tt.wantSame {
				t.Fatalf("same doer = %t, want %t", same, tt.wantSame)
			}
			if !tt.wantClient {
				return
			}
			gotClient, ok := got.(*nethttp.Client)
			if !ok {
				t.Fatalf("withTS43Transport() = %T, want *http.Client", got)
			}
			if tt.wantSame {
				return
			}
			if gotClient.Timeout != client.Timeout {
				t.Fatalf("Timeout = %s, want %s", gotClient.Timeout, client.Timeout)
			}
			if _, ok := gotClient.Transport.(RoundTripper); !ok {
				t.Fatalf("Transport = %T, want ts43.RoundTripper", gotClient.Transport)
			}
			if client.Transport != transport {
				t.Fatal("caller transport was mutated")
			}
		})
	}
}

func TestNewClientHTTPClient(t *testing.T) {
	customDoer := new(stubDoer)
	tests := []struct {
		name        string
		doer        HTTPDoer
		wantDefault bool
	}{
		{name: "default client", wantDefault: true},
		{name: "custom doer", doer: customDoer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newClient(Config{HTTPClient: tt.doer}).runtime.httpClient
			if tt.wantDefault {
				client, ok := got.(*nethttp.Client)
				if !ok {
					t.Fatalf("HTTP client = %T, want *http.Client", got)
				}
				if client.Timeout != defaultEntitlementTimeout {
					t.Fatalf("Timeout = %s, want %s", client.Timeout, defaultEntitlementTimeout)
				}
				return
			}
			if got != tt.doer {
				t.Fatalf("HTTP client = %T, want original %T", got, tt.doer)
			}
		})
	}
}

func TestCloseIdleConnections(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name      string
		newCloser func(*closeIdleTransport) idleConnectionsCloser
	}{
		{
			name: "round tripper",
			newCloser: func(transport *closeIdleTransport) idleConnectionsCloser {
				return newRoundTripper(logger, transport)
			},
		},
		{
			name: "client",
			newCloser: func(transport *closeIdleTransport) idleConnectionsCloser {
				httpClient := &nethttp.Client{Transport: newRoundTripper(logger, transport)}
				return newClient(Config{Logger: logger, HTTPClient: httpClient})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := new(closeIdleTransport)
			tt.newCloser(transport).CloseIdleConnections()
			if !transport.closed.Load() {
				t.Fatal("CloseIdleConnections() was not forwarded")
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

type stubDoer struct{}

func (*stubDoer) Do(*nethttp.Request) (*nethttp.Response, error) {
	return nil, errors.New("unexpected request")
}

type closeIdleTransport struct {
	closed atomic.Bool
}

func (*closeIdleTransport) RoundTrip(*nethttp.Request) (*nethttp.Response, error) {
	return nil, errors.New("unexpected round trip")
}

func (t *closeIdleTransport) CloseIdleConnections() {
	t.closed.Store(true)
}
