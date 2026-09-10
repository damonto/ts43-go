package ts43

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type RoundTripper struct {
	log *loggingTransport
}

type idleConnectionsCloser interface {
	CloseIdleConnections()
}

func NewRoundTripper(logger *slog.Logger) RoundTripper {
	return newRoundTripper(logger, nil)
}

func newRoundTripper(logger *slog.Logger, transport http.RoundTripper) RoundTripper {
	return RoundTripper{
		log: newLoggingTransport(logger, ts43Transport(transport)),
	}
}

func withTS43Transport(logger *slog.Logger, doer HTTPDoer) HTTPDoer {
	client, ok := doer.(*http.Client)
	if !ok {
		return doer
	}
	if client == nil {
		return nil
	}
	switch client.Transport.(type) {
	case nil, *http.Transport:
	default:
		return client
	}
	clone := *client
	clone.Transport = newRoundTripper(logger, client.Transport)
	return &clone
}

func (r RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.log.RoundTrip(req)
}

func (r RoundTripper) CloseIdleConnections() {
	closer, ok := r.log.transport.(idleConnectionsCloser)
	if ok {
		closer.CloseIdleConnections()
	}
}

type loggingTransport struct {
	transport http.RoundTripper
	logger    *slog.Logger
}

func newLoggingTransport(logger *slog.Logger, transport http.RoundTripper) *loggingTransport {
	return &loggingTransport{logger: logger, transport: transport}
}

func (l *loggingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := requestBody(request)
	if err != nil {
		return nil, err
	}
	l.logger.Debug("[HTTP] sending request to",
		"url", request.URL.String(),
		"headers", request.Header.Clone(),
		"body", string(body),
	)

	response, err := l.transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err = responseBody(response)
	if err != nil {
		return nil, err
	}
	l.logger.Debug("[HTTP] received response from",
		"url", request.URL.String(),
		"status", response.Status,
		"headers", response.Header.Clone(),
		"body", string(body),
	)
	return response, nil
}

func requestBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	body, readErr := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	request.Body = io.NopCloser(bytes.NewBuffer(body))
	if readErr != nil {
		readErr = fmt.Errorf("read request body: %w", readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close request body: %w", closeErr)
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	return body, nil
}

func responseBody(response *http.Response) ([]byte, error) {
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	response.Body = io.NopCloser(bytes.NewBuffer(body))
	if readErr != nil {
		readErr = fmt.Errorf("read response body: %w", readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close response body: %w", closeErr)
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	return body, nil
}

func ts43Transport(transport http.RoundTripper) http.RoundTripper {
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpTransport, ok := transport.(*http.Transport)
	if !ok {
		return transport
	}
	httpTransport = httpTransport.Clone()
	httpTransport.TLSClientConfig = ts43TLSConfig(httpTransport.TLSClientConfig)
	return httpTransport
}

func ts43TLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		cfg = new(tls.Config)
	} else {
		cfg = cfg.Clone()
	}
	if cfg.CipherSuites == nil {
		cfg.CipherSuites = []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			// Some carrier TS.43 endpoints still only negotiate TLS 1.2 RSA key exchange.
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		}
	}
	return cfg
}

func NewHTTPClient(logger *slog.Logger, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewRoundTripper(logger),
	}
}

// CloseIdleConnections closes idle connections held by the client's HTTP transport.
// It does nothing when the configured HTTP client does not support the operation.
func (c *Client) CloseIdleConnections() {
	closer, ok := c.runtime.httpClient.(idleConnectionsCloser)
	if ok {
		closer.CloseIdleConnections()
	}
}

type HTTPError struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("TS.43 HTTP status %d", e.StatusCode)
}

func DoRaw(ctx context.Context, client HTTPDoer, req *http.Request) ([]byte, []*http.Cookie, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	cookies := resp.Cookies()
	data, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		readErr = fmt.Errorf("read HTTP response body: %w", readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close HTTP response body: %w", closeErr)
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, cookies, &HTTPError{StatusCode: resp.StatusCode, Header: resp.Header, Body: data}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return data, cookies, nil
}
