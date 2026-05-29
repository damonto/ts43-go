package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/damonto/ts43-go/driver"
)

type Doer interface {
	Do(*nethttp.Request) (*nethttp.Response, error)
}

type RoundTripper struct {
	log *driver.LoggingTransport
}

func NewRoundTripper(logger *slog.Logger) RoundTripper {
	return RoundTripper{
		log: driver.NewLoggingTransport(logger, newTransport()),
	}
}

func (r RoundTripper) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return r.log.RoundTrip(req)
}

func newTransport() nethttp.RoundTripper {
	transport := nethttp.DefaultTransport.(*nethttp.Transport).Clone()
	transport.TLSClientConfig = ts43TLSConfig(transport.TLSClientConfig)
	return transport
}

func ts43TLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		cfg = new(tls.Config)
	} else {
		cfg = cfg.Clone()
	}
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
	return cfg
}

func NewHTTPClient(logger *slog.Logger, timeout time.Duration) *nethttp.Client {
	return &nethttp.Client{
		Timeout:   timeout,
		Transport: NewRoundTripper(logger),
	}
}

type HTTPError struct {
	StatusCode int
	Header     nethttp.Header
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("TS.43 HTTP status %d", e.StatusCode)
}

func DoRaw(ctx context.Context, client Doer, req *nethttp.Request) ([]byte, []*nethttp.Cookie, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	cookies := resp.Cookies()
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, cookies, &HTTPError{StatusCode: resp.StatusCode, Header: resp.Header, Body: data}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return data, cookies, nil
}
