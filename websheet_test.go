package ts43

import (
	"fmt"
	"net/http"
	"testing"
)

func TestWebsheetWaitsForConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name  string
		event WebsheetCallbackEvent
	}{{"finish flow", WebsheetEventFinishFlow}, {"check status", WebsheetEventCheckProfileServiceStatus}} {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{Entitlement: Entitlement{Server: "https://example.test"}, HTTPClient: &queueDoer{bodies: []string{`{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":0}}}`}}})
			result := &Result{State: StatePendingWebsheet, session: &transferSession{offer: &transferOffer{SourceIdentity: Identity{MCC: "450", MNC: "05", IMSI: "450051234567890"}, AuthToken: AuthToken{Value: "auth"}}}}
			got, err := client.Continue(t.Context(), result, ContinueRequest{Websheet: &WebsheetResult{Event: tt.event}})
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StatePendingConfiguration {
				t.Fatalf("state=%v", got.State)
			}
			if _, ok := got.Event.(ConfigurationPendingEvent); !ok {
				t.Fatalf("event=%T", got.Event)
			}
			if _, err := client.CompleteActivation(t.Context(), got, ActivationResult{}); err == nil {
				t.Fatal("activated before download configuration")
			}
		})
	}
}

func TestGoogleTypeTwoFieldsForEachPhase(t *testing.T) {
	for _, post := range []bool{false, true} {
		t.Run(fmt.Sprint(post), func(t *testing.T) {
			offer := alignmentOffer()
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", TransferType: TransferTypeTemporaryToken, UsePost: post, SetTargetTerminalFields: true}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				fields := alignmentRequestFields(t, req)
				op := Operation(fields.Get("operation"))
				wantTerminal := offer.TargetDevice.IMEI
				if op == OperationCheck || op == OperationTempToken {
					wantTerminal = offer.SourceDevice.IMEI
				}
				if fields.Get("terminal_id") != wantTerminal {
					t.Errorf("%s terminal_id=%q, want %q", op, fields.Get("terminal_id"), wantTerminal)
				}
				switch op {
				case OperationCheck, OperationTempToken:
					for key := range fields {
						if len(key) >= 16 && key[:16] == "target_terminal_" {
							t.Errorf("%s contains target field %s", op, key)
						}
					}
				case OperationManage, OperationAcquireConf:
					if fields.Get("terminal_eid") != offer.TargetDevice.EID {
						t.Errorf("%s terminal EID is not the target", op)
					}
					if fields.Get("old_terminal_entitlement_protocol") != "1" {
						t.Errorf("%s dropped protocol metadata", op)
					}
				}
				switch op {
				case OperationCheck:
					return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
				case OperationTempToken:
					return response(200, `{"APPLICATION":{"OperationResult":1,"TemporaryToken":"temp","TemporaryTokenExpiry":"2030-01-01T00:00:00Z","OperationTargets":"ManageSubscription,AcquireConfiguration"}}`), nil
				case OperationManage:
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				default:
					return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
				}
			})})
			if _, err := client.checkEligibility(t.Context(), nil, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, offer.AuthToken); err != nil {
				t.Fatal(err)
			}
			tokens, err := client.acquireTemporaryToken(t.Context(), nil, offer.SourceIdentity, offer.SourceDevice, offer.AuthToken)
			if err != nil {
				t.Fatal(err)
			}
			credential := temporaryCredential(tokens.Temporary)
			if _, err := client.manageSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, UserInputResult{Button: MessageButtonUnknown}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.acquireConfiguration(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWebsheetRemainsUsableAcrossDownloadAndActivation(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, callback := range []WebsheetResult{
			{Event: WebsheetEventProfileReadyWithActivationCode, ActivationCode: "LPA:1$smdp.example$matching", ICCID: "8920000000000000000"},
			{Event: WebsheetEventProfileReadyWithDefaultSMDP, SMDPFQDN: "smdp.example", ICCID: "0"},
		} {
			t.Run(fmt.Sprintf("post_%v/callback_%d", post, callback.Event), func(t *testing.T) {
				manages, queries, authentications := 0, 0, 0
				client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					fields := alignmentRequestFields(t, req)
					switch Operation(fields.Get("operation")) {
					case "":
						authentications++
						return response(200, `{"TOKEN":{"token":"new-auth","Validity":3600}}`), nil
					case OperationCheck:
						return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
					case OperationManage:
						manages++
						if manages == 1 {
							return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":1,"SubscriptionServiceURL":"https://carrier.example/portal"}}`), nil
						}
						if manages > 2 || fields.Get("operation_type") != "4" {
							t.Fatal("subscription update submitted again")
						}
						return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
					case OperationAcquireConf:
						queries++
						if queries == 1 {
							return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":0}}}`), nil
						}
						return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
					default:
						t.Fatal("unexpected operation")
						return nil, nil
					}
				})})
				result, err := client.activate(t.Context(), alignmentOffer(), UserInputResult{Button: MessageButtonUnknown})
				if err != nil {
					t.Fatal(err)
				}
				apply := func(cb WebsheetResult) {
					t.Helper()
					result, err = client.Continue(t.Context(), result, ContinueRequest{Websheet: &cb})
					if err != nil {
						t.Fatal(err)
					}
				}
				apply(WebsheetResult{Event: WebsheetEventDeleteToken})
				apply(WebsheetResult{Event: WebsheetEventCheckProfileServiceStatus})
				if result.State != StatePendingConfiguration {
					t.Fatalf("check state=%v", result.State)
				}
				apply(callback)
				if result.State != StatePendingDownload {
					t.Fatalf("download state=%v", result.State)
				}
				if callback.Event == WebsheetEventProfileReadyWithDefaultSMDP && (result.DownloadConfig.SMDPFQDN != "smdp.example" || result.DownloadConfig.ProfileICCID != "" || result.DownloadConfig.MatchingID != "") {
					t.Fatalf("default download=%+v", result.DownloadConfig)
				}
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "8920000000000000000"})
				if err != nil || result.State != StateDone {
					t.Fatalf("activation state=%v error=%v", result.State, err)
				}
				apply(callback)
				if result.State != StateDone || manages != 2 || queries != 2 {
					t.Fatal("duplicate callback restarted installation")
				}
				apply(WebsheetResult{Event: WebsheetEventFinishFlow})
				if result.State != StateDone || manages != 2 || queries != 3 || authentications != 1 {
					t.Fatalf("state=%v manages=%d queries=%d auth=%d", result.State, manages, queries, authentications)
				}
			})
		}
	}
}

func TestInvalidWebsheetSelectionPreservesPendingFlow(t *testing.T) {
	client := newClient(Config{})
	result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: alignmentOffer(), websheetOpen: true}}
	result, err := client.Continue(t.Context(), result, ContinueRequest{Websheet: &WebsheetResult{Event: WebsheetEventSelectionCompleted}})
	if err == nil {
		t.Fatal("empty selection was accepted")
	}
	result, err = client.Continue(t.Context(), result, ContinueRequest{Websheet: &WebsheetResult{Event: WebsheetEventProfileReadyWithDefaultSMDP, SMDPFQDN: "smdp.example"}})
	if err != nil || result.State != StatePendingDownload {
		t.Fatalf("state=%v err=%v", result.State, err)
	}
}

func TestWebsheetResultUnmarshalText(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		wantEvent      WebsheetCallbackEvent
		wantActivation string
		wantSMDP       string
		wantICCID      string
		wantIMEI       string
		wantNext       Operation
	}{
		{
			name:           "activation JavaScript callback",
			text:           `ODSAServiceFlow.profileReadyWithActivationCode("LPA:1$smdp.example$matching","8900000000000000000","123456789012345")`,
			wantEvent:      WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
			wantIMEI:       "123456789012345",
		},
		{
			name:      "default smdp JSON callback",
			text:      `{"event":"profileReadyWithDefaultSmdp","defaultSmdpAddress":"smdp.example","iccid":"8900000000000000000","imei":"123456789012345"}`,
			wantEvent: WebsheetEventProfileReadyWithDefaultSMDP,
			wantSMDP:  "smdp.example",
			wantICCID: "8900000000000000000",
			wantIMEI:  "123456789012345",
		},
		{
			name:      "prefixed adapter JSON callback",
			text:      `TS43_ODSA_CALLBACK {"event":"finishFlow","nextAction":"AcquireConfiguration","source":"odsa"}`,
			wantEvent: WebsheetEventFinishFlow,
			wantNext:  OperationAcquireConf,
		},
		{
			name:      "VoWiFi entitlement changed JSON callback",
			text:      `TS43_VOWIFI_CALLBACK {"event":"entitlementChanged","source":"vowifi"}`,
			wantEvent: WebsheetEventVoWiFiEntitlementChanged,
		},
		{
			name:      "VoWiFi dismiss JavaScript callback",
			text:      `VoWiFiWebServiceFlow.dismissFlow()`,
			wantEvent: WebsheetEventVoWiFiDismissFlow,
		},
		{
			name:           "callback URL activation",
			text:           "https://carrier.test/_callback/esim?activationCode=LPA%3A1%24smdp.example%24matching&iccid=8900000000000000000",
			wantEvent:      WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got WebsheetResult
			if err := got.UnmarshalText([]byte(tt.text)); err != nil {
				t.Fatalf("UnmarshalText error = %v", err)
			}
			if got.Event != tt.wantEvent {
				t.Errorf("Event = %d, want %d", got.Event, tt.wantEvent)
			}
			if got.ActivationCode != tt.wantActivation {
				t.Errorf("ActivationCode = %q, want %q", got.ActivationCode, tt.wantActivation)
			}
			if got.SMDPFQDN != tt.wantSMDP {
				t.Errorf("SMDPFQDN = %q, want %q", got.SMDPFQDN, tt.wantSMDP)
			}
			if got.ICCID != tt.wantICCID {
				t.Errorf("ICCID = %q, want %q", got.ICCID, tt.wantICCID)
			}
			if got.IMEI != tt.wantIMEI {
				t.Errorf("IMEI = %q, want %q", got.IMEI, tt.wantIMEI)
			}
			if got.NextAction != tt.wantNext {
				t.Errorf("NextAction = %q, want %q", got.NextAction, tt.wantNext)
			}
		})
	}
}
