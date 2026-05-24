package driver

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
)

type LoggingTransport struct {
	Transport http.RoundTripper
	Logger    *slog.Logger
}

func NewLoggingTransport(logger *slog.Logger, transport http.RoundTripper) *LoggingTransport {
	return &LoggingTransport{Logger: logger, Transport: transport}
}

func (l *LoggingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := requestBody(request)
	if err != nil {
		return nil, err
	}
	l.Logger.Debug("[HTTP] sending request to",
		"url", request.URL.String(),
		"headers", request.Header.Clone(),
		"body", string(body),
	)

	response, err := l.Transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err = responseBody(response)
	if err != nil {
		return nil, err
	}
	l.Logger.Debug("[HTTP] received response from",
		"url", request.URL.String(),
		"status", response.Status,
		"headers", response.Header.Clone(),
		"body", string(body),
	)
	return response, nil
}

func requestBody(request *http.Request) ([]byte, error) {
	if request.Body != nil {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		request.Body.Close()
		request.Body = io.NopCloser(bytes.NewBuffer(body))
		return body, nil
	}
	return nil, nil
}

func responseBody(response *http.Response) ([]byte, error) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewBuffer(body))
	return body, nil
}
