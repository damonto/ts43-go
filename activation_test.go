package ts43

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDownloadActivationStateControlsCompletion(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name        string
			status      int
			delayed     bool
			expireToken bool
			wantUpdates int
		}{
			{name: "Google activated delayed download", status: 1, delayed: true},
			{name: "Google activated without surviving credentials", status: 1, delayed: true, expireToken: true},
			{name: "Google reusable download needs activation", status: 3, delayed: true, wantUpdates: 1},
			{name: "Google direct download needs activation", wantUpdates: 1},
		} {
			t.Run(fmt.Sprintf("post_%v/%s", post, tt.name), func(t *testing.T) {
				now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
				offer := alignmentOffer()
				offer.TemporaryToken = TemporaryToken{Value: "temporary", ExpiresAt: now.Add(time.Minute), Targets: []Operation{OperationManage, OperationAcquireConf}}
				transfers, updates, queries := 0, 0, 0
				client := newClient(Config{
					Now: func() time.Time { return now },
					Entitlement: Entitlement{
						Server:       "https://carrier.example",
						TransferType: TransferTypeTemporaryToken,
						UsePost:      post,
					},
					HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
						fields := alignmentRequestFields(t, req)
						switch Operation(fields.Get("operation")) {
						case OperationManage:
							if fields.Get("operation_type") == "3" {
								transfers++
								if tt.delayed {
									return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
								}
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example","ProfileIccid":"8920000000000000000"}}}`), nil
							}
							updates++
							if tt.wantUpdates == 0 {
								// The carrier has already activated this download and does
								// not support a second activation request.
								return response(200, `{"ap2009":{"OperationResult":103}}`), nil
							}
							if fields.Get("operation_type") != "4" || fields.Get("terminal_iccid") != "8920000000000000000" {
								t.Error("activation update does not identify the installed profile")
							}
							return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
						case OperationAcquireConf:
							queries++
							if updates == 0 {
								return response(200, fmt.Sprintf(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="%d"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/><parm name="ProfileIccid" value="8920000000000000000"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`, tt.status)), nil
							}
							return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
						default:
							t.Fatalf("unexpected operation %q", fields.Get("operation"))
							return nil, nil
						}
					}),
				})
				result, err := client.activate(t.Context(), offer, UserInputResult{Button: MessageButtonUnknown})
				if err != nil {
					t.Fatal(err)
				}
				if result.State != StatePendingDownload {
					t.Fatalf("download state=%v, want %v", result.State, StatePendingDownload)
				}
				if _, ok := result.Event.(DownloadReadyEvent); !ok {
					t.Fatalf("event=%T, want download before completion", result.Event)
				}
				if tt.expireToken {
					now = now.Add(2 * time.Minute)
				}
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "8920000000000000000"})
				if err != nil || result.State != StateDone {
					t.Fatalf("completion state=%v err=%v", result.State, err)
				}
				if _, ok := result.Event.(DoneEvent); !ok {
					t.Fatalf("event=%T, want done", result.Event)
				}
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "8920000000000000000"})
				wantQueries := tt.wantUpdates
				if tt.delayed {
					wantQueries++
				}
				if err != nil || result.State != StateDone || transfers != 1 || updates != tt.wantUpdates || queries != wantQueries {
					t.Fatalf("state=%v transfers=%d updates=%d queries=%d err=%v", result.State, transfers, updates, queries, err)
				}
				if result.DownloadConfig.ProfileICCID != "8920000000000000000" {
					t.Error("installed ICCID was lost")
				}
			})
		}
	}
}

func TestCompleteActivationRejectsPreDownloadStates(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state State
	}{
		{name: "unknown", state: StateUnknown},
		{name: "user input", state: StatePendingUserInput},
		{name: "websheet", state: StatePendingWebsheet},
		{name: "source deletion", state: StatePendingSourceProfileDeletion},
		{name: "configuration", state: StatePendingConfiguration},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := newClient(Config{
				Entitlement: Entitlement{Server: "https://carrier.example"},
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					calls++
					return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
				}),
			})
			result := &Result{
				State:          tt.state,
				DownloadConfig: DownloadConfig{ProfileICCID: "original"},
				session:        &transferSession{offer: alignmentOffer()},
			}
			next, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
			if err == nil {
				t.Fatal("completion accepted before installation was requested")
			}
			if next != result || result.State != tt.state || result.DownloadConfig.ProfileICCID != "original" || calls != 0 {
				t.Fatalf("state=%v iccid=%q calls=%d", result.State, result.DownloadConfig.ProfileICCID, calls)
			}
		})
	}
}

func TestCompleteActivationAcceptsExternalDownloads(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state State
	}{
		{name: "SM-DS discovery", state: StatePendingSMDSDiscovery},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updates, queries := 0, 0
			client := newClient(Config{
				Entitlement: Entitlement{Server: "https://carrier.example"},
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					fields := alignmentRequestFields(t, req)
					switch Operation(fields.Get("operation")) {
					case OperationManage:
						updates++
						if fields.Get("operation_type") != "4" || fields.Get("terminal_iccid") != "installed" {
							t.Fatalf("unexpected update after external installation: %v", fields)
						}
						return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
					case OperationAcquireConf:
						queries++
						return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
					default:
						t.Fatalf("unexpected operation %q", fields.Get("operation"))
						return nil, nil
					}
				}),
			})
			result := &Result{State: tt.state, session: &transferSession{offer: alignmentOffer()}}
			result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
			if err != nil || result.State != StateDone || updates != 1 || queries != 1 || result.DownloadConfig.ProfileICCID != "installed" {
				t.Fatalf("state=%v updates=%d queries=%d err=%v", result.State, updates, queries, err)
			}
		})
	}
}

func TestWebsheetSelectionCannotRestartInstalledTransfer(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state State
	}{
		{name: "activation pending", state: StatePendingActivation},
		{name: "completed", state: StateDone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := newClient(Config{
				Entitlement: Entitlement{Server: "https://carrier.example"},
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					calls++
					return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				}),
			})
			offer := alignmentOffer()
			iccid, imei := offer.SourceDevice.ICCID, offer.SourceDevice.IMEI
			result := &Result{State: tt.state, session: &transferSession{offer: offer, profileInstalled: true, websheetOpen: true}}
			next, err := client.Continue(t.Context(), result, ContinueRequest{Websheet: &WebsheetResult{
				Event: WebsheetEventSelectionCompleted,
				ICCID: "another-profile",
				IMEI:  "333333333333333",
			}})
			if err != nil || next != result || result.State != tt.state || calls != 0 {
				t.Fatalf("state=%v calls=%d err=%v", result.State, calls, err)
			}
			if offer.SourceDevice.ICCID != iccid || offer.SourceDevice.IMEI != imei || offer.SourceIdentity.ICCID != iccid {
				t.Fatal("callback changed the source of an installed transfer")
			}
		})
	}
}

func TestUnsupportedActivationUpdateRemainsPending(t *testing.T) {
	calls := 0
	client := newClient(Config{
		Entitlement: Entitlement{Server: "https://carrier.example"},
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			calls++
			fields := alignmentRequestFields(t, req)
			if fields.Get("operation") != string(OperationManage) || fields.Get("operation_type") != "4" {
				t.Fatalf("unexpected request after installation: %v", fields)
			}
			return response(200, `{"ap2009":{"OperationResult":103}}`), nil
		}),
	})
	result := &Result{State: StatePendingDownload, session: &transferSession{offer: alignmentOffer()}}
	result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
	var denied *CarrierDeniedError
	if !errors.As(err, &denied) || denied.Result.OperationResult != 103 {
		t.Fatalf("error=%v, want carrier rejection 103", err)
	}
	if result.State != StatePendingActivation || result.DownloadConfig.ProfileICCID != "installed" || calls != 1 {
		t.Fatalf("result=%v calls=%d", result, calls)
	}
}

func TestInstalledProfileResumesUnacceptedUpdate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		portal bool
	}{
		{name: "continue"},
		{name: "portal status", portal: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updates, queries := 0, 0
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				fields := alignmentRequestFields(t, req)
				if Operation(fields.Get("operation")) == OperationManage {
					updates++
					if fields.Get("operation_type") != "4" {
						t.Fatal("restarted transfer")
					}
					if updates == 1 {
						return response(503, ""), nil
					}
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				}
				queries++
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
			})})
			result := &Result{State: StatePendingDownload, Event: DownloadReadyEvent{}, session: &transferSession{offer: alignmentOffer(), websheetOpen: tt.portal}}
			result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
			if !isHTTPStatus(err, 503) || result.State != StatePendingActivation {
				t.Fatalf("state=%v err=%v", result.State, err)
			}
			if _, ok := result.Event.(ActivationPendingEvent); !ok {
				t.Fatalf("event=%T", result.Event)
			}
			request := ContinueRequest{}
			if tt.portal {
				request.Websheet = &WebsheetResult{Event: WebsheetEventCheckProfileServiceStatus}
			}
			result, err = client.Continue(t.Context(), result, request)
			if err != nil || result.State != StateDone || updates != 2 || queries != 1 || result.DownloadConfig.ProfileICCID != "installed" {
				t.Fatalf("state=%v updates=%d queries=%d iccid=%q err=%v", result.State, updates, queries, result.DownloadConfig.ProfileICCID, err)
			}
		})
	}
}

func TestInstalledProfileAuthFailurePreservesActivation(t *testing.T) {
	offer := alignmentOffer()
	offer.AuthToken = AuthToken{}
	client := newClient(Config{})
	result := &Result{State: StatePendingDownload, Event: DownloadReadyEvent{}, session: &transferSession{offer: offer}}
	result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
	if err == nil || result.State != StatePendingActivation || offer.TargetDevice.ICCID != "installed" {
		t.Fatalf("state=%v err=%v", result.State, err)
	}
	if _, ok := result.Event.(ActivationPendingEvent); !ok {
		t.Fatalf("event=%T", result.Event)
	}
	updates := 0
	client.runtime.httpClient = fakeDoer(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("operation") == string(OperationManage) {
			updates++
			return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
		}
		return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
	})
	client.transfer.entitlement.Server = "https://carrier.example"
	offer.AuthToken = AuthToken{Value: "renewed"}
	result, err = client.Continue(t.Context(), result, ContinueRequest{})
	if err != nil || result.State != StateDone || updates != 1 || result.DownloadConfig.ProfileICCID != "installed" {
		t.Fatalf("state=%v updates=%d iccid=%q err=%v", result.State, updates, result.DownloadConfig.ProfileICCID, err)
	}
}

func TestActivationDoesNotApplyDownloadRetryAfter(t *testing.T) {
	var operations []Operation
	queries := 0
	client := newClient(Config{
		Entitlement: Entitlement{Server: "https://carrier.example", TransferType: TransferTypeTS43V8, SupportRetryAfter: true, UseServerRetryAfterDelay: true},
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("activation applied download Retry-After policy")
			return nil
		},
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			op := Operation(req.URL.Query().Get("operation"))
			operations = append(operations, op)
			if op == OperationManage {
				if req.URL.Query().Get("operation_type") != "4" {
					t.Error("incorrect post-installation update type")
				}
				return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
			}
			if op != OperationAcquireConf {
				t.Fatalf("unexpected operation %q", op)
			}
			queries++
			if queries == 1 {
				resp := response(503, "")
				resp.Header = http.Header{"Retry-After": []string{"1"}}
				return resp, nil
			}
			return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
		}),
	})
	result := &Result{State: StatePendingDownload, session: &transferSession{offer: alignmentOffer()}}
	result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "8920000000000000000"})
	if !isHTTPStatus(err, 503) || result == nil || result.State != StatePendingActivation || queries != 1 {
		t.Fatalf("activation result=%v error=%v queries=%d", result, err, queries)
	}
	result, err = client.Continue(t.Context(), result, ContinueRequest{})
	if err != nil || result.State != StateDone || !slices.Equal(operations, []Operation{OperationManage, OperationAcquireConf, OperationAcquireConf}) {
		t.Fatalf("resumed state=%v error=%v operations=%v", result.State, err, operations)
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

func TestCompleteActivationPollsConfigurationAfterUpdateSubscription(t *testing.T) {
	var updateTokens []string
	var eligibilityFetched bool
	var configurationFetched bool
	sourceIdentity := Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}
	targetIdentity := Identity{ICCID: "8900000000000000000", IMSI: "311480987654321", MCC: "311", MNC: "480"}
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case "":
				if got := query.Get("terminal_id"); got != "222222222222222" {
					t.Errorf("authentication terminal_id = %q, want target IMEI", got)
				}
				wantEAPID, err := IMSIEAPIdentity(mccMNC(targetIdentity), targetIdentity.IMSI, DefaultEAPAKARealm)
				if err != nil {
					t.Fatalf("IMSIEAPIdentity() error = %v", err)
				}
				if got := query.Get("EAP_ID"); got != wantEAPID {
					t.Errorf("authentication EAP_ID = %q, want target identity %q", got, wantEAPID)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				eligibilityFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Errorf("CheckEligibility token = %q, want new-auth", got)
				}
				if got := query.Get("IMSI"); got != targetIdentity.IMSI {
					t.Errorf("CheckEligibility IMSI = %q, want target IMSI", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("operation_type"); got != OperationTypeUpdateSubscription.String() {
					t.Errorf("operation_type = %q, want %s", got, OperationTypeUpdateSubscription)
				}
				if got := query.Get("terminal_id"); got != "222222222222222" {
					t.Errorf("terminal_id = %q, want target IMEI", got)
				}
				if got := query.Get("temporary_token"); got != "" {
					t.Errorf("temporary_token = %q, want empty", got)
				}
				updateTokens = append(updateTokens, query.Get("token"))
				if query.Get("token") == "old-auth" {
					if got := query.Get("IMSI"); got != sourceIdentity.IMSI {
						t.Errorf("initial UpdateSubscription IMSI = %q, want source IMSI", got)
					}
					return response(511, ""), nil
				}
				if got := query.Get("IMSI"); got != targetIdentity.IMSI {
					t.Errorf("retried UpdateSubscription IMSI = %q, want target IMSI", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				configurationFetched = true
				if got := query.Get("IMSI"); got != targetIdentity.IMSI {
					t.Errorf("AcquireConfiguration IMSI = %q, want target IMSI", got)
				}
				if got := query.Get("terminal_eid"); got != "89049032000001000000000000000000" {
					t.Errorf("terminal_eid = %q, want target EID", got)
				}
				if got := query.Get("target_terminal_iccid"); got != "" {
					t.Errorf("target_terminal_iccid = %q, want omitted", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target: Endpoint{
			Channel: identityChannel{identity: targetIdentity},
			Device:  Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		},
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result := &Result{
		State: StatePendingDownload,
		session: &transferSession{
			offer: &transferOffer{
				SourceIdentity: sourceIdentity,
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "old-auth"},
			},
		},
		// The active subscription identity is authoritative if it differs from
		// the ICCID reported before profile activation.
		DownloadConfig: DownloadConfig{ProfileICCID: "8999999999999999999"},
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if got := strings.Join(updateTokens, ","); got != "old-auth,new-auth" {
		t.Errorf("UpdateSubscription tokens = %q, want old-auth,new-auth", got)
	}
	if !eligibilityFetched {
		t.Fatal("CheckEligibility was not called during token refresh")
	}
	if !configurationFetched {
		t.Fatal("AcquireConfiguration was not called after subscription update")
	}
	if got := result.DownloadConfig.ProfileICCID; got != targetIdentity.ICCID {
		t.Errorf("DownloadConfig.ProfileICCID = %q, want active target ICCID %q", got, targetIdentity.ICCID)
	}
	if result.State != StateDone {
		t.Errorf("State = %d, want %d", result.State, StateDone)
	}
}
