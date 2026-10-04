package ts43

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestWFCResponseIncludesStatusVersionAndToken(t *testing.T) {
	tests := []struct{ name, body string }{
		{"JSON", `{"VERS":{"version":2,"validity":60},"TOKEN":{"token":"next","validity":120},"ap2004":{"EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2,"ServiceFlow_URL":"https://portal.test/?a=1&b=2"}}`},
		{"XML", `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="2"/><parm name="validity" value="60"/></characteristic><characteristic type="TOKEN"><parm name="token" value="next"/><parm name="validity" value="120"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/><parm name="ServiceFlow_URL" value="https://portal.test/?a=1&amp;b=2"/></characteristic></wap-provisioningdoc>`},
		{"carrier XML with bare ampersand", `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="2"/><parm name="validity" value="60"/></characteristic><characteristic type="TOKEN"><parm name="token" value="next"/><parm name="validity" value="120"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/><parm name="ServiceFlow_URL" value="https://portal.test/?a=1&b=2"/></characteristic></wap-provisioningdoc>`},
	}
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{Now: func() time.Time { return now }, HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
				resp := response(200, tt.body)
				resp.Header = http.Header{"Set-Cookie": {"session=next"}}
				return resp, nil
			})})
			got, err := client.CheckWFCEntitlement(context.Background(), WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, Config: WFCConfig{Server: "https://example.test"}, AuthToken: AuthToken{Value: "previous"}})
			if err != nil {
				t.Fatal(err)
			}
			if !got.Status.Entitled() || got.Status.ServiceFlowURL != "https://portal.test/?a=1&b=2" {
				t.Errorf("status=%+v", got.Status)
			}
			if got.Configuration != (WFCConfiguration{Present: true, Version: 2, Validity: 60}) {
				t.Errorf("VERS=%+v", got.Configuration)
			}
			if got.AuthToken.Value != "next" || !got.AuthToken.ExpiresAt.Equal(now.Add(2*time.Minute)) || len(got.AuthToken.Cookies) != 1 {
				t.Errorf("token metadata=%+v", got.AuthToken)
			}
		})
	}
}

func TestWFCPayloadReuseClearsAbsentMetadata(t *testing.T) {
	for _, body := range []string{`{}`, `<wap-provisioningdoc/>`} {
		t.Run(body, func(t *testing.T) {
			var payload wfcPayload
			if err := payload.UnmarshalBinary([]byte(`{"TOKEN":{"token":"stale"},"VERS":{"version":2,"validity":60}}`)); err != nil {
				t.Fatal(err)
			}
			if err := payload.UnmarshalBinary([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if payload.Token != nil || payload.Configuration.Present {
				t.Fatalf("stale metadata=%+v", payload)
			}
		})
	}
}
