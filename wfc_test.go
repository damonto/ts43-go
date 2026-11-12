package ts43

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWFCResetAndContinuation(t *testing.T) {
	for _, tt := range []struct {
		name             string
		ims, enabled     bool
		body             string
		wantError, reset bool
	}{
		{"reset disabled", true, false, `{"VERS":{"version":0,"validity":0}}`, false, true},
		{"reset enabled", true, true, `{"VERS":{"version":-1,"validity":-1}}`, false, true},
		{"stop until setting changes", true, true, `{"VERS":{"version":-2,"validity":-2}}`, false, true},
		{"setup mode missing WFC", false, true, `{"VERS":{"version":-1,"validity":-1}}`, true, false},
		{"empty body is not default", true, true, "", true, false},
		{"other application", false, false, `{"ap2003":{"EntitlementStatus":1}}`, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(Config{HTTPClient: &queueDoer{bodies: []string{tt.body}}})
			got, err := c.CheckWFCEntitlement(context.Background(), WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, AuthToken: AuthToken{Value: "old"}, Config: WFCConfig{Server: "https://example.test", NeedsIMSProvisioning: tt.ims, DefaultServiceEntitlementEnabled: tt.enabled}})
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				return
			}
			if got.ConfigurationReset != tt.reset || got.Status.Entitled() != tt.enabled || got.AuthToken.Value != "" {
				t.Errorf("result = %+v", got)
			}
		})
	}
	for _, event := range []WebsheetCallbackEvent{WebsheetEventVoWiFiDismissFlow, WebsheetEventVoWiFiEntitlementChanged} {
		t.Run(fmt.Sprintf("callback_%v", event), func(t *testing.T) {
			doer := &queueDoer{bodies: []string{`{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":0,"TC_Status":0,"AddrStatus":0,"ProvStatus":0}}`, `{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`}}
			c := newClient(Config{HTTPClient: doer})
			previous, err := c.CheckWFCEntitlement(context.Background(), WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, Config: WFCConfig{Server: "https://example.test"}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.ContinueWFC(context.Background(), previous, WebsheetResult{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			if event == WebsheetEventVoWiFiDismissFlow {
				if !got.Cancelled || got.Status != previous.Status || doer.calls != 1 {
					t.Errorf("cancel = %+v calls=%d", got, doer.calls)
				}
			} else if !got.Status.Entitled() || doer.calls != 2 {
				t.Errorf("complete = %+v calls=%d", got, doer.calls)
			}
		})
	}
}

func TestRegressionWFCProvisioning(t *testing.T) {
	tests := []struct {
		name   string
		status WFCStatus
	}{
		{"entitlement provisioning", WFCStatus{EntitlementStatus: WFCEntitlementProvisioning, TCStatus: WFCTCAvailable, AddrStatus: WFCAddressAvailable, ProvStatus: WFCProvisionProvisioned}},
		{"disabled awaiting provisioning", WFCStatus{EntitlementStatus: WFCEntitlementDisabled, TCStatus: WFCTCAvailable, AddrStatus: WFCAddressAvailable, ProvStatus: WFCProvisionNotProvisioned}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.ActivationMode(); got != WFCActivationInProgress {
				t.Errorf("ActivationMode() = %d, want in-progress %d", got, WFCActivationInProgress)
			}
		})
	}
}

func TestRegressionWFCReset(t *testing.T) {
	tests := []struct {
		name, body string
		want       WFCEntitlementStatus
	}{
		{"reset minus one", `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="-1"/><parm name="validity" value="-1"/></characteristic></wap-provisioningdoc>`, WFCEntitlementEnabled},
		{"reset minus two", `<wap-provisioningdoc><characteristic type="VERS"><parm name="version" value="-2"/><parm name="validity" value="-2"/></characteristic></wap-provisioningdoc>`, WFCEntitlementEnabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CheckWFCEntitlement(context.Background(), Config{HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) { return response(200, tt.body), nil })}, WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, Config: WFCConfig{Server: "https://example.test", DefaultServiceEntitlementEnabled: true, NeedsIMSProvisioning: true}, AuthToken: AuthToken{Value: "audit"}})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status.EntitlementStatus != tt.want {
				t.Errorf("status = %d, want %d; web flow = %v", got.Status.EntitlementStatus, tt.want, got.Status.NeedsUserWebFlow())
			}
		})
	}
}

func TestRegressionEmptyWFCApplication(t *testing.T) {
	tests := []struct{ name, body string }{
		{"JSON empty", `{}`},
		{"XML only token", `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="audit"/></characteristic></wap-provisioningdoc>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s WFCStatus
			if err := s.UnmarshalBinary([]byte(tt.body)); err == nil && s.NeedsUserWebFlow() {
				t.Errorf("missing application became actionable web flow: %+v", s)
			}
		})
	}
}

func TestRegressionWFCCallbacks(t *testing.T) {
	tests := []string{"WiFiCallingWebViewController.entitlementChanged()", "WiFiCallingWebViewController.cancelButtonClicked()", "WiFiCallingWebViewController.CloseWebView()"}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			var got WebsheetResult
			if err := got.UnmarshalText([]byte(tt)); err != nil {
				t.Errorf("configured controller callback rejected: %v", err)
			}
		})
	}
}

func TestRegressionWFCAdverseApplication(t *testing.T) {
	tests := []string{"ap2003", "ap2005"}
	for _, app := range tests {
		t.Run(app, func(t *testing.T) {
			body := strings.ReplaceAll(`{"APPLICATION":{"AppID":"APP","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`, "APP\"", app+`"`)
			var s WFCStatus
			if err := s.UnmarshalBinary([]byte(body)); err == nil && s.Entitled() {
				t.Errorf("response for %s entitled Wi-Fi Calling", app)
			}
		})
	}
}

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

func TestWFCJSONExplicitApplicationPrecedence(t *testing.T) {
	const body = `{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2},"ap2004":{"EntitlementStatus":2,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`
	// Selection must not depend on the iteration order of JSON object keys.
	for range 50 {
		var status WFCStatus
		if err := status.UnmarshalBinary([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if !status.Incompatible() {
			t.Fatalf("explicit application did not win: %+v", status)
		}
	}
}

func TestWFCJSONIgnoresUnrelatedApplicationBodies(t *testing.T) {
	var status WFCStatus
	body := `{"ap2003":5,"ap2004":{"EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`
	if err := status.UnmarshalBinary([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if !status.Entitled() {
		t.Fatalf("status=%+v", status)
	}
}

func TestWFCJSONIgnoresUnusedLegacyApplication(t *testing.T) {
	var status WFCStatus
	body := `{"APPLICATION":5,"ap2004":{"EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`
	if err := status.UnmarshalBinary([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if !status.Entitled() {
		t.Fatalf("status=%+v", status)
	}
}

func TestWFCJSONRejectsConflictingApplicationID(t *testing.T) {
	var status WFCStatus
	body := `{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2},"ap2004":{"AppID":"ap2003","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`
	if err := status.UnmarshalBinary([]byte(body)); err == nil {
		t.Fatal("conflicting application ID was accepted")
	}
}

func TestDiscoverWFC(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
		want     WFCConfig
		wantErr  error
	}{
		{
			name:     "GID1 match wins over MCCMNC fallback",
			identity: Identity{MCC: "208", MNC: "01", GID1: "01"},
			want: WFCConfig{
				CarrierName:                      "orangentn_fr",
				Server:                           "https://aes.mnc001.mcc208.pub.3gppnetwork.org",
				App:                              AppVoWiFi,
				Version:                          "8.0",
				ClientTS43:                       DefaultWFCClientTS43,
				DefaultServiceEntitlementEnabled: true,
				WebsheetClientName:               "WiFiCallingWebViewController",
			},
		},
		{
			name:     "unique MCCMNC fallback",
			identity: Identity{MCC: "208", MNC: "01"},
			want: WFCConfig{
				CarrierName:                      "orange_fr",
				Server:                           "https://aes.mnc001.mcc208.pub.3gppnetwork.org",
				App:                              AppVoWiFi,
				Version:                          "8.0",
				ClientTS43:                       DefaultWFCClientTS43,
				DefaultServiceEntitlementEnabled: true,
				WebsheetClientName:               "WiFiCallingWebViewController",
			},
		},
		{
			name:     "SPN match wins over MCCMNC candidates",
			identity: Identity{MCC: "204", MNC: "04", SPN: "U.S. CELLULAR"},
			want: WFCConfig{
				CarrierName:        "uscc_us",
				Server:             "https://aes.mnc580.mcc311.pub.3gppnetwork.org/rcs-entitlement",
				App:                AppVoWiFi,
				Version:            "2.0",
				ClientTS43:         DefaultWFCClientTS43,
				ShowVoWiFiWebview:  true,
				WebsheetClientName: "VoWiFiWebServiceFlow",
			},
		},
		{
			name:     "missing MCCMNC",
			identity: Identity{MCC: "999", MNC: "99"},
			wantErr:  ErrWFCConfigNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateWFCConfigs(t).Discover(tt.identity)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("privateWFCConfigs(t).Discover() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("privateWFCConfigs(t).Discover() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("privateWFCConfigs(t).Discover() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDiscoverWFCWebsheetClientName(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
		want     string
	}{
		{
			name:     "uscc uses VoWiFi service flow",
			identity: Identity{MCC: "311", MNC: "220"},
			want:     "VoWiFiWebServiceFlow",
		},
		{
			name:     "cspire uses VoWiFi service flow",
			identity: Identity{MCC: "311", MNC: "230"},
			want:     "VoWiFiWebServiceFlow",
		},
		{
			name:     "default uses WiFi calling web view",
			identity: Identity{MCC: "208", MNC: "01"},
			want:     "WiFiCallingWebViewController",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateWFCConfigs(t).Discover(tt.identity)
			if err != nil {
				t.Fatalf("privateWFCConfigs(t).Discover() error = %v", err)
			}
			if got.WebsheetClientName != tt.want {
				t.Errorf("WebsheetClientName = %q, want %q", got.WebsheetClientName, tt.want)
			}
		})
	}
}

func TestWFCStatusUnmarshalBinary(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		want     WFCStatus
		entitled bool
		webFlow  bool
	}{
		{
			name: "entitled XML",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="1"/><parm name="AddrStatus" value="1"/><parm name="ProvStatus" value="1"/></characteristic></wap-provisioningdoc>`),
			want: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionProvisioned,
			},
			entitled: true,
		},
		{
			name: "needs web flow XML",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="0"/><parm name="TC_Status" value="0"/><parm name="AddrStatus" value="0"/><parm name="ProvStatus" value="0"/><parm name="ServiceFlow_URL" value="https://example.test/e911"/><parm name="ServiceFlow_UserData" value="token=abc"/></characteristic></wap-provisioningdoc>`),
			want: WFCStatus{
				EntitlementStatus:   WFCEntitlementDisabled,
				TCStatus:            WFCTCNotAvailable,
				AddrStatus:          WFCAddressNotAvailable,
				ProvStatus:          WFCProvisionNotProvisioned,
				ServiceFlowURL:      "https://example.test/e911",
				ServiceFlowUserData: "token=abc",
			},
			webFlow: true,
		},
		{
			name: "needs web flow XML with bare ampersand",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="0"/><parm name="TC_Status" value="0"/><parm name="AddrStatus" value="0"/><parm name="ProvStatus" value="0"/><parm name="ServiceFlow_URL" value="https://example.test/e911"/><parm name="ServiceFlow_UserData" value="PostData=abc&req_locale=en_US"/></characteristic></wap-provisioningdoc>`),
			want: WFCStatus{
				EntitlementStatus:   WFCEntitlementDisabled,
				TCStatus:            WFCTCNotAvailable,
				AddrStatus:          WFCAddressNotAvailable,
				ProvStatus:          WFCProvisionNotProvisioned,
				ServiceFlowURL:      "https://example.test/e911",
				ServiceFlowUserData: "PostData=abc&req_locale=en_US",
			},
			webFlow: true,
		},
		{
			name: "needs web flow XML keeps escaped ampersand",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="0"/><parm name="TC_Status" value="0"/><parm name="AddrStatus" value="0"/><parm name="ProvStatus" value="0"/><parm name="ServiceFlow_URL" value="https://example.test/e911"/><parm name="ServiceFlow_UserData" value="PostData=abc&amp;amp;req_locale=en_US"/></characteristic></wap-provisioningdoc>`),
			want: WFCStatus{
				EntitlementStatus:   WFCEntitlementDisabled,
				TCStatus:            WFCTCNotAvailable,
				AddrStatus:          WFCAddressNotAvailable,
				ProvStatus:          WFCProvisionNotProvisioned,
				ServiceFlowURL:      "https://example.test/e911",
				ServiceFlowUserData: "PostData=abc&amp;req_locale=en_US",
			},
			webFlow: true,
		},
		{
			name: "entitled JSON",
			body: []byte(`{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":"1","TC_Status":"2","AddrStatus":"2","ProvStatus":"2"}}`),
			want: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			},
			entitled: true,
		},
		{
			name: "lowercase JSON fields",
			body: []byte(`{"application":{"appid":"ap2004","entitlementstatus":"0","tc_status":"0","addrstatus":"0","provstatus":"0","serviceflow_url":"https://example.test/e911","serviceflow_userdata":"token=abc"}}`),
			want: WFCStatus{
				EntitlementStatus:   WFCEntitlementDisabled,
				TCStatus:            WFCTCNotAvailable,
				AddrStatus:          WFCAddressNotAvailable,
				ProvStatus:          WFCProvisionNotProvisioned,
				ServiceFlowURL:      "https://example.test/e911",
				ServiceFlowUserData: "token=abc",
			},
			webFlow: true,
		},
		{
			name: "VoWiFi JSON keyed by service id",
			body: []byte(`{"ap2004":{"EntitlementStatus":"1","TC_Status":"2","AddrStatus":"2","ProvStatus":"2"}}`),
			want: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			},
			entitled: true,
		},
		{
			name: "selects VoWiFi JSON service id",
			body: []byte(`{"ap2003":{"EntitlementStatus":"2"},"ap2004":{"EntitlementStatus":"1","TC_Status":"2","AddrStatus":"2","ProvStatus":"2"}}`),
			want: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			},
			entitled: true,
		},
		{
			name: "selects VoWiFi application XML",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2003"/><parm name="EntitlementStatus" value="2"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`),
			want: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			},
			entitled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got WFCStatus
			if err := got.UnmarshalBinary(tt.body); err != nil {
				t.Fatalf("WFCStatus.UnmarshalBinary() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("WFCStatus.UnmarshalBinary() = %+v, want %+v", got, tt.want)
			}
			if got.Entitled() != tt.entitled {
				t.Errorf("Entitled() = %t, want %t", got.Entitled(), tt.entitled)
			}
			if got.NeedsUserWebFlow() != tt.webFlow {
				t.Errorf("NeedsUserWebFlow() = %t, want %t", got.NeedsUserWebFlow(), tt.webFlow)
			}
		})
	}
}

func TestWFCStatusUnmarshalBinaryRequiresStatusFields(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "missing terms JSON",
			body: []byte(`{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":"1","AddrStatus":"2","ProvStatus":"2"}}`),
		},
		{
			name: "empty terms XML",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value=""/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`),
		},
		{
			name: "only app id XML",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/></characteristic></wap-provisioningdoc>`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got WFCStatus
			if err := got.UnmarshalBinary(tt.body); err == nil {
				t.Fatal("WFCStatus.UnmarshalBinary() error is nil")
			}
		})
	}
}

func TestWFCActivationMode(t *testing.T) {
	tests := []struct {
		name       string
		status     WFCStatus
		want       WFCActivationMode
		entitled   bool
		webFlow    bool
		inProgress bool
	}{
		{
			name: "web view when terms missing",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCNotAvailable,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionProvisioned,
			},
			want:    WFCActivationWebView,
			webFlow: true,
		},
		{
			name: "web view when address missing",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressNotAvailable,
				ProvStatus:        WFCProvisionProvisioned,
			},
			want:    WFCActivationWebView,
			webFlow: true,
		},
		{
			name: "in progress when terms are pending",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCInProgress,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionProvisioned,
			},
			want:       WFCActivationInProgress,
			inProgress: true,
		},
		{
			name: "in progress when address is pending",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressInProgress,
				ProvStatus:        WFCProvisionProvisioned,
			},
			want:       WFCActivationInProgress,
			inProgress: true,
		},
		{
			name: "in progress when provisioning is pending",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionInProgress,
			},
			want:       WFCActivationInProgress,
			inProgress: true,
		},
		{
			name: "not provisioned is in progress",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementDisabled,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionNotProvisioned,
			},
			want:       WFCActivationInProgress,
			inProgress: true,
		},
		{
			name: "entitlement provisioning is in progress",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementProvisioning,
				TCStatus:          WFCTCAvailable,
				AddrStatus:        WFCAddressAvailable,
				ProvStatus:        WFCProvisionProvisioned,
			},
			want:       WFCActivationInProgress,
			inProgress: true,
		},
		{
			name: "entitled can be activated",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			},
			want:     WFCActivationAvailable,
			entitled: true,
		},
		{
			name: "incompatible is unavailable",
			status: WFCStatus{
				EntitlementStatus: WFCEntitlementIncompatible,
				TCStatus:          WFCTCNotAvailable,
				AddrStatus:        WFCAddressNotAvailable,
				ProvStatus:        WFCProvisionNotProvisioned,
			},
			want: WFCActivationUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.ActivationMode(); got != tt.want {
				t.Errorf("ActivationMode() = %d, want %d", got, tt.want)
			}
			if got := tt.status.Entitled(); got != tt.entitled {
				t.Errorf("Entitled() = %t, want %t", got, tt.entitled)
			}
			if got := tt.status.NeedsUserWebFlow(); got != tt.webFlow {
				t.Errorf("NeedsUserWebFlow() = %t, want %t", got, tt.webFlow)
			}
			if got := tt.status.InProgress(); got != tt.inProgress {
				t.Errorf("InProgress() = %t, want %t", got, tt.inProgress)
			}
		})
	}
}

func TestWFCConfigShouldShowWebFlow(t *testing.T) {
	status := WFCStatus{
		EntitlementStatus: WFCEntitlementDisabled,
		TCStatus:          WFCTCNotAvailable,
		AddrStatus:        WFCAddressNotAvailable,
		ProvStatus:        WFCProvisionNotProvisioned,
	}
	tests := []struct {
		name string
		cfg  WFCConfig
		want bool
	}{
		{name: "shown when carrier allows webview", cfg: WFCConfig{ShowVoWiFiWebview: true}, want: true},
		{name: "hidden when carrier disables webview", cfg: WFCConfig{}, want: false},
		{name: "hidden when activation is skipped", cfg: WFCConfig{ShowVoWiFiWebview: true, SkipActivation: true}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ShouldShowWebFlow(status); got != tt.want {
				t.Errorf("ShouldShowWebFlow() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWFCAuthTokenValidity(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "empty uses default", want: 3600},
		{name: "zero uses default", value: "0", want: 3600},
		{name: "positive", value: "7200", want: 7200},
		{name: "invalid", value: "one hour", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wfcAuthTokenValidity(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wfcAuthTokenValidity(%q) error = %v, wantErr %t", tt.value, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("wfcAuthTokenValidity(%q) = %d, want %d", tt.value, got, tt.want)
			}
			if !tt.wantErr {
				return
			}
			if _, ok := errors.AsType[*strconv.NumError](err); !ok {
				t.Errorf("wfcAuthTokenValidity(%q) error = %T, want *strconv.NumError in chain", tt.value, err)
			}
			if !strings.Contains(err.Error(), "WFC authentication token validity") {
				t.Errorf("wfcAuthTokenValidity(%q) error = %q, want field context", tt.value, err)
			}
		})
	}
}

func TestCheckWFCEntitlementHandlesEmptyResponse(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("default_enabled_%v", enabled), func(t *testing.T) {
			_, err := CheckWFCEntitlement(context.Background(), Config{
				HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) { return response(200, ""), nil }),
			}, WFCEntitlementRequest{
				Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Config:   WFCConfig{Server: "https://example.test", DefaultServiceEntitlementEnabled: enabled},
			})
			if err == nil {
				t.Fatal("empty response succeeded")
			}
		})
	}
}

func TestCheckWFCEntitlementKeepsZeroStatusResponse(t *testing.T) {
	got, err := CheckWFCEntitlement(
		context.Background(),
		Config{
			HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="0"/><parm name="TC_Status" value="0"/><parm name="AddrStatus" value="0"/><parm name="ProvStatus" value="0"/></characteristic></wap-provisioningdoc>`), nil
			}),
			Source: Endpoint{Channel: fakeChannel{}},
		},
		WFCEntitlementRequest{
			Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Device:   Device{IMEI: "111111111111111", Vendor: "Google", Model: "Pixel 8 Pro", SoftwareVersion: "15"},
			Config: WFCConfig{
				Server:                           "https://example.test/ts43",
				DefaultServiceEntitlementEnabled: true,
			},
		},
	)
	if err != nil {
		t.Fatalf("CheckWFCEntitlement() error = %v", err)
	}
	want := WFCStatus{
		EntitlementStatus: WFCEntitlementDisabled,
		TCStatus:          WFCTCNotAvailable,
		AddrStatus:        WFCAddressNotAvailable,
		ProvStatus:        WFCProvisionNotProvisioned,
	}
	if got.Status != want {
		t.Errorf("CheckWFCEntitlement().Status = %+v, want %+v", got.Status, want)
	}
}

func TestCheckWFCEntitlementPackageHelper(t *testing.T) {
	result, err := CheckWFCEntitlement(
		context.Background(),
		Config{
			HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				if got := req.URL.Query().Get("app"); got != AppVoWiFi {
					t.Errorf("app = %q, want %q", got, AppVoWiFi)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`), nil
			}),
			Source: Endpoint{Channel: fakeChannel{}},
		},
		WFCEntitlementRequest{
			Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Device:   Device{IMEI: "111111111111111", Vendor: "Google", Model: "Pixel 8 Pro", SoftwareVersion: "15"},
			Config:   WFCConfig{Server: "https://example.test/ts43", App: AppVoWiFi},
		},
	)
	if err != nil {
		t.Fatalf("CheckWFCEntitlement() error = %v", err)
	}
	if !result.Status.Entitled() {
		t.Errorf("Entitled() = false, status = %+v", result.Status)
	}
}

func TestCheckWFCEntitlementSendsVoWiFiApp(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			if got := query.Get("app"); got != AppVoWiFi {
				t.Errorf("app = %q, want %q", got, AppVoWiFi)
			}
			if got := query.Get("entitlement_version"); got != "8.0" {
				t.Errorf("entitlement_version = %q, want 8.0", got)
			}
			wantAccept := EAPRelayContentType + ", " + WFCResponseAccept
			if got := req.Header.Get("Accept"); got != wantAccept {
				t.Errorf("Accept = %q, want %q", got, wantAccept)
			}
			if got := req.Header.Get("User-Agent"); got == "" || !strings.Contains(got, DefaultWFCClientTS43) {
				t.Errorf("User-Agent = %q, want WFC client", got)
			}
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Source: Endpoint{Channel: fakeChannel{}},
		Logger: slog.Default(),
	})

	result, err := client.CheckWFCEntitlement(
		context.Background(),
		WFCEntitlementRequest{
			Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Device:   Device{IMEI: "111111111111111", Vendor: "Google", Model: "Pixel 8 Pro", SoftwareVersion: "15"},
			Config:   WFCConfig{Server: "https://example.test/ts43", Version: "8.0"},
		},
	)
	if err != nil {
		t.Fatalf("CheckWFCEntitlement() error = %v", err)
	}
	if !result.Status.Entitled() {
		t.Errorf("Entitled() = false, status = %+v", result.Status)
	}
}

func TestCheckWFCEntitlementUsesCachedToken(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			if got := query.Get("IMSI"); got != "310260123456789" {
				t.Errorf("IMSI = %q, want source IMSI", got)
			}
			if got := query.Get("token"); got != "cached-token" {
				t.Errorf("token = %q, want cached-token", got)
			}
			if got := query.Get("EAP_ID"); got != "" {
				t.Errorf("EAP_ID = %q, want empty", got)
			}
			if got := req.Header.Get("Cookie"); got != "sid=old" {
				t.Errorf("Cookie = %q, want sid=old", got)
			}
			return responseWithCookie(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="fresh-token"/><parm name="validity" value="3600"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`, "sid=new"), nil
		}),
		Source: Endpoint{Channel: fakeChannel{}},
		Logger: slog.Default(),
	})

	got, err := client.CheckWFCEntitlement(
		context.Background(),
		WFCEntitlementRequest{
			Identity:  Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Device:    Device{IMEI: "111111111111111"},
			Config:    WFCConfig{Server: "https://example.test/ts43"},
			AuthToken: AuthToken{Value: "cached-token", Cookies: []string{"sid=old"}},
		},
	)
	if err != nil {
		t.Fatalf("CheckWFCEntitlement() error = %v", err)
	}
	if !got.Status.Entitled() {
		t.Errorf("Status.Entitled() = false, status = %+v", got.Status)
	}
	if got.AuthToken.Value != "fresh-token" {
		t.Errorf("AuthToken.Value = %q, want fresh-token", got.AuthToken.Value)
	}
	if strings.Join(got.AuthToken.Cookies, ",") != "sid=new" {
		t.Errorf("AuthToken.Cookies = %q, want sid=new", strings.Join(got.AuthToken.Cookies, ","))
	}
	if got.AuthToken.ExpiresAt.IsZero() {
		t.Error("AuthToken.ExpiresAt is zero")
	}
}

func TestCheckWFCEntitlementFallsBackFromRejectedCachedToken(t *testing.T) {
	var requests []string
	client := newClient(Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch {
			case query.Get("token") == "expired-token":
				requests = append(requests, "cached")
				return response(http.StatusNetworkAuthenticationRequired, ""), nil
			case query.Get("EAP_ID") != "":
				requests = append(requests, "eap")
				return response(200, eapRelayBody(t, 7)), nil
			case req.Method == http.MethodPost:
				requests = append(requests, "relay")
				if got := req.Header.Get("Accept"); got != WFCResponseAccept {
					t.Errorf("relay Accept = %q, want %q", got, WFCResponseAccept)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="fresh-token"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="EntitlementStatus" value="1"/><parm name="TC_Status" value="2"/><parm name="AddrStatus" value="2"/><parm name="ProvStatus" value="2"/></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected request: method=%s query=%s", req.Method, req.URL.RawQuery)
				return response(500, ""), nil
			}
		}),
		Source: Endpoint{Channel: akaChannel{}},
		Logger: slog.Default(),
	})

	got, err := client.CheckWFCEntitlement(
		context.Background(),
		WFCEntitlementRequest{
			Identity:  Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Device:    Device{IMEI: "111111111111111"},
			Config:    WFCConfig{Server: "https://example.test/ts43"},
			AuthToken: AuthToken{Value: "expired-token"},
		},
	)
	if err != nil {
		t.Fatalf("CheckWFCEntitlement() error = %v", err)
	}
	if !got.Status.Entitled() {
		t.Errorf("Status.Entitled() = false, status = %+v", got.Status)
	}
	if got.AuthToken.Value != "fresh-token" {
		t.Errorf("AuthToken.Value = %q, want fresh-token", got.AuthToken.Value)
	}
	if strings.Join(requests, ",") != "cached,eap,relay" {
		t.Errorf("requests = %q, want cached,eap,relay", strings.Join(requests, ","))
	}
}
