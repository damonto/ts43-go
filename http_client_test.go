package ts43

import (
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
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
	transport, ok := got.(*http.Transport)
	if !ok {
		t.Fatalf("ts43Transport(nil) = %T, want *http.Transport", got)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Contains(transport.TLSClientConfig.CipherSuites, tt.want) {
				t.Errorf("CipherSuites = %v, want %d", transport.TLSClientConfig.CipherSuites, tt.want)
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
				t.Errorf("CipherSuites = %v, want explicit %v", got.CipherSuites, tt.suites)
			}
		})
	}
}

func TestTS43TransportPreservesCallerTransport(t *testing.T) {
	custom := http.DefaultTransport.(*http.Transport).Clone()
	custom.MaxIdleConns = 17
	custom.TLSClientConfig = &tls.Config{ServerName: "carrier.example"}
	passthrough := new(closeIdleTransport)
	tests := []struct {
		name           string
		transport      http.RoundTripper
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
				t.Errorf("same transport = %t, want %t", same, tt.wantSame)
			}
			if !tt.wantHTTP {
				return
			}
			transport, ok := got.(*http.Transport)
			if !ok {
				t.Fatalf("ts43Transport() = %T, want *http.Transport", got)
			}
			if transport.MaxIdleConns != tt.wantMaxIdle {
				t.Errorf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, tt.wantMaxIdle)
			}
			if transport.TLSClientConfig.ServerName != tt.wantServerName {
				t.Errorf("ServerName = %q, want %q", transport.TLSClientConfig.ServerName, tt.wantServerName)
			}
			if !slices.Contains(transport.TLSClientConfig.CipherSuites, tls.TLS_RSA_WITH_AES_128_GCM_SHA256) {
				t.Errorf("CipherSuites = %v, want legacy RSA suite", transport.TLSClientConfig.CipherSuites)
			}
		})
	}
	if len(custom.TLSClientConfig.CipherSuites) != 0 {
		t.Errorf("caller CipherSuites = %v, want unchanged", custom.TLSClientConfig.CipherSuites)
	}
}

func TestWithTS43Transport(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 17
	client := &http.Client{Transport: transport, Timeout: 19 * time.Second}
	customDoer := &stubDoer{}
	customClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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
					t.Errorf("withTS43Transport() = %T, want nil", got)
				}
				return
			}
			if same := got == tt.doer; same != tt.wantSame {
				t.Errorf("same doer = %t, want %t", same, tt.wantSame)
			}
			if !tt.wantClient {
				return
			}
			gotClient, ok := got.(*http.Client)
			if !ok {
				t.Fatalf("withTS43Transport() = %T, want *http.Client", got)
			}
			if tt.wantSame {
				return
			}
			if gotClient.Timeout != client.Timeout {
				t.Errorf("Timeout = %s, want %s", gotClient.Timeout, client.Timeout)
			}
			if _, ok := gotClient.Transport.(RoundTripper); !ok {
				t.Errorf("Transport = %T, want ts43.RoundTripper", gotClient.Transport)
			}
			if client.Transport != transport {
				t.Error("caller transport was mutated")
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
				client, ok := got.(*http.Client)
				if !ok {
					t.Fatalf("HTTP client = %T, want *http.Client", got)
				}
				if client.Timeout != defaultEntitlementTimeout {
					t.Errorf("Timeout = %s, want %s", client.Timeout, defaultEntitlementTimeout)
				}
				return
			}
			if got != tt.doer {
				t.Errorf("HTTP client = %T, want original %T", got, tt.doer)
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
				httpClient := &http.Client{Transport: newRoundTripper(logger, transport)}
				return newClient(Config{Logger: logger, HTTPClient: httpClient})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := new(closeIdleTransport)
			tt.newCloser(transport).CloseIdleConnections()
			if !transport.closed.Load() {
				t.Error("CloseIdleConnections() was not forwarded")
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
			transport := newLoggingTransport(slog.New(slog.NewTextHandler(io.Discard, nil)), roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("ReadAll(request.Body) error = %v", err)
				}
				if string(body) != tt.requestBody {
					t.Errorf("request body = %q, want %q", body, tt.requestBody)
				}
				return &http.Response{
					StatusCode: 200,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tt.responseBody)),
				}, nil
			}))

			req, err := http.NewRequest(http.MethodPost, "https://example.test", strings.NewReader(tt.requestBody))
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
				t.Errorf("response body = %q, want %q", got, tt.responseBody)
			}
		})
	}
}

func TestBodyLoggingRestoresBodiesOnError(t *testing.T) {
	readErr := errors.New("read body")
	closeErr := errors.New("close body")
	targets := []struct {
		name         string
		read         func(io.ReadCloser) (io.ReadCloser, error)
		readContext  string
		closeContext string
	}{
		{
			name: "request",
			read: func(body io.ReadCloser) (io.ReadCloser, error) {
				req := &http.Request{Body: body}
				_, err := requestBody(req)
				return req.Body, err
			},
			readContext:  "read request body",
			closeContext: "close request body",
		},
		{
			name: "response",
			read: func(body io.ReadCloser) (io.ReadCloser, error) {
				resp := &http.Response{Body: body}
				_, err := responseBody(resp)
				return resp.Body, err
			},
			readContext:  "read response body",
			closeContext: "close response body",
		},
	}
	cases := []struct {
		name       string
		readError  bool
		closeError bool
	}{
		{name: "read error", readError: true},
		{name: "close error", closeError: true},
		{name: "read and close errors", readError: true, closeError: true},
	}

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					reader := io.Reader(strings.NewReader("partial body"))
					if tc.readError {
						reader = io.MultiReader(reader, errorReader{err: readErr})
					}
					body := &trackedReadCloser{Reader: reader}
					if tc.closeError {
						body.closeErr = closeErr
					}

					restored, err := target.read(body)
					if err == nil {
						t.Fatal("body read error = nil, want error")
					}
					if got := errors.Is(err, readErr); got != tc.readError {
						t.Errorf("errors.Is(error, readErr) = %t, want %t", got, tc.readError)
					}
					if got := errors.Is(err, closeErr); got != tc.closeError {
						t.Errorf("errors.Is(error, closeErr) = %t, want %t", got, tc.closeError)
					}
					if tc.readError && !strings.Contains(err.Error(), target.readContext) {
						t.Errorf("error = %q, want context %q", err, target.readContext)
					}
					if tc.closeError && !strings.Contains(err.Error(), target.closeContext) {
						t.Errorf("error = %q, want context %q", err, target.closeContext)
					}
					if !body.closed {
						t.Error("original body was not closed")
					}
					got, err := io.ReadAll(restored)
					if err != nil {
						t.Fatalf("ReadAll(restored body) error = %v", err)
					}
					if string(got) != "partial body" {
						t.Errorf("restored body = %q, want %q", got, "partial body")
					}
				})
			}
		})
	}
}

func TestDoRawReturnsBodyErrors(t *testing.T) {
	readErr := errors.New("read body")
	closeErr := errors.New("close body")
	tests := []struct {
		name       string
		readError  bool
		closeError bool
	}{
		{name: "read error", readError: true},
		{name: "close error", closeError: true},
		{name: "read and close errors", readError: true, closeError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := io.Reader(strings.NewReader("partial body"))
			if tt.readError {
				reader = io.MultiReader(reader, errorReader{err: readErr})
			}
			body := &trackedReadCloser{Reader: reader}
			if tt.closeError {
				body.closeErr = closeErr
			}
			client := fakeDoer(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})

			_, _, err := DoRaw(t.Context(), client, new(http.Request))
			if err == nil {
				t.Fatal("DoRaw() error = nil, want error")
			}
			if got := errors.Is(err, readErr); got != tt.readError {
				t.Errorf("errors.Is(error, readErr) = %t, want %t", got, tt.readError)
			}
			if got := errors.Is(err, closeErr); got != tt.closeError {
				t.Errorf("errors.Is(error, closeErr) = %t, want %t", got, tt.closeError)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stubDoer struct{}

func (*stubDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected request")
}

type closeIdleTransport struct {
	closed atomic.Bool
}

func (*closeIdleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected round trip")
}

func (t *closeIdleTransport) CloseIdleConnections() {
	t.closed.Store(true)
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type trackedReadCloser struct {
	io.Reader
	closeErr error
	closed   bool
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}
