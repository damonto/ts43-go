package ts43

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

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

func TestRegressionSamsungOperation(t *testing.T) {
	tests := []struct {
		name     string
		transfer TransferType
	}{
		{"AKA D2D", TransferTypeTS43V8},
		{"temporary-token D2D", TransferTypeTemporaryToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ent := Entitlement{Channel: EntitlementChannelSamsung, TransferType: tt.transfer}
			op := samsungManageSubscriptionOperation(ent, ManageSubscriptionRequest{Source: Device{IMEI: "source", EID: "source-eid"}, Target: Device{IMEI: "target", EID: "target-eid"}, UserInput: UserInputResult{Button: MessageButtonUnknown}})
			if op.OperationType != OperationTypeTransferSubscription {
				t.Errorf("operation_type = %s, want 3", op.OperationType)
			}
		})
	}
}

func TestRegressionSamsungEID(t *testing.T) {
	tests := []struct {
		name             string
		ent              Entitlement
		terminal, target string
	}{
		{"normal pSIM conversion", Entitlement{Channel: EntitlementChannelSamsung, SourceSIMType: SIMTypePSIM, TransferType: TransferTypeOldTerminalIDs}, "", "target-eid"},
		{"source AKA transfer", Entitlement{Channel: EntitlementChannelSamsung, TransferType: TransferTypeTS43V8}, "source-eid", "target-eid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := samsungManageSubscriptionOperation(tt.ent, ManageSubscriptionRequest{Source: Device{EID: "source-eid"}, Target: Device{EID: "target-eid"}, UserInput: UserInputResult{Button: MessageButtonUnknown}})
			if op.TerminalEID != tt.terminal || op.TargetTerminalEID != tt.target {
				t.Errorf("terminal_eid=%q target_terminal_eid=%q; want %q, %q", op.TerminalEID, op.TargetTerminalEID, tt.terminal, tt.target)
			}
		})
	}
}

func TestRegressionSamsungExtraFields(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
	}{{"KT eligibility", OperationCheck}, {"KT manage", OperationManage}, {"KT acquire", OperationAcquireConf}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ent, err := privateEntitlementConfigs(t).DiscoverForChannel(Identity{MCC: "450", MNC: "02"}, SIMTypeESIM, EntitlementChannelSamsung)
			if err != nil {
				t.Fatal(err)
			}
			target := Device{IMEI: "target", Model: "target-model", SerialNumber: "target-sn", IMEIs: []string{"imei1", "imei2"}}
			var op ODSAOperation
			switch tt.operation {
			case OperationCheck:
				op = checkEligibilityOperation(ent, Device{}, target)
			case OperationManage:
				op = manageSubscriptionOperation(ent, ManageSubscriptionRequest{Target: target})
			case OperationAcquireConf:
				op = acquireConfigurationOperation(ent, Device{}, target)
			}
			if op.TargetTerminalSN != target.SerialNumber || op.TargetTerminalModel != target.Model || len(op.TargetTerminalIMEIs) != 2 {
				t.Errorf("extra fields lost with SetTargetTerminalFields=%v: %+v", ent.SetTargetTerminalFields, op)
			}
		})
	}
}

func TestRegressionGoogleTransferBranches(t *testing.T) {
	tests := []struct {
		name, body string
		wantCalls  int
		wantState  State
		invalid    bool
	}{
		{"invalid message 104", `{"APPLICATION":{"OperationResult":104,"GeneralErrorText":"invalid input"}}`, 1, StatePendingUserInput, true},
		{"delayed without SMDS", `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":4}}`, 2, StatePendingDownload, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doer := &queueDoer{bodies: []string{tt.body, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`}}
			c := newClient(Config{HTTPClient: doer, Entitlement: Entitlement{Server: "https://example.test", Channel: EntitlementChannelGoogle, TransferType: TransferTypeTS43V8}})
			result, err := c.activate(context.Background(), &transferOffer{SourceIdentity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"}, AuthToken: AuthToken{Value: "audit"}}, UserInputResult{Button: MessageButtonUnknown})
			if tt.invalid {
				if denied, ok := errors.AsType[*CarrierDeniedError](err); !ok || denied.Result.GeneralErrorText != "invalid input" {
					t.Fatalf("104 error = %v", err)
				}
				if result.Event != nil {
					t.Errorf("invalid input became event %T", result.Event)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tt.wantState || doer.calls != tt.wantCalls {
				t.Errorf("state=%d calls=%d, want state=%d calls=%d", result.State, doer.calls, tt.wantState, tt.wantCalls)
			}
		})
	}
}

func TestRegressionGoogleAppName(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
	}{{"Verizon", Identity{MCC: "310", MNC: "012"}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ent, err := privateEntitlementConfigs(t).DiscoverForChannel(tt.identity, SIMTypeESIM, EntitlementChannelGoogle)
			if err != nil {
				t.Fatal(err)
			}
			c := newClient(Config{Entitlement: ent})
			defer c.CloseIdleConnections()
			if got := c.baseRequest(Device{}, "audit", "").AppName; got != "" {
				t.Errorf("configured appName=%q became request app_name=%q", ent.AppName, got)
			}
		})
	}
}

func TestRegressionSamsungUserAgent(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
	}{{"KT", Identity{MCC: "450", MNC: "02"}}, {"SKT", Identity{MCC: "450", MNC: "05"}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ent, err := privateEntitlementConfigs(t).DiscoverForChannel(tt.identity, SIMTypeESIM, EntitlementChannelSamsung)
			if err != nil {
				t.Fatal(err)
			}
			got := userAgent(ent, ServiceEntitlementRequest{TerminalModel: "SM-S938B", TerminalSoftware: "16"}, SamsungODSAClientTS43)
			if got != "SM-S938B Android16" {
				t.Errorf("User-Agent=%q, want legacy model Android16 form", got)
			}
		})
	}
}

func TestRegressionGoogleCarrierMatching(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
		server   string
	}{
		{"GID prefix", Identity{MCC: "204", MNC: "04", GID1: "5AFF"}, "https://aes.mnc780.mcc302.pub.3gppnetwork.org"},
		{"GID outranks SPN", Identity{MCC: "204", MNC: "04", GID1: "5A", SPN: "U.S. Cellular"}, "https://aes.mnc780.mcc302.pub.3gppnetwork.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateEntitlementConfigs(t).DiscoverForChannel(tt.identity, SIMTypeESIM, EntitlementChannelGoogle)
			if err != nil {
				t.Fatalf("discovery error = %v", err)
			}
			if got.Server != tt.server {
				t.Errorf("server=%q want %q", got.Server, tt.server)
			}
		})
	}
}

func TestRegressionPrefixedText(t *testing.T) {
	tests := []struct{ name, value, want string }{{"plain regex", "test", "test"}, {"marked text", "encodedValue=dGVzdA==", "test"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodedPrefixedField(tt.value); got != tt.want {
				t.Errorf("decoded=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestRegressionSamsungPollingWithoutInterval(t *testing.T) {
	tests := []struct {
		name string
		ent  Entitlement
	}{{"Samsung missing interval", Entitlement{Channel: EntitlementChannelSamsung}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(Config{Entitlement: tt.ent})
			defer c.CloseIdleConnections()
			if interval, ok := c.pollInterval(&ODSAResponse{ServiceStatus: ServiceStatusActivating}); ok {
				t.Errorf("missing server interval starts polling every %v", interval)
			}
		})
	}
}

func TestRegressionTimeout(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  Config
		want time.Duration
	}{
		{"discovered timeout", Config{}, time.Minute},
		{"explicit timeout", Config{Entitlement: Entitlement{Timeout: 30 * time.Second}}, 30 * time.Second},
		{"custom HTTP client", Config{HTTPClient: &http.Client{Timeout: 7 * time.Second}}, 7 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(tt.cfg)
			t.Cleanup(c.CloseIdleConnections)
			c.applyDiscoveredEntitlement(Entitlement{Server: "https://example.test", Channel: EntitlementChannelSamsung, Timeout: time.Minute})
			if got := c.runtime.httpClient.(*http.Client).Timeout; got != tt.want {
				t.Errorf("timeout = %v, want %v", got, tt.want)
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

func TestRegressionAfterSourceDeletion(t *testing.T) {
	tests := []struct {
		name     string
		transfer TransferType
	}{
		{"temporary token", TransferTypeTemporaryToken},
		{"old terminal", TransferTypeOldTerminalIDs},
		{"temporary token old terminal", TransferTypeTemporaryTokenOldTerminalIDs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			doer := fakeDoer(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Query().Get("operation"))
				if len(calls) == 1 {
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":6}}`), nil
				}
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
			})
			c := newClient(Config{HTTPClient: doer, Entitlement: Entitlement{Server: "https://example.test", TransferType: tt.transfer, Channel: EntitlementChannelGoogle}})
			r, err := c.activate(context.Background(), &transferOffer{SourceIdentity: Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}, AuthToken: AuthToken{Value: "audit"}, TemporaryToken: TemporaryToken{Value: "temp"}}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Continue(context.Background(), r, ContinueRequest{SourceProfileDeleted: true})
			if len(calls) != 2 || calls[1] != OperationAcquireConf.String() {
				t.Errorf("requests after deletion=%v; want [ManageSubscription AcquireConfiguration]; error=%v", calls, err)
			}
		})
	}
}
