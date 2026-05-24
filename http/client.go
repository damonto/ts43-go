package http

import (
	"context"
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
	return nethttp.DefaultTransport.(*nethttp.Transport).Clone()
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
