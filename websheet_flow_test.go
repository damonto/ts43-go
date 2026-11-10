package ts43

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func alignmentRequestFields(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	if req.Method == http.MethodGet {
		return req.URL.Query()
	}
	var body map[string]string
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	fields := make(url.Values)
	for key, value := range body {
		fields.Set(key, value)
	}
	return fields
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
			if _, err := client.checkEligibility(t.Context(), nil, offer.SourceIdentity, offer.SourceDevice, offer.AuthToken, offer.TargetDevice); err != nil {
				t.Fatal(err)
			}
			tokens, err := client.acquireTemporaryToken(t.Context(), nil, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, offer.AuthToken)
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
				client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", Channel: EntitlementChannelSamsung, UsePost: post}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
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
							return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":2}}}`), nil
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
