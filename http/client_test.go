package http

import (
	"crypto/tls"
	nethttp "net/http"
	"slices"
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
