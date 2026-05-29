package ts43

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/damonto/ts43-go/sim"
)

func TestActivateDownloadAndCompleteActivation(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	doer := &queueDoer{bodies: []string{
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + base64.StdEncoding.EncodeToString([]byte(activationCode)) + `"/><parm name="ProfileIccid" value="8900000000000000000"/></characteristic></characteristic></wap-provisioningdoc>`,
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`,
	}}
	events := make(chan Event, 4)
	client, err := New(&Config{
		HTTPClient: doer,
		Entitlement: Entitlement{
			Server:       "https://example.test/ts43",
			PollTimeout:  time.Second,
			PollInterval: time.Millisecond,
		},
		Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Events: events,
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Fatalf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if event, ok := result.Event.(DownloadReadyEvent); !ok || event.Config.ActivationCode != activationCode {
		t.Fatalf("Event = %#v", result.Event)
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{ICCID: "8900000000000000000"})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if result.State != StateDone {
		t.Fatalf("State = %d, want %d", result.State, StateDone)
	}
	if _, ok := result.Event.(DoneEvent); !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if doer.calls != 2 {
		t.Fatalf("HTTP calls = %d, want 2", doer.calls)
	}
}

func TestClientUsesDefaultLogger(t *testing.T) {
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.cfg.Logger == nil {
		t.Fatal("Logger = nil, want default logger")
	}
	_, _, err = client.call(
		context.Background(),
		sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		client.baseRequest(Device{IMEI: "111111111111111"}, "", ""),
		ODSAOperation{},
	)
	if err != nil {
		t.Fatalf("call() error = %v", err)
	}
}

func TestNewAllowsMissingEntitlementServer(t *testing.T) {
	client, err := New(&Config{
		HTTPClient:  fakeDoer(func(*http.Request) (*http.Response, error) { return response(500, ""), nil }),
		Entitlement: Entitlement{},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.cfg.Entitlement.Server != "" {
		t.Fatalf("Server = %q, want empty before discovery", client.cfg.Entitlement.Server)
	}
}

func TestTransferDiscoversEntitlementServer(t *testing.T) {
	var calls []Operation
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "ts43.eas3.msg.t-mobile.com" {
				t.Fatalf("host = %q, want discovered T-Mobile server", req.URL.Host)
			}
			query := req.URL.Query()
			if got := query.Get("entitlement_version"); got != "9.0" {
				t.Fatalf("entitlement_version = %q, want 9.0", got)
			}
			op := Operation(query.Get("operation"))
			calls = append(calls, op)
			switch op {
			case "":
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("temporary_token"); got != "tmp" {
					t.Fatalf("temporary_token = %q, want tmp", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Transfer(context.Background())
	if err != nil {
		t.Fatalf("Transfer() error = %v", err)
	}
	if result.State != StateDone {
		t.Fatalf("State = %d, want %d", result.State, StateDone)
	}
	if client.cfg.Entitlement.Server != "https://ts43.eas3.msg.t-mobile.com/google/" {
		t.Fatalf("Server = %q, want discovered T-Mobile server", client.cfg.Entitlement.Server)
	}
	if got := operationList(calls); got != ",CheckEligibility,AcquireTemporaryToken,ManageSubscription" {
		t.Fatalf("calls = %s", got)
	}
}

func TestTransferKeepsExplicitEntitlementServer(t *testing.T) {
	var calls int
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Host != "example.test" {
				t.Fatalf("host = %q, want explicit server", req.URL.Host)
			}
			switch Operation(req.URL.Query().Get("operation")) {
			case "":
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", req.URL.Query().Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Source: Endpoint{
			Channel: identityChannel{identity: sim.Identity{ICCID: "8910000000000000000", IMSI: "208011234567890", MCC: "208", MNC: "01"}},
			Device:  Device{IMEI: "111111111111111"},
		},
		Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Transfer(context.Background())
	if err != nil {
		t.Fatalf("Transfer() error = %v", err)
	}
	if result.State != StateDone {
		t.Fatalf("State = %d, want %d", result.State, StateDone)
	}
	if calls != 3 {
		t.Fatalf("HTTP calls = %d, want 3", calls)
	}
}

func TestEAPRelayUsesLatestCookies(t *testing.T) {
	var calls int
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			switch calls {
			case 0:
				calls++
				return responseWithCookie(http.StatusOK, eapRelayBody(t, 1), "relay=one"), nil
			case 1:
				cookie, err := req.Cookie("relay")
				if err != nil || cookie.Value != "one" {
					t.Fatalf("relay call 1 cookie = %v, %v; want relay=one", cookie, err)
				}
				calls++
				return responseWithCookie(http.StatusOK, eapRelayBody(t, 2), "relay=two"), nil
			case 2:
				cookie, err := req.Cookie("relay")
				if err != nil || cookie.Value != "two" {
					t.Fatalf("relay call 2 cookie = %v, %v; want relay=two", cookie, err)
				}
				calls++
				return response(http.StatusOK, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected HTTP call %d", calls)
				return nil, nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Source:      Endpoint{Channel: akaChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, _, err = client.call(
		context.Background(),
		sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		client.baseRequest(Device{IMEI: "111111111111111"}, "", ""),
		ODSAOperation{},
	)
	if err != nil {
		t.Fatalf("call() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("HTTP calls = %d, want 3", calls)
	}
}

func TestActivateReturnsDownloadFromManageSubscription(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	doer := &queueDoer{bodies: []string{
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + base64.StdEncoding.EncodeToString([]byte(activationCode)) + `"/></characteristic></characteristic></wap-provisioningdoc>`,
	}}
	client, err := New(&Config{
		HTTPClient:  doer,
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Fatalf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if event, ok := result.Event.(DownloadReadyEvent); !ok || event.Config.ActivationCode != activationCode {
		t.Fatalf("Event = %#v", result.Event)
	}
	if doer.calls != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.calls)
	}
}

func TestContinueAfterSourceProfileDeletionUsesReturnedToken(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	tests := []struct {
		name         string
		transferType TransferType
		wantSecond   Operation
		wantOldProto bool
	}{
		{name: "source credential reusable activation code", transferType: TransferTypeReusableActivationCode, wantSecond: OperationAcquireConf, wantOldProto: true},
		{name: "old terminal ID type keeps protocol empty", transferType: TransferTypeOldTerminalIDs, wantSecond: OperationManage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					data, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatalf("ReadAll() error = %v", err)
					}
					body := string(data)
					calls++
					switch calls {
					case 1:
						if !strings.Contains(body, `"operation":"ManageSubscription"`) {
							t.Fatalf("first request body = %s, want ManageSubscription", body)
						}
						if !strings.Contains(body, `"token":"old-token"`) {
							t.Fatalf("first ManageSubscription body = %s, want old token", body)
						}
						if strings.Contains(body, `"old_terminal_entitlement_protocol"`) {
							t.Fatalf("first ManageSubscription body = %s, want no old terminal protocol", body)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-token"/><parm name="validity" value="3600"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="6"/></characteristic></wap-provisioningdoc>`), nil
					case 2:
						if !strings.Contains(body, `"operation":"`+tt.wantSecond.String()+`"`) {
							t.Fatalf("second request body = %s, want %s", body, tt.wantSecond)
						}
						if !strings.Contains(body, `"token":"new-token"`) {
							t.Fatalf("second request body = %s, want returned token", body)
						}
						hasOldProto := strings.Contains(body, `"old_terminal_entitlement_protocol":"1"`)
						if hasOldProto != tt.wantOldProto {
							t.Fatalf("second request body = %s, want old terminal protocol %v", body, tt.wantOldProto)
						}
						if tt.wantSecond == OperationAcquireConf {
							if strings.Contains(body, `"operation_type"`) {
								t.Fatalf("AcquireConfiguration body = %s, want no operation_type", body)
							}
							if !strings.Contains(body, `"terminal_id":"111111111111111"`) {
								t.Fatalf("AcquireConfiguration body = %s, want source terminal_id", body)
							}
							if !strings.Contains(body, `"target_terminal_id":"222222222222222"`) {
								t.Fatalf("AcquireConfiguration body = %s, want target_terminal_id", body)
							}
							if !strings.Contains(body, `"target_terminal_eid":"89049032000001000000000000000000"`) {
								t.Fatalf("AcquireConfiguration body = %s, want target_terminal_eid", body)
							}
							if strings.Contains(body, `"old_terminal_iccid"`) || strings.Contains(body, `"old_terminal_id"`) {
								t.Fatalf("AcquireConfiguration body = %s, want no old terminal identifiers", body)
							}
							return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="`+base64.StdEncoding.EncodeToString([]byte(activationCode))+`"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`), nil
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="`+base64.StdEncoding.EncodeToString([]byte(activationCode))+`"/></characteristic></characteristic></wap-provisioningdoc>`), nil
					default:
						t.Fatalf("unexpected HTTP call %d", calls)
						return response(500, ""), nil
					}
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					UsePost:      true,
					TransferType: tt.transferType,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := client.activate(context.Background(), &Offer{
				SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "old-token"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if result.State != StatePendingSourceProfileDeletion {
				t.Fatalf("State = %d, want %d", result.State, StatePendingSourceProfileDeletion)
			}

			result, err = client.Continue(context.Background(), result, ContinueRequest{SourceProfileDeleted: true})
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if result.State != StatePendingDownload {
				t.Fatalf("State = %d, want %d", result.State, StatePendingDownload)
			}
			if result.DownloadConfig.ActivationCode != activationCode {
				t.Fatalf("ActivationCode = %q, want %q", result.DownloadConfig.ActivationCode, activationCode)
			}
		})
	}
}

func TestTransferSourceCredentialReturnsDownload(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	var calls []Operation
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			op := Operation(query.Get("operation"))
			calls = append(calls, op)
			switch op {
			case "":
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				if got := query.Get("token"); got != "auth" {
					t.Fatalf("CheckEligibility token = %q, want auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("token"); got != "checked-auth" {
					t.Fatalf("ManageSubscription token = %q, want checked-auth", got)
				}
				if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Fatalf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
				}
				if got := query.Get("terminal_id"); got != "111111111111111" {
					t.Fatalf("terminal_id = %q, want source IMEI", got)
				}
				if got := query.Get("terminal_eid"); got != "" {
					t.Fatalf("terminal_eid = %q, want empty", got)
				}
				if got := query.Get("terminal_iccid"); got != "" {
					t.Fatalf("terminal_iccid = %q, want empty", got)
				}
				if got := query.Get("target_terminal_id"); got != "222222222222222" {
					t.Fatalf("target_terminal_id = %q, want target IMEI", got)
				}
				if got := query.Get("target_terminal_eid"); got != "89049032000001000000000000000000" {
					t.Fatalf("target_terminal_eid = %q, want target EID", got)
				}
				if got := query.Get("target_terminal_model"); got != "" {
					t.Fatalf("target_terminal_model = %q, want empty", got)
				}
				if got := query.Get("old_terminal_id"); got != "111111111111111" {
					t.Fatalf("old_terminal_id = %q, want source IMEI", got)
				}
				if got := query.Get("old_terminal_iccid"); got != "8910000000000000000" {
					t.Fatalf("old_terminal_iccid = %q, want source ICCID", got)
				}
				if got := query.Get("old_terminal_entitlement_protocol"); got != "" {
					t.Fatalf("old_terminal_entitlement_protocol = %q, want empty", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="`+base64.StdEncoding.EncodeToString([]byte(activationCode))+`"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", op)
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{
			Server:       "https://example.test/ts43",
			PollTimeout:  time.Second,
			TransferType: TransferTypeReusableActivationCode,
		},
		Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.Transfer(context.Background())
	if err != nil {
		t.Fatalf("Transfer() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Fatalf("State = %d, want %d", result.State, StatePendingDownload)
	}
	event, ok := result.Event.(DownloadReadyEvent)
	if !ok {
		t.Fatalf("Event = %#v, want DownloadReadyEvent", result.Event)
	}
	if event.Config.ActivationCode != activationCode {
		t.Fatalf("ActivationCode = %q, want %q", event.Config.ActivationCode, activationCode)
	}
	wantCalls := []Operation{"", OperationCheck, OperationManage}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{ICCID: "8900000000000000000"})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if result.State != StateDone {
		t.Fatalf("State after CompleteActivation = %d, want %d", result.State, StateDone)
	}
	if _, ok := result.Event.(DoneEvent); !ok {
		t.Fatalf("Event after CompleteActivation = %#v, want DoneEvent", result.Event)
	}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("calls after CompleteActivation = %v, want no extra calls after %v", calls, wantCalls)
	}
}

func TestActivateReturnsDoneFromManageSubscription(t *testing.T) {
	doer := &queueDoer{bodies: []string{
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`,
	}}
	client, err := New(&Config{
		HTTPClient:  doer,
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StateDone {
		t.Fatalf("State = %d, want %d", result.State, StateDone)
	}
	if doer.calls != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.calls)
	}
}

func TestAcquireTemporaryTokenRequiresTransferTargets(t *testing.T) {
	client := &Client{cfg: Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="OperationTargets" value="ManageSubscription"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	}}

	_, err := client.AcquireTemporaryToken(
		context.Background(),
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		Device{IMEI: "111111111111111"},
		Device{IMEI: "222222222222222"},
		AuthToken{Value: "auth"},
	)
	if err == nil {
		t.Fatal("AcquireTemporaryToken() error = nil, want operation target error")
	}
}

func TestAcquireTemporaryTokenRequiresExpiry(t *testing.T) {
	client := &Client{cfg: Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	}}

	_, err := client.AcquireTemporaryToken(
		context.Background(),
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		Device{IMEI: "111111111111111"},
		Device{IMEI: "222222222222222"},
		AuthToken{Value: "auth"},
	)
	if err == nil {
		t.Fatal("AcquireTemporaryToken() error = nil, want expiry error")
	}
}

func TestAuthenticateInitialEAPRequestUsesRelayAccept(t *testing.T) {
	client := &Client{cfg: Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			wantAccept := EAPRelayContentType + ", " + DefaultResponseAccept
			if got := req.Header.Get("Accept"); got != wantAccept {
				t.Fatalf("Accept = %q, want %q", got, wantAccept)
			}
			if got := req.URL.Query().Get("EAP_ID"); got == "" {
				t.Fatal("EAP_ID is empty")
			}
			return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	}}

	token, err := client.Authenticate(
		context.Background(),
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		Device{IMEI: "111111111111111"},
	)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if token.Value != "auth" {
		t.Fatalf("token = %q, want auth", token.Value)
	}
}

func TestTransferRefreshesAuthAroundEligibility(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		do          func(*testing.T, *http.Request) (*http.Response, error)
		wantAuth    string
		wantCalls   string
	}{
		{
			name:        "default transfer type uses authentication token directly",
			entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second},
			wantAuth:    "checked-auth",
			wantCalls:   "auth,check:auth,manage",
			do: func(t *testing.T, req *http.Request) (*http.Response, error) {
				query := req.URL.Query()
				switch Operation(query.Get("operation")) {
				case "":
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
				case OperationCheck:
					if got := query.Get("token"); got != "auth" {
						t.Fatalf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Fatalf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Fatalf("ManageSubscription temporary_token = %q, want empty", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				default:
					t.Fatalf("unexpected operation %q", query.Get("operation"))
					return response(500, ""), nil
				}
			},
		},
		{
			name:        "source credential fetch uses checked auth token",
			entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second, TransferType: TransferTypeReusableActivationCode},
			wantAuth:    "checked-auth",
			wantCalls:   "auth,check:auth,manage",
			do: func(t *testing.T, req *http.Request) (*http.Response, error) {
				query := req.URL.Query()
				switch Operation(query.Get("operation")) {
				case "":
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
				case OperationCheck:
					if got := query.Get("token"); got != "auth" {
						t.Fatalf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Fatalf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Fatalf("ManageSubscription temporary_token = %q, want empty", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				default:
					t.Fatalf("unexpected operation %q", query.Get("operation"))
					return response(500, ""), nil
				}
			},
		},
		{
			name: "source credential fetch ignores temporary token conversion flag",
			entitlement: Entitlement{
				Server:                 "https://example.test/ts43",
				PollTimeout:            time.Second,
				TransferType:           TransferTypeTS43V8,
				UseTempTokenConversion: true,
			},
			wantAuth:  "checked-auth",
			wantCalls: "auth,check:auth,manage",
			do: func(t *testing.T, req *http.Request) (*http.Response, error) {
				query := req.URL.Query()
				switch Operation(query.Get("operation")) {
				case "":
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
				case OperationCheck:
					if got := query.Get("token"); got != "auth" {
						t.Fatalf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Fatalf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Fatalf("ManageSubscription temporary_token = %q, want empty", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				default:
					t.Fatalf("unexpected operation %q", query.Get("operation"))
					return response(500, ""), nil
				}
			},
		},
		{
			name:        "uses token returned by check eligibility for temporary token request",
			entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
			wantAuth:    "temp-auth",
			wantCalls:   "auth,check:auth,temp:checked-auth,manage",
			do: func(t *testing.T, req *http.Request) (*http.Response, error) {
				query := req.URL.Query()
				switch Operation(query.Get("operation")) {
				case "":
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
				case OperationCheck:
					if got := query.Get("token"); got != "auth" {
						t.Fatalf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationTempToken:
					if got := query.Get("token"); got != "checked-auth" {
						t.Fatalf("AcquireTemporaryToken token = %q, want checked-auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="temp-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("temporary_token"); got != "tmp" {
						t.Fatalf("ManageSubscription temporary_token = %q, want tmp", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				default:
					t.Fatalf("unexpected operation %q", query.Get("operation"))
					return response(500, ""), nil
				}
			},
		},
		{
			name:        "re-authenticates when check eligibility returns 511",
			entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
			wantAuth:    "fresh-auth",
			wantCalls:   "auth,check:stale-auth,auth,check:fresh-auth,temp:fresh-auth,manage",
			do: func() func(*testing.T, *http.Request) (*http.Response, error) {
				authCalls := 0
				return func(t *testing.T, req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					switch Operation(query.Get("operation")) {
					case "":
						authCalls++
						token := "stale-auth"
						if authCalls == 2 {
							token = "fresh-auth"
						}
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="`+token+`"/></characteristic></wap-provisioningdoc>`), nil
					case OperationCheck:
						if query.Get("token") == "stale-auth" {
							return response(511, ""), nil
						}
						if got := query.Get("token"); got != "fresh-auth" {
							t.Fatalf("CheckEligibility retry token = %q, want fresh-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						if got := query.Get("token"); got != "fresh-auth" {
							t.Fatalf("AcquireTemporaryToken token = %q, want fresh-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
					case OperationManage:
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
					default:
						t.Fatalf("unexpected operation %q", query.Get("operation"))
						return response(500, ""), nil
					}
				}
			}(),
		},
		{
			name:        "retries complete flow when temporary token returns 511",
			entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
			wantAuth:    "fresh-auth",
			wantCalls:   "auth,check:stale-auth,temp:stale-auth,auth,check:fresh-auth,temp:fresh-auth,manage",
			do: func() func(*testing.T, *http.Request) (*http.Response, error) {
				authCalls := 0
				return func(t *testing.T, req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					switch Operation(query.Get("operation")) {
					case "":
						authCalls++
						token := "stale-auth"
						if authCalls == 2 {
							token = "fresh-auth"
						}
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="`+token+`"/></characteristic></wap-provisioningdoc>`), nil
					case OperationCheck:
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						if query.Get("token") == "stale-auth" {
							return response(511, ""), nil
						}
						if got := query.Get("token"); got != "fresh-auth" {
							t.Fatalf("AcquireTemporaryToken retry token = %q, want fresh-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
					case OperationManage:
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
					default:
						t.Fatalf("unexpected operation %q", query.Get("operation"))
						return response(500, ""), nil
					}
				}
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					switch Operation(query.Get("operation")) {
					case "":
						calls = append(calls, "auth")
					case OperationCheck:
						calls = append(calls, "check:"+query.Get("token"))
					case OperationTempToken:
						calls = append(calls, "temp:"+query.Get("token"))
					case OperationManage:
						calls = append(calls, "manage")
					}
					return tt.do(t, req)
				}),
				Entitlement: tt.entitlement,
				Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger:      slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := client.Transfer(context.Background())
			if err != nil {
				t.Fatalf("Transfer() error = %v", err)
			}
			if result.Offer.AuthToken.Value != tt.wantAuth {
				t.Fatalf("Offer.AuthToken = %q, want %q", result.Offer.AuthToken.Value, tt.wantAuth)
			}
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Fatalf("calls = %q, want %q", got, tt.wantCalls)
			}
		})
	}
}

func TestPostRelayMatchesGMSRequestShape(t *testing.T) {
	tests := []struct {
		name       string
		packet     string
		accept     string
		cookies    []*http.Cookie
		wantBody   string
		wantAccept string
		wantCookie string
	}{
		{
			name:       "relay packet JSON",
			packet:     "base64-eap-response",
			accept:     DefaultResponseAccept,
			cookies:    []*http.Cookie{{Name: "SESSION", Value: "abc"}},
			wantBody:   `{"eap-relay-packet":"base64-eap-response"}`,
			wantAccept: EAPRelayContentType + ", " + DefaultResponseAccept,
			wantCookie: "SESSION=abc",
		},
		{
			name:       "accept already includes relay packet JSON",
			packet:     "base64-eap-response",
			accept:     EAPRelayContentType + ", " + DefaultResponseAccept,
			cookies:    []*http.Cookie{{Name: "SESSION", Value: "abc"}},
			wantBody:   `{"eap-relay-packet":"base64-eap-response"}`,
			wantAccept: EAPRelayContentType + ", " + DefaultResponseAccept,
			wantCookie: "SESSION=abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodPost {
						t.Fatalf("method = %q, want POST", req.Method)
					}
					if got := req.URL.String(); got != "https://example.test/ts43" {
						t.Fatalf("URL = %q, want https://example.test/ts43", got)
					}
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatalf("read body: %v", err)
					}
					if got := string(body); got != tt.wantBody {
						t.Fatalf("body = %q, want %q", got, tt.wantBody)
					}
					if got := req.Header.Get("Content-Type"); got != EAPRelayContentType {
						t.Fatalf("Content-Type = %q, want %q", got, EAPRelayContentType)
					}
					if got := req.Header.Get("Accept"); got != tt.wantAccept {
						t.Fatalf("Accept = %q, want %q", got, tt.wantAccept)
					}
					if got := req.Header.Get("Cookie"); got != tt.wantCookie {
						t.Fatalf("Cookie = %q, want %q", got, tt.wantCookie)
					}
					if got := req.Header.Get("User-Agent"); !strings.Contains(got, DefaultODSAClientTS43) {
						t.Fatalf("User-Agent = %q, want TS.43 client marker", got)
					}
					return response(200, `<wap-provisioningdoc/>`), nil
				}),
				Entitlement: Entitlement{Server: "https://example.test/ts43"},
				Logger:      slog.Default(),
			}}

			_, _, err := client.postRelay(context.Background(), tt.packet, ServiceEntitlementRequest{
				TerminalID:       "111111111111111",
				TerminalVendor:   "Google",
				TerminalModel:    "Pixel 8 Pro",
				TerminalSoftware: "15/AP3A.240905.015",
				Accept:           tt.accept,
			}, tt.cookies)
			if err != nil {
				t.Fatalf("postRelay() error = %v", err)
			}
		})
	}
}

func TestActivateRefreshesTokenAfterManageHTTP511(t *testing.T) {
	var manageTokens []string
	var authFetched, eligibilityFetched, tempFetched bool
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case "":
				authFetched = true
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				eligibilityFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("CheckEligibility token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				tempFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("AcquireTemporaryToken token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="new-temp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Fatalf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
				}
				manageTokens = append(manageTokens, query.Get("temporary_token"))
				if query.Get("temporary_token") == "old-temp" {
					return response(511, ""), nil
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				t.Fatal("AcquireConfiguration was called")
				return response(500, ""), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		TemporaryToken: TemporaryToken{Value: "old-temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if !authFetched || !eligibilityFetched || !tempFetched {
		t.Fatalf("refresh auth=%v eligibility=%v temp=%v, want all true", authFetched, eligibilityFetched, tempFetched)
	}
	if got := strings.Join(manageTokens, ","); got != "old-temp,new-temp" {
		t.Fatalf("ManageSubscription tokens = %q, want old-temp,new-temp", got)
	}
	if result.State != StatePendingDelayedDownload {
		t.Fatalf("State = %d, want %d", result.State, StatePendingDelayedDownload)
	}
	if _, ok := result.Event.(DelayedDownloadEvent); !ok {
		t.Fatalf("Event = %#v, want DelayedDownloadEvent", result.Event)
	}
}

func TestPollConfigurationRetriesAfterHTTP511(t *testing.T) {
	var acquireTokens []string
	var authFetched, eligibilityFetched, tempFetched bool
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case "":
				authFetched = true
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				eligibilityFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("CheckEligibility token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				tempFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("AcquireTemporaryToken token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="new-temp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				t.Fatal("ManageSubscription was called")
				return response(500, ""), nil
			case OperationAcquireConf:
				acquireTokens = append(acquireTokens, query.Get("temporary_token"))
				if query.Get("temporary_token") == "old-temp" {
					return response(511, ""), nil
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileIccid" value="8900000000000000000"/><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, temporaryCredential(TemporaryToken{Value: "old-temp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if !authFetched || !eligibilityFetched || !tempFetched {
		t.Fatalf("refresh auth=%v eligibility=%v temp=%v, want all true", authFetched, eligibilityFetched, tempFetched)
	}
	if got := strings.Join(acquireTokens, ","); got != "old-temp,new-temp" {
		t.Fatalf("AcquireConfiguration tokens = %q, want old-temp,new-temp", got)
	}
	if resp.Download.SMDPFQDN != "smdp.example" {
		t.Fatalf("Download = %+v", resp.Download)
	}
}

func TestActivateTypeTwoDelayedDownloadWithTemporaryToken(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
	}{
		{
			name: "type two sends temporary token",
			entitlement: Entitlement{
				Server:                  "https://example.test/ts43",
				AutoRetry511Limit:       1,
				PollTimeout:             time.Second,
				TransferType:            TransferTypeTemporaryToken,
				SetTargetTerminalFields: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var manageTokens, acquireTokens []string
			var authFetched, eligibilityFetched bool
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					switch Operation(query.Get("operation")) {
					case "":
						authFetched = true
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
					case OperationCheck:
						eligibilityFetched = true
						if got := query.Get("token"); got != "new-auth" {
							t.Fatalf("CheckEligibility token = %q, want new-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						t.Fatal("AcquireTemporaryToken was called")
						return response(500, ""), nil
					case OperationManage:
						manageTokens = append(manageTokens, query.Get("temporary_token"))
						if got := query.Get("token"); got != "" {
							t.Fatalf("ManageSubscription token = %q, want empty", got)
						}
						if got := query.Get("temporary_token"); got != "old-temp" {
							t.Fatalf("ManageSubscription temporary_token = %q, want old-temp", got)
						}
						if got := query.Get("terminal_id"); got != "111111111111111" {
							t.Fatalf("ManageSubscription terminal_id = %q, want source IMEI", got)
						}
						if got := query.Get("terminal_eid"); got != "89049032000001000000000000000000" {
							t.Fatalf("ManageSubscription terminal_eid = %q, want target EID", got)
						}
						if got := query.Get("target_terminal_id"); got != "" {
							t.Fatalf("ManageSubscription target_terminal_id = %q, want empty", got)
						}
						if got := query.Get("old_terminal_id"); got != "111111111111111" {
							t.Fatalf("ManageSubscription old_terminal_id = %q, want source IMEI", got)
						}
						if got := query.Get("old_terminal_iccid"); got != "8910000000000000000" {
							t.Fatalf("ManageSubscription old_terminal_iccid = %q, want source ICCID", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
					case OperationAcquireConf:
						acquireTokens = append(acquireTokens, query.Get("temporary_token"))
						if got := query.Get("token"); got != "" {
							t.Fatalf("AcquireConfiguration token = %q, want empty", got)
						}
						if got := query.Get("temporary_token"); got != "old-temp" {
							t.Fatalf("AcquireConfiguration temporary_token = %q, want old-temp", got)
						}
						if got := query.Get("terminal_id"); got != "222222222222222" {
							t.Fatalf("AcquireConfiguration terminal_id = %q, want target IMEI", got)
						}
						if got := query.Get("terminal_eid"); got != "89049032000001000000000000000000" {
							t.Fatalf("AcquireConfiguration terminal_eid = %q, want target EID", got)
						}
						if got := query.Get("target_terminal_id"); got != "" {
							t.Fatalf("AcquireConfiguration target_terminal_id = %q, want empty", got)
						}
						if query.Get("token") == "old-auth" {
							return response(511, ""), nil
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileIccid" value="8900000000000000000"/><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`), nil
					default:
						t.Fatalf("unexpected operation %q", query.Get("operation"))
						return response(500, ""), nil
					}
				}),
				Entitlement: tt.entitlement,
				Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger:      slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			result, err := client.activate(context.Background(), &Offer{
				SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "old-auth"},
				TemporaryToken: TemporaryToken{Value: "old-temp"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if authFetched || eligibilityFetched {
				t.Fatalf("refresh auth=%v eligibility=%v, want no refresh", authFetched, eligibilityFetched)
			}
			if got := strings.Join(manageTokens, ","); got != "old-temp" {
				t.Fatalf("ManageSubscription tokens = %q, want old-temp", got)
			}
			if len(acquireTokens) != 0 {
				t.Fatalf("AcquireConfiguration tokens = %q, want none", strings.Join(acquireTokens, ","))
			}
			if result.State != StatePendingDelayedDownload {
				t.Fatalf("State = %d, want %d", result.State, StatePendingDelayedDownload)
			}
			if _, ok := result.Event.(DelayedDownloadEvent); !ok {
				t.Fatalf("Event = %#v, want DelayedDownloadEvent", result.Event)
			}
		})
	}
}

func TestActivateHandlesManageSubscriptionPendingResults(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantState State
		wantEvent func(Event) bool
	}{
		{
			name:      "websheet",
			body:      `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="1"/><parm name="SubscriptionServiceURL" value="https://example.test/websheet"/></characteristic></wap-provisioningdoc>`,
			wantState: StatePendingWebsheet,
			wantEvent: func(event Event) bool {
				got, ok := event.(WebsheetEvent)
				return ok && got.Websheet.URL == "https://example.test/websheet"
			},
		},
		{
			name:      "user input",
			body:      `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="8"/><characteristic type="MSG"><parm name="Message" value="Enter PIN"/></characteristic></characteristic></wap-provisioningdoc>`,
			wantState: StatePendingUserInput,
			wantEvent: func(event Event) bool {
				got, ok := event.(UserInputEvent)
				return ok && got.Message.Text == "Enter PIN"
			},
		},
		{
			name:      "source profile deletion",
			body:      `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="6"/></characteristic></wap-provisioningdoc>`,
			wantState: StatePendingSourceProfileDeletion,
			wantEvent: func(event Event) bool {
				got, ok := event.(SourceProfileDeletionEvent)
				return ok && got.ICCID == "8910000000000000000"
			},
		},
		{
			name:      "dismiss",
			body:      `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="5"/></characteristic></wap-provisioningdoc>`,
			wantState: StateDismissed,
			wantEvent: func(event Event) bool {
				got, ok := event.(DismissEvent)
				return ok && got.SubscriptionResult == SubscriptionResultDismiss
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: &queueDoer{bodies: []string{tt.body}},
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					PollTimeout:  time.Second,
					PollInterval: time.Millisecond,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			result, err := client.activate(context.Background(), &Offer{
				SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "tmp"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if result.State != tt.wantState {
				t.Fatalf("State = %d, want %d", result.State, tt.wantState)
			}
			if !tt.wantEvent(result.Event) {
				t.Fatalf("Event = %#v", result.Event)
			}
		})
	}
}

func TestContinueRequiresPendingRequestBodies(t *testing.T) {
	tests := []struct {
		name   string
		result *Result
		req    ContinueRequest
	}{
		{
			name: "nil result",
		},
		{
			name:   "missing user input",
			result: &Result{State: StatePendingUserInput, Offer: &Offer{}},
		},
		{
			name:   "missing websheet result",
			result: &Result{State: StatePendingWebsheet, Offer: &Offer{}},
		},
		{
			name:   "missing source profile deletion confirmation",
			result: &Result{State: StatePendingSourceProfileDeletion, Offer: &Offer{}},
		},
		{
			name:   "missing offer",
			result: &Result{State: StatePendingUserInput},
			req:    ContinueRequest{UserInput: &UserInputResult{Button: MessageButtonAccepted}},
		},
	}
	client := &Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Continue(context.Background(), tt.result, tt.req)
			if err == nil {
				t.Fatal("Continue() error is nil")
			}
		})
	}
}

func TestCompleteActivationRequiresResult(t *testing.T) {
	tests := []struct {
		name   string
		result *Result
	}{
		{name: "nil result"},
		{name: "missing offer", result: &Result{}},
	}
	client := &Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CompleteActivation(context.Background(), tt.result, ActivationResult{})
			if err == nil {
				t.Fatal("CompleteActivation() error is nil")
			}
		})
	}
}

func TestCompleteActivationReturnsDoneFromUpdateSubscription(t *testing.T) {
	var updateTokens []string
	var eligibilityFetched bool
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case "":
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				eligibilityFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("CheckEligibility token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				if got := query.Get("token"); got != "new-auth" {
					t.Fatalf("AcquireTemporaryToken token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="new-temp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("operation_type"); got != OperationTypeUpdateSubscription.String() {
					t.Fatalf("operation_type = %q, want %s", got, OperationTypeUpdateSubscription)
				}
				if got := query.Get("terminal_id"); got != "222222222222222" {
					t.Fatalf("terminal_id = %q, want target IMEI", got)
				}
				if got := query.Get("token"); got != "" {
					t.Fatalf("token = %q, want empty", got)
				}
				updateTokens = append(updateTokens, query.Get("temporary_token"))
				if query.Get("temporary_token") == "old-temp" {
					return response(511, ""), nil
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				t.Fatal("activation configuration poll was called")
				return response(500, ""), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result := &Result{
		Offer: &Offer{
			SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
			SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
			TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			TemporaryToken: TemporaryToken{Value: "old-temp"},
		},
		DownloadConfig: DownloadConfig{ProfileICCID: "8900000000000000000"},
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if got := strings.Join(updateTokens, ","); got != "old-temp,new-temp" {
		t.Fatalf("UpdateSubscription tokens = %q, want old-temp,new-temp", got)
	}
	if !eligibilityFetched {
		t.Fatal("CheckEligibility was not called during token refresh")
	}
	if result.State != StateDone {
		t.Fatalf("State = %d, want %d", result.State, StateDone)
	}
}

func TestCompleteActivationReturnsDismissEvent(t *testing.T) {
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			switch Operation(req.URL.Query().Get("operation")) {
			case OperationManage:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="5"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				t.Fatal("activation configuration poll was called")
				return response(500, ""), nil
			default:
				t.Fatalf("unexpected operation %q", req.URL.Query().Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.CompleteActivation(context.Background(), &Result{
		Offer: &Offer{
			SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
			SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
			TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			AuthToken:      AuthToken{Value: "temp"},
		},
		DownloadConfig: DownloadConfig{ProfileICCID: "8900000000000000000"},
	}, ActivationResult{})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if result.State != StateDismissed {
		t.Fatalf("State = %d, want %d", result.State, StateDismissed)
	}
	event, ok := result.Event.(DismissEvent)
	if !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if event.SubscriptionResult != SubscriptionResultDismiss {
		t.Fatalf("Event.SubscriptionResult = %d, want %d", event.SubscriptionResult, SubscriptionResultDismiss)
	}
}

func TestCompleteActivationReturnsPendingActivationForDelayedDownload(t *testing.T) {
	var activationConfigurationCalled bool
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			switch Operation(req.URL.Query().Get("operation")) {
			case OperationManage:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				activationConfigurationCalled = true
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", req.URL.Query().Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.CompleteActivation(context.Background(), &Result{
		Offer: &Offer{
			SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
			SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
			TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			AuthToken:      AuthToken{Value: "temp"},
		},
		DownloadConfig: DownloadConfig{ProfileICCID: "8900000000000000000"},
	}, ActivationResult{})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if activationConfigurationCalled {
		t.Fatal("activation configuration poll was called")
	}
	if result.State != StatePendingActivation {
		t.Fatalf("State = %d, want %d", result.State, StatePendingActivation)
	}
	event, ok := result.Event.(ActivationPendingEvent)
	if !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if event.ICCID != "8900000000000000000" {
		t.Fatalf("Event.ICCID = %q, want %q", event.ICCID, "8900000000000000000")
	}
	if event.SubscriptionResult != SubscriptionResultDelayedDownload {
		t.Fatalf("Event.SubscriptionResult = %d, want %d", event.SubscriptionResult, SubscriptionResultDelayedDownload)
	}
}

func TestManageSubscriptionAcceptsActionableResults(t *testing.T) {
	tests := []struct {
		name               string
		subscriptionResult string
		websheetURL        string
		message            string
		activationCode     string
		wantErr            bool
	}{
		{name: "done", subscriptionResult: "3"},
		{name: "download profile", subscriptionResult: "2", activationCode: "LPA:1$smdp.example$MATCH"},
		{name: "websheet", subscriptionResult: "1", websheetURL: "https://example.test/websheet"},
		{name: "delete profile in use", subscriptionResult: "6"},
		{name: "user input", subscriptionResult: "8", message: "Enter PIN"},
		{name: "dismiss", subscriptionResult: "5"},
		{name: "download profile without download info", subscriptionResult: "2", wantErr: true},
		{name: "websheet without URL", subscriptionResult: "1", wantErr: true},
		{name: "user input without message", subscriptionResult: "8", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
					body := `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="` + tt.subscriptionResult + `"/>`
					if tt.websheetURL != "" {
						body += `<parm name="SubscriptionServiceURL" value="` + tt.websheetURL + `"/>`
					}
					if tt.message != "" {
						body += `<characteristic type="MSG"><parm name="Message" value="` + tt.message + `"/></characteristic>`
					}
					if tt.activationCode != "" {
						body += `<characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + tt.activationCode + `"/></characteristic>`
					}
					body += `</characteristic></wap-provisioningdoc>`
					return response(200, body), nil
				}),
				Entitlement: Entitlement{Server: "https://example.test/ts43"},
				Logger:      slog.Default(),
			}}
			_, err := client.ManageSubscription(
				context.Background(),
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken{Value: "tmp"},
				UserInputResult{Button: MessageButtonUnknown},
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ManageSubscription() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPollConfigurationUsesServerPollingInterval(t *testing.T) {
	var sleeps []time.Duration
	client, err := New(&Config{
		HTTPClient: &queueDoer{bodies: []string{
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="2"/><parm name="PollingInterval" value="2"/></characteristic></characteristic></wap-provisioningdoc>`,
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`,
		}},
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 2*time.Minute {
		t.Fatalf("sleeps = %v, want [2m]", sleeps)
	}
}

func TestPollConfigurationUsesPollingIntervalUnit(t *testing.T) {
	var sleeps []time.Duration
	client, err := New(&Config{
		HTTPClient: &queueDoer{bodies: []string{
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="2"/><parm name="PollingInterval" value="2"/><parm name="PollingIntervalUnit" value="1"/></characteristic></characteristic></wap-provisioningdoc>`,
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`,
		}},
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Minute},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 2*time.Second {
		t.Fatalf("sleeps = %v, want [2s]", sleeps)
	}
}

func TestPollConfigurationUsesConfiguredIntervalWhenServerOmitsPollingInterval(t *testing.T) {
	var sleeps []time.Duration
	client, err := New(&Config{
		HTTPClient: &queueDoer{bodies: []string{
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="2"/></characteristic></characteristic></wap-provisioningdoc>`,
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`,
		}},
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: 3 * time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 3*time.Second {
		t.Fatalf("sleeps = %v, want [3s]", sleeps)
	}
}

func TestPollConfigurationStopsWhenPollingIntervalIsZero(t *testing.T) {
	client, err := New(&Config{
		HTTPClient: &queueDoer{bodies: []string{
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="2"/><parm name="PollingInterval" value="0"/></characteristic></characteristic></wap-provisioningdoc>`,
		}},
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("sleep called when polling interval is zero")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if !errors.Is(err, errPollingNotTriggered) {
		t.Fatalf("pollConfiguration() error = %v, want errPollingNotTriggered", err)
	}
}

func TestActivateReturnsDelayedDownloadEventWhenServerDelaysDownload(t *testing.T) {
	doer := &queueDoer{bodies: []string{
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`,
	}}
	client, err := New(&Config{
		HTTPClient:  doer,
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "tmp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StatePendingDelayedDownload {
		t.Fatalf("State = %d, want %d", result.State, StatePendingDelayedDownload)
	}
	event, ok := result.Event.(DelayedDownloadEvent)
	if !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if event.SourceICCID != "8910000000000000000" {
		t.Fatalf("Event.SourceICCID = %q, want %q", event.SourceICCID, "8910000000000000000")
	}
	if event.TargetEID != "89049032000001000000000000000000" {
		t.Fatalf("Event.TargetEID = %q, want %q", event.TargetEID, "89049032000001000000000000000000")
	}
	if event.TargetIMEI != "222222222222222" {
		t.Fatalf("Event.TargetIMEI = %q, want %q", event.TargetIMEI, "222222222222222")
	}
	if event.SubscriptionResult != SubscriptionResultDelayedDownload {
		t.Fatalf("Event.SubscriptionResult = %d, want %d", event.SubscriptionResult, SubscriptionResultDelayedDownload)
	}
	if result.DownloadConfig != (DownloadConfig{}) {
		t.Fatalf("DownloadConfig = %+v, want empty before activation", result.DownloadConfig)
	}
	if doer.calls != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.calls)
	}
}

func TestApplyDownloadConfigurationRejectsMissingDownloadInfo(t *testing.T) {
	client := &Client{}

	result, err := client.applyDownloadConfiguration(&Result{
		Offer: &Offer{
			TargetDevice: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		},
	}, DownloadConfig{})
	if !errors.Is(err, errDownloadInfoMissing) {
		t.Fatalf("applyDownloadConfiguration() error = %v, want errDownloadInfoMissing", err)
	}
	if result == nil || result.State == StatePendingDownload {
		t.Fatalf("result = %+v, should not enter download", result)
	}
}

func TestActivateReturnsSMDSDiscoveryEventForDelayedDownload(t *testing.T) {
	var acquireConfigurationCalled bool
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			switch Operation(req.URL.Query().Get("operation")) {
			case OperationManage:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				acquireConfigurationCalled = true
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", req.URL.Query().Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", UseSMDSDelayedDownload: true, PollTimeout: time.Hour},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "tmp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if acquireConfigurationCalled {
		t.Fatal("acquire configuration was called")
	}
	if result.State != StatePendingSMDSDiscovery {
		t.Fatalf("State = %d, want %d", result.State, StatePendingSMDSDiscovery)
	}
	event, ok := result.Event.(SMDSDiscoveryEvent)
	if !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if event.SourceICCID != "8910000000000000000" {
		t.Fatalf("Event.SourceICCID = %q, want %q", event.SourceICCID, "8910000000000000000")
	}
	if event.TargetEID != "89049032000001000000000000000000" {
		t.Fatalf("Event.TargetEID = %q, want %q", event.TargetEID, "89049032000001000000000000000000")
	}
	if event.TargetIMEI != "222222222222222" {
		t.Fatalf("Event.TargetIMEI = %q, want %q", event.TargetIMEI, "222222222222222")
	}
	if event.SubscriptionResult != SubscriptionResultDelayedDownload {
		t.Fatalf("Event.SubscriptionResult = %d, want %d", event.SubscriptionResult, SubscriptionResultDelayedDownload)
	}
}

func TestPollConfigurationHandlesDeactivatedServiceStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "deactivated",
			body: `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="3"/></characteristic></characteristic></wap-provisioningdoc>`,
		},
		{
			name: "deactivated no reuse",
			body: `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="4"/></characteristic></characteristic></wap-provisioningdoc>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient:  &queueDoer{bodies: []string{tt.body}},
				Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Second},
				Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger:      slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			resp, err := client.pollConfiguration(context.Background(), &Offer{
				SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			}, authCredential(AuthToken{Value: "tmp"}))
			if err == nil {
				t.Fatal("pollConfiguration() error = nil, want deactivated error")
			}
			if resp == nil || (resp.ServiceStatus != ServiceStatusDeactivated && resp.ServiceStatus != ServiceStatusDeactivatedNoReuse) {
				t.Fatalf("ServiceStatus = %v", resp)
			}
		})
	}
}

func TestPollConfigurationRejectsUnknownServiceStatus(t *testing.T) {
	client, err := New(&Config{
		HTTPClient: &queueDoer{bodies: []string{
			`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"></characteristic></characteristic></wap-provisioningdoc>`,
		}},
		Entitlement: Entitlement{Server: "https://example.test/ts43", PollTimeout: time.Hour, PollInterval: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("sleep called for unknown service status")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.pollConfiguration(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err == nil {
		t.Fatal("pollConfiguration() error = nil, want unknown service status error")
	}
}

func TestManageSubscriptionOldTerminalIDGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		sourceProto int
		wantOld     bool
		wantProto   string
	}{
		{name: "default sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43"}, wantOld: true},
		{name: "source credential type three sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode}, wantOld: true},
		{name: "source credential type six sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}, wantOld: true},
		{name: "type seven sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeOldTerminalIDs}, wantOld: true},
		{name: "type nine sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantOld: true},
		{name: "type two sends old IDs by default", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}, wantOld: true},
		{name: "type eleven sends old IDs by default", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenVariant11}, wantOld: true},
		{name: "restricted type two omits old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, RestrictOldTerminalFields: true}},
		{name: "restricted type three omits old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode, RestrictOldTerminalFields: true}},
		{name: "restricted type seven sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeOldTerminalIDs, RestrictOldTerminalFields: true}, wantOld: true},
		{name: "Samsung ignores Google restricted old ID gate", entitlement: Entitlement{Server: "https://example.test/ts43", Channel: EntitlementChannelSamsung, TransferType: TransferTypeTemporaryToken, RestrictOldTerminalFields: true}, wantOld: true},
		{name: "Samsung sends old IDs without Google proto", entitlement: Entitlement{Server: "https://example.test/ts43", Channel: EntitlementChannelSamsung, TransferType: TransferTypeTemporaryToken, RestrictOldTerminalFields: true}, sourceProto: 1, wantOld: true},
		{name: "explicit old terminal protocol sends proto", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode}, sourceProto: 1, wantOld: true, wantProto: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("old_terminal_id") != ""; got != tt.wantOld {
						t.Fatalf("old_terminal_id present = %v, want %v", got, tt.wantOld)
					}
					if got := query.Get("old_terminal_iccid") != ""; got != tt.wantOld {
						t.Fatalf("old_terminal_iccid present = %v, want %v", got, tt.wantOld)
					}
					if got := query.Get("old_terminal_entitlement_protocol"); got != tt.wantProto {
						t.Fatalf("old_terminal_entitlement_protocol = %q, want %q", got, tt.wantProto)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			}}
			credential := authCredential(AuthToken{Value: "tmp"})
			if transferTypeUsesTemporaryToken(tt.entitlement.TransferType) {
				credential = temporaryCredential(TemporaryToken{Value: "tmp"})
			}
			_, err := client.manageSubscription(
				context.Background(),
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EntitlementProtocol: tt.sourceProto},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				credential,
				UserInputResult{Button: MessageButtonUnknown},
			)
			if err != nil {
				t.Fatalf("ManageSubscription() error = %v", err)
			}
		})
	}
}

func TestAcquireConfigurationSourceCredentialShape(t *testing.T) {
	tests := []struct {
		name         string
		transferType TransferType
	}{
		{name: "type three", transferType: TransferTypeReusableActivationCode},
		{name: "type six", transferType: TransferTypeTS43V8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != OperationAcquireConf.String() {
						t.Fatalf("operation = %q, want %s", got, OperationAcquireConf)
					}
					if got := query.Get("operation_type"); got != "" {
						t.Fatalf("operation_type = %q, want empty", got)
					}
					if got := query.Get("terminal_id"); got != "111111111111111" {
						t.Fatalf("terminal_id = %q, want source IMEI", got)
					}
					if got := query.Get("terminal_eid"); got != "" {
						t.Fatalf("terminal_eid = %q, want empty", got)
					}
					if got := query.Get("terminal_iccid"); got != "" {
						t.Fatalf("terminal_iccid = %q, want empty", got)
					}
					if got := query.Get("target_terminal_id"); got != "222222222222222" {
						t.Fatalf("target_terminal_id = %q, want target IMEI", got)
					}
					if got := query.Get("target_terminal_eid"); got != "89049032000001000000000000000000" {
						t.Fatalf("target_terminal_eid = %q, want target EID", got)
					}
					if got := query.Get("old_terminal_entitlement_protocol"); got != "1" {
						t.Fatalf("old_terminal_entitlement_protocol = %q, want 1", got)
					}
					if got := query.Get("old_terminal_id"); got != "" {
						t.Fatalf("old_terminal_id = %q, want empty", got)
					}
					if got := query.Get("old_terminal_iccid"); got != "" {
						t.Fatalf("old_terminal_iccid = %q, want empty", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					PollTimeout:  time.Second,
					TransferType: tt.transferType,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = client.acquireConfiguration(
				context.Background(),
				sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EntitlementProtocol: 1},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				authCredential(AuthToken{Value: "tmp"}),
			)
			if err != nil {
				t.Fatalf("acquireConfiguration() error = %v", err)
			}
		})
	}
}

func TestSamsungTokenOperationRequestFields(t *testing.T) {
	identity := sim.Identity{ICCID: "8910000000000000000", IMSI: "450051234567890", MCC: "450", MNC: "05"}
	source := Device{IMEI: "111111111111111", ICCID: "8910000000000000000"}
	target := Device{IMEI: "222222222222222", ICCID: "8920000000000000000", EID: "89049032000001000000000000000000", EntitlementProtocol: 1}

	tests := []struct {
		name      string
		operation Operation
		run       func(context.Context, *Client) error
	}{
		{
			name:      "CheckEligibility",
			operation: OperationCheck,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.CheckEligibility(ctx, identity, source, AuthToken{Value: "auth"}, target)
				return err
			},
		},
		{
			name:      "AcquireTemporaryToken",
			operation: OperationTempToken,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.AcquireTemporaryToken(ctx, identity, source, target, AuthToken{Value: "auth"})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != tt.operation.String() {
						t.Fatalf("operation = %q, want %s", got, tt.operation)
					}
					if got := query.Get("token"); got != "auth" {
						t.Fatalf("token = %q, want auth", got)
					}
					if got := query.Get("terminal_id"); got != source.IMEI {
						t.Fatalf("terminal_id = %q, want source IMEI", got)
					}
					if got := query.Get("terminal_iccid"); got != source.ICCID {
						t.Fatalf("terminal_iccid = %q, want source ICCID", got)
					}
					if got := query.Get("target_terminal_iccid"); got != "" {
						t.Fatalf("target_terminal_iccid = %q, want empty", got)
					}
					if got := query.Get("target_terminal_eid"); got != "" {
						t.Fatalf("target_terminal_eid = %q, want empty", got)
					}
					if got := query.Get("target_terminal_entitlement_protocol"); got != "" {
						t.Fatalf("target_terminal_entitlement_protocol = %q, want empty", got)
					}
					switch tt.operation {
					case OperationCheck:
						want, err := terminalIMSIEAPBase64(identity, DefaultEAPAkaRealm)
						if err != nil {
							t.Fatalf("terminalIMSIEAPBase64() error = %v", err)
						}
						if got := query.Get("IMSI"); got != want {
							t.Fatalf("IMSI = %q, want %q", got, want)
						}
						if got := query.Get("target_terminal_id"); got != target.IMEI {
							t.Fatalf("target_terminal_id = %q, want target IMEI", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						if got := query.Get("IMSI"); got != "" {
							t.Fatalf("IMSI = %q, want empty", got)
						}
						if got := query.Get("target_terminal_id"); got != "" {
							t.Fatalf("target_terminal_id = %q, want empty", got)
						}
						if got := query.Get("operation_targets"); got != "ManageSubscription,AcquireConfiguration" {
							t.Fatalf("operation_targets = %q, want ManageSubscription,AcquireConfiguration", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
					default:
						t.Fatalf("unexpected operation %s", tt.operation)
						return response(500, ""), nil
					}
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					Channel:      EntitlementChannelSamsung,
					TransferType: TransferTypeTemporaryToken,
					PollTimeout:  time.Second,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: source},
				Target: Endpoint{Device: target},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := tt.run(context.Background(), client); err != nil {
				t.Fatalf("%s error = %v", tt.operation, err)
			}
		})
	}
}

func TestSamsungSourceCredentialRequestFields(t *testing.T) {
	identity := sim.Identity{ICCID: "8910000000000000000", IMSI: "450051234567890", MCC: "450", MNC: "05"}
	source := Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EID: "89049032000000000000000000000001", EntitlementProtocol: 1}
	target := Device{IMEI: "222222222222222", ICCID: "8920000000000000000", EID: "89049032000001000000000000000000"}

	tests := []struct {
		name      string
		operation Operation
		run       func(context.Context, *Client) error
	}{
		{
			name:      "ManageSubscription",
			operation: OperationManage,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.manageSubscription(ctx, identity, source, target, authCredential(AuthToken{Value: "tmp"}), UserInputResult{Button: MessageButtonUnknown})
				return err
			},
		},
		{
			name:      "AcquireConfiguration",
			operation: OperationAcquireConf,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.acquireConfiguration(ctx, identity, source, target, authCredential(AuthToken{Value: "tmp"}))
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != tt.operation.String() {
						t.Fatalf("operation = %q, want %s", got, tt.operation)
					}
					if got := query.Get("terminal_id"); got != source.IMEI {
						t.Fatalf("terminal_id = %q, want source IMEI", got)
					}
					if got := query.Get("terminal_iccid"); got != source.ICCID {
						t.Fatalf("terminal_iccid = %q, want source ICCID", got)
					}
					if got := query.Get("target_terminal_id"); got != target.IMEI {
						t.Fatalf("target_terminal_id = %q, want target IMEI", got)
					}
					if got := query.Get("target_terminal_iccid"); got != target.ICCID {
						t.Fatalf("target_terminal_iccid = %q, want target ICCID", got)
					}
					wantTerminalEID := ""
					wantTargetEID := target.EID
					if tt.operation == OperationAcquireConf {
						wantTerminalEID = source.EID
						wantTargetEID = ""
					}
					if got := query.Get("terminal_eid"); got != wantTerminalEID {
						t.Fatalf("terminal_eid = %q, want %q", got, wantTerminalEID)
					}
					if got := query.Get("target_terminal_eid"); got != wantTargetEID {
						t.Fatalf("target_terminal_eid = %q, want %q", got, wantTargetEID)
					}
					if got := query.Get("old_terminal_entitlement_protocol"); got != "" {
						t.Fatalf("old_terminal_entitlement_protocol = %q, want empty", got)
					}
					want, err := terminalIMSIEAPBase64(identity, DefaultEAPAkaRealm)
					if err != nil {
						t.Fatalf("terminalIMSIEAPBase64() error = %v", err)
					}
					if got := query.Get("IMSI"); got != want {
						t.Fatalf("IMSI = %q, want %q", got, want)
					}
					if got := query.Get("terminal_imsieapbase64"); got != "" {
						t.Fatalf("terminal_imsieapbase64 = %q, want empty", got)
					}
					if tt.operation == OperationManage {
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					Channel:      EntitlementChannelSamsung,
					TransferType: TransferTypeTS43V8,
					PollTimeout:  time.Second,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: source},
				Target: Endpoint{Device: target},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := tt.run(context.Background(), client); err != nil {
				t.Fatalf("%s error = %v", tt.operation, err)
			}
		})
	}
}

func TestSamsungAcquireConfigurationEIDPlacement(t *testing.T) {
	tests := []struct {
		name          string
		transferType  TransferType
		wantTerminal  string
		wantTargetEID string
	}{
		{
			name:          "digitalized SIM without temporary token uses target_terminal_eid",
			transferType:  TransferTypeOldTerminalIDs,
			wantTargetEID: "89049032000001000000000000000000",
		},
		{
			name:         "digitalized SIM with temporary token uses terminal_eid",
			transferType: TransferTypeTemporaryTokenOldTerminalIDs,
			wantTerminal: "89049032000001000000000000000000",
		},
		{
			name:         "temporary token transfer uses terminal_eid",
			transferType: TransferTypeTemporaryToken,
			wantTerminal: "89049032000001000000000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != OperationAcquireConf.String() {
						t.Fatalf("operation = %q, want %s", got, OperationAcquireConf)
					}
					if got := query.Get("terminal_eid"); got != tt.wantTerminal {
						t.Fatalf("terminal_eid = %q, want %q", got, tt.wantTerminal)
					}
					if got := query.Get("target_terminal_eid"); got != tt.wantTargetEID {
						t.Fatalf("target_terminal_eid = %q, want %q", got, tt.wantTargetEID)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					Channel:      EntitlementChannelSamsung,
					TransferType: tt.transferType,
					PollTimeout:  time.Second,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			credential := authCredential(AuthToken{Value: "tmp"})
			if transferTypeUsesTemporaryToken(tt.transferType) {
				credential = temporaryCredential(TemporaryToken{Value: "tmp"})
			}
			_, err = client.acquireConfiguration(
				context.Background(),
				sim.Identity{ICCID: "8910000000000000000", IMSI: "450051234567890", MCC: "450", MNC: "05"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				credential,
			)
			if err != nil {
				t.Fatalf("AcquireConfiguration() error = %v", err)
			}
		})
	}
}

func TestSamsungTemporaryTokenTransferSendsBasicTargetFields(t *testing.T) {
	identity := sim.Identity{ICCID: "8910000000000000000", IMSI: "262011234567890", MCC: "262", MNC: "01"}
	source := Device{IMEI: "111111111111111", ICCID: "8910000000000000000"}
	target := Device{IMEI: "222222222222222", ICCID: "8920000000000000000", EID: "89049032000001000000000000000000"}

	tests := []struct {
		name      string
		operation Operation
		run       func(context.Context, *Client) error
	}{
		{
			name:      "ManageSubscription",
			operation: OperationManage,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.manageSubscription(ctx, identity, source, target, temporaryCredential(TemporaryToken{Value: "tmp"}), UserInputResult{Button: MessageButtonUnknown})
				return err
			},
		},
		{
			name:      "AcquireConfiguration",
			operation: OperationAcquireConf,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.acquireConfiguration(ctx, identity, source, target, temporaryCredential(TemporaryToken{Value: "tmp"}))
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != tt.operation.String() {
						t.Fatalf("operation = %q, want %s", got, tt.operation)
					}
					if got := query.Get("target_terminal_id"); got != target.IMEI {
						t.Fatalf("target_terminal_id = %q, want target IMEI", got)
					}
					if got := query.Get("target_terminal_iccid"); got != target.ICCID {
						t.Fatalf("target_terminal_iccid = %q, want target ICCID", got)
					}
					if tt.operation == OperationManage {
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					Channel:      EntitlementChannelSamsung,
					TransferType: TransferTypeTemporaryToken,
					PollTimeout:  time.Second,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: source},
				Target: Endpoint{Device: target},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := tt.run(context.Background(), client); err != nil {
				t.Fatalf("%s error = %v", tt.operation, err)
			}
		})
	}
}

func TestSamsungDigitalizedTempToken4FFManageClearsTerminalICCIDAndIMSI(t *testing.T) {
	identity := sim.Identity{ICCID: "8910000000000000000", IMSI: "440501234567890", MCC: "440", MNC: "50"}
	source := Device{IMEI: "111111111111111", ICCID: "8910000000000000000"}
	target := Device{IMEI: "222222222222222", ICCID: "8920000000000000000", EID: "89049032000001000000000000000000"}

	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			if got := query.Get("operation"); got != OperationManage.String() {
				t.Fatalf("operation = %q, want %s", got, OperationManage)
			}
			if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
				t.Fatalf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
			}
			if got := query.Get("terminal_iccid"); got != "" {
				t.Fatalf("terminal_iccid = %q, want empty", got)
			}
			if got := query.Get("IMSI"); got != "" {
				t.Fatalf("IMSI = %q, want empty", got)
			}
			if got := query.Get("target_terminal_id"); got != target.IMEI {
				t.Fatalf("target_terminal_id = %q, want target IMEI", got)
			}
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{
			Server:                 "https://example.test/ts43",
			Channel:                EntitlementChannelSamsung,
			TransferType:           TransferTypeTemporaryTokenOldTerminalIDs,
			UseTempTokenConversion: true,
			PollTimeout:            time.Second,
		},
		Source: Endpoint{Channel: fakeChannel{}, Device: source},
		Target: Endpoint{Device: target},
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.manageSubscription(context.Background(), identity, source, target, temporaryCredential(TemporaryToken{Value: "tmp"}), UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("ManageSubscription() error = %v", err)
	}
}

func TestTransferRequestAuthMode(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantAuth    string
		wantTemp    string
	}{
		{name: "default uses auth token", wantAuth: "transfer"},
		{name: "type two uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken}, wantTemp: "transfer"},
		{name: "source credential type three uses auth token by default", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode}, wantAuth: "transfer"},
		{name: "source credential type six uses auth token by default", entitlement: Entitlement{TransferType: TransferTypeTS43V8}, wantAuth: "transfer"},
		{name: "source credential type three ignores temporary token conversion", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode, UseTempTokenConversion: true}, wantAuth: "transfer"},
		{name: "source credential type six ignores temporary token conversion", entitlement: Entitlement{TransferType: TransferTypeTS43V8, UseTempTokenConversion: true}, wantAuth: "transfer"},
		{name: "type seven uses auth token", entitlement: Entitlement{TransferType: TransferTypeOldTerminalIDs}, wantAuth: "transfer"},
		{name: "type nine uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantTemp: "transfer"},
		{name: "type eleven uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11}, wantTemp: "transfer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{Entitlement: tt.entitlement}}

			req := client.transferRequest(Device{IMEI: "111111111111111"}, client.transferCredentialFromTokens(TransferTokenResult{
				Auth:      AuthToken{Value: "transfer"},
				Temporary: TemporaryToken{Value: "transfer"},
			}))

			if req.AuthenticationToken != tt.wantAuth {
				t.Fatalf("AuthenticationToken = %q, want %q", req.AuthenticationToken, tt.wantAuth)
			}
			if req.TemporaryToken != tt.wantTemp {
				t.Fatalf("TemporaryToken = %q, want %q", req.TemporaryToken, tt.wantTemp)
			}
		})
	}
}

func TestSelectedTransferType(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		want        TransferType
	}{
		{name: "ordinary transfer keeps default", want: TransferTypeDisabled},
		{
			name:        "ordinary transfer keeps explicit type",
			entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode},
			want:        TransferTypeReusableActivationCode,
		},
		{
			name:        "pSIM type two keeps explicit carrier type",
			entitlement: Entitlement{SourceSIMType: SIMTypePSIM, TransferType: TransferTypeTemporaryToken},
			want:        TransferTypeTemporaryToken,
		},
		{
			name:        "pSIM type three keeps explicit carrier type",
			entitlement: Entitlement{SourceSIMType: SIMTypePSIM, TransferType: TransferTypeReusableActivationCode},
			want:        TransferTypeReusableActivationCode,
		},
		{
			name:        "pSIM type six keeps explicit carrier type with temp conversion",
			entitlement: Entitlement{SourceSIMType: SIMTypePSIM, TransferType: TransferTypeTS43V8, UseTempTokenConversion: true},
			want:        TransferTypeTS43V8,
		},
		{
			name:        "pSIM conversion without temp token uses old terminal IDs",
			entitlement: Entitlement{SourceSIMType: SIMTypePSIM},
			want:        TransferTypeOldTerminalIDs,
		},
		{
			name:        "pSIM conversion with temp token uses temporary token old terminal IDs",
			entitlement: Entitlement{SourceSIMType: SIMTypePSIM, UseTempTokenConversion: true},
			want:        TransferTypeTemporaryTokenOldTerminalIDs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectedTransferType(tt.entitlement); got != tt.want {
				t.Fatalf("selectedTransferType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSamsungManageSubscriptionOperationType(t *testing.T) {
	tests := []struct {
		name         string
		transferType TransferType
		want         OperationType
	}{
		{
			name: "Samsung D2D transfer uses Samsung operation type",
			want: OperationTypeSamsungD2DTransfer,
		},
		{
			name:         "Samsung eSIM D2D type six follows Samsung TS.43 transfer type",
			transferType: TransferTypeTS43V8,
			want:         OperationTypeTransferSubscription,
		},
		{
			name:         "Samsung pSIM digitalized type seven uses transfer subscription",
			transferType: TransferTypeOldTerminalIDs,
			want:         OperationTypeTransferSubscription,
		},
		{
			name:         "Samsung pSIM temporary-token type nine uses transfer subscription",
			transferType: TransferTypeTemporaryTokenOldTerminalIDs,
			want:         OperationTypeTransferSubscription,
		},
		{
			name:         "Samsung temporary-token D2D type two uses Samsung operation type",
			transferType: TransferTypeTemporaryToken,
			want:         OperationTypeSamsungD2DTransfer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := samsungManageSubscriptionOperationType(tt.transferType); got != tt.want {
				t.Fatalf("samsungManageSubscriptionOperationType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSourceCredentialTransferType(t *testing.T) {
	tests := []struct {
		name string
		t    TransferType
		want bool
	}{
		{name: "type three", t: TransferTypeReusableActivationCode, want: true},
		{name: "type six", t: TransferTypeTS43V8, want: true},
		{name: "temporary token", t: TransferTypeTemporaryToken},
		{name: "old terminal IDs", t: TransferTypeOldTerminalIDs},
		{name: "disabled", t: TransferTypeDisabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSourceCredentialTransferType(tt.t); got != tt.want {
				t.Fatalf("IsSourceCredentialTransferType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGoogleIncludeTargetTerminalIdentity(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		want        bool
	}{
		{name: "default sends target identity", want: true},
		{name: "type two follows set target terminal fields", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target terminal fields sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, want: true},
		{name: "source credential type three sends target identity", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode}, want: true},
		{name: "source credential type six sends target identity", entitlement: Entitlement{TransferType: TransferTypeTS43V8}, want: true},
		{name: "type seven sends target identity", entitlement: Entitlement{TransferType: TransferTypeOldTerminalIDs}, want: true},
		{name: "type nine sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, want: true},
		{name: "type eleven follows set target terminal fields", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11}},
		{name: "type eleven with set target terminal fields sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11, SetTargetTerminalFields: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := googleIncludeTargetTerminalIdentity(tt.entitlement); got != tt.want {
				t.Fatalf("googleIncludeTargetTerminalIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManageSubscriptionTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target terminal fields sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, wantTarget: true},
		{name: "source credential type six sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}, wantTarget: true},
		{name: "type nine sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantTarget: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					wantModel := ""
					if tt.wantTarget && !IsSourceCredentialTransferType(tt.entitlement.TransferType) {
						wantModel = "Pixel"
					}
					if got := query.Get("target_terminal_model"); got != wantModel {
						t.Fatalf("target_terminal_model = %q, want %q", got, wantModel)
					}
					wantOldICCID := ""
					if googleIncludeOldTerminalIDs(tt.entitlement) {
						wantOldICCID = "8910000000000000000"
					}
					if got := query.Get("old_terminal_iccid"); got != wantOldICCID {
						t.Fatalf("old_terminal_iccid = %q, want %q", got, wantOldICCID)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			}}

			credential := authCredential(AuthToken{Value: "tmp"})
			if transferTypeUsesTemporaryToken(tt.entitlement.TransferType) {
				credential = temporaryCredential(TemporaryToken{Value: "tmp"})
			}
			_, err := client.manageSubscription(
				context.Background(),
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
				credential,
				UserInputResult{Button: MessageButtonUnknown},
			)
			if err != nil {
				t.Fatalf("ManageSubscription() error = %v", err)
			}
		})
	}
}

func TestCheckEligibilityTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target fields sends target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, wantTarget: true},
		{name: "source credential type six sends target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}, wantTarget: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_model") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_model present = %v, want %v", got, tt.wantTarget)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			}}

			_, err := client.CheckEligibility(
				context.Background(),
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111"},
				AuthToken{Value: "auth"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
			)
			if err != nil {
				t.Fatalf("CheckEligibility() error = %v", err)
			}
		})
	}
}

func TestAcquireTemporaryTokenTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target fields sends target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, wantTarget: true},
		{name: "type nine sends target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantTarget: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{cfg: Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_model") != ""; got != tt.wantTarget {
						t.Fatalf("target_terminal_model present = %v, want %v", got, tt.wantTarget)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			}}

			tokens, err := client.AcquireTemporaryToken(
				context.Background(),
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
				AuthToken{Value: "auth"},
			)
			if err != nil {
				t.Fatalf("AcquireTemporaryToken() error = %v", err)
			}
			if !slices.Equal(tokens.Temporary.Targets, []Operation{OperationManage, OperationAcquireConf}) {
				t.Fatalf("Temporary.Targets = %v, want ManageSubscription and AcquireConfiguration", tokens.Temporary.Targets)
			}
		})
	}
}

func TestTransferRequestsSendTargetAndConfiguredFields(t *testing.T) {
	client := &Client{cfg: Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case OperationManage, OperationAcquireConf:
				want := map[string]string{
					"target_terminal_id":               "222222222222222",
					"target_terminal_eid":              "89049032000001000000000000000000",
					"target_terminal_model":            "Pixel",
					"target_terminal_sn":               "serial",
					"companion_terminal_id":            "444444444444444",
					"companion_terminal_vendor":        "Fit",
					"companion_terminal_model":         "Watch",
					"companion_terminal_sw_version":    "1.0",
					"companion_terminal_friendly_name": "Pixel Watch",
					"companion_terminal_service":       "SharedNumber",
					"companion_terminal_iccid":         "8910000000000000001",
					"companion_terminal_eid":           "89049032000001000000000000000001",
				}
				for key, value := range want {
					if got := query.Get(key); got != value {
						t.Fatalf("%s = %q, want %q", key, got, value)
					}
				}
				imeis := query["target_terminal_imeis"]
				if len(imeis) != 2 || imeis[0] != "222222222222222" || imeis[1] != "333333333333333" {
					t.Fatalf("target_terminal_imeis = %v, want repeated query parameters", imeis)
				}
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
			}
			if Operation(query.Get("operation")) == OperationManage {
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			}
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{
			Server:      "https://example.test/ts43",
			PollTimeout: time.Second,
		},
		Logger: slog.Default(),
		Companion: Device{
			IMEI:            "444444444444444",
			ICCID:           "8910000000000000001",
			EID:             "89049032000001000000000000000001",
			Vendor:          "Fit",
			Model:           "Watch",
			SoftwareVersion: "1.0",
			FriendlyName:    "Pixel Watch",
			Service:         "SharedNumber",
		},
	}}

	_, err := client.activate(context.Background(), &Offer{
		SourceIdentity: sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice: Device{
			IMEI:         "222222222222222",
			IMEIs:        []string{"222222222222222", "333333333333333"},
			EID:          "89049032000001000000000000000000",
			Model:        "Pixel",
			SerialNumber: "serial",
		},
		AuthToken: AuthToken{Value: "tmp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
}

func TestTemporaryTokenExpiresAt(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	absolute := now.Add(3 * time.Hour).Format(time.RFC3339Nano)
	tests := []struct {
		name    string
		value   string
		want    time.Time
		wantErr bool
	}{
		{name: "seconds reject", value: "7200", wantErr: true},
		{name: "absolute RFC3339", value: absolute, want: now.Add(3 * time.Hour)},
		{name: "empty rejects", wantErr: true},
		{name: "malformed rejects", value: "not-a-time", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := temporaryTokenExpiresAt(tt.value, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("temporaryTokenExpiresAt() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("temporaryTokenExpiresAt() = %s, want %s", got, tt.want)
			}
		})
	}
}

type queueDoer struct {
	bodies []string
	calls  int
}

func (d *queueDoer) Do(*http.Request) (*http.Response, error) {
	if d.calls >= len(d.bodies) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	body := d.bodies[d.calls]
	d.calls++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

type fakeDoer func(*http.Request) (*http.Response, error)

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func responseWithCookie(status int, body, cookie string) *http.Response {
	resp := response(status, body)
	resp.Header = http.Header{"Set-Cookie": []string{cookie}}
	return resp
}

func eapRelayBody(t *testing.T, identifier byte) string {
	t.Helper()
	rand := make([]byte, 16)
	autn := make([]byte, 16)
	for i := range rand {
		rand[i] = identifier
		autn[i] = identifier + 1
	}
	challenge := sim.AKAChallenge{Identifier: identifier, Rand: rand, Autn: autn}
	packet, err := challenge.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText error = %v", err)
	}
	return `{"eap-relay-packet":"` + string(packet) + `"}`
}

type fakeChannel struct{}

func (fakeChannel) Identity(context.Context) (*sim.Identity, error) {
	return &sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}, nil
}
func (fakeChannel) AuthenticateAKA(context.Context, *sim.AKARequest) (*sim.AKAResponse, error) {
	return nil, nil
}

type identityChannel struct {
	identity sim.Identity
}

func (c identityChannel) Identity(context.Context) (*sim.Identity, error) {
	return &c.identity, nil
}

func (identityChannel) AuthenticateAKA(context.Context, *sim.AKARequest) (*sim.AKAResponse, error) {
	return nil, nil
}

type akaChannel struct{}

func (akaChannel) Identity(context.Context) (*sim.Identity, error) {
	return &sim.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}, nil
}

func (akaChannel) AuthenticateAKA(context.Context, *sim.AKARequest) (*sim.AKAResponse, error) {
	return &sim.AKAResponse{
		Res: []byte{0x01, 0x02, 0x03, 0x04},
		CK:  []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f},
		IK:  []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f},
	}, nil
}

func operationList(ops []Operation) string {
	values := make([]string, 0, len(ops))
	for _, op := range ops {
		values = append(values, op.String())
	}
	return strings.Join(values, ",")
}
