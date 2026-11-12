package ts43

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

func alignmentOffer() *transferOffer {
	return &transferOffer{
		SourceIdentity: Identity{MCC: "234", MNC: "20", IMSI: "234201234567890", ICCID: "8910000000000000000"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EntitlementProtocol: 1},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "auth"},
	}
}

func TestConfigurationReadinessDependsOnStage(t *testing.T) {
	for _, tt := range []struct {
		name                                 string
		status                               int
		download, installed, denied, pending bool
	}{
		{name: "Google reusable download", status: 3, download: true},

		{name: "Google activating waits", status: 2, download: true, pending: true},
		{name: "deactivated without download", status: 3, denied: true},
		{name: "no reuse cannot download", status: 4, download: true, denied: true},
		{name: "installed deactivated cannot confirm activation", status: 3, download: true, installed: true, denied: true},
		{name: "installed activating cannot confirm activation", status: 2, download: true, installed: true, pending: true},
		{name: "installed activated", status: 1, installed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			waits := 0
			pause := errors.New("poll paused")
			download := ""
			if tt.download {
				download = `,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}`
			}
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, Sleep: func(context.Context, time.Duration) error { waits++; return pause }, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				if Operation(req.URL.Query().Get("operation")) != OperationAcquireConf {
					t.Fatalf("unexpected operation %s", req.URL.Query().Get("operation"))
				}
				return response(200, fmt.Sprintf(`{"APPLICATION":{"OperationResult":1,"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":%d,"PollingInterval":1%s}}]}}`, tt.status, download)), nil
			})})
			result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: alignmentOffer()}}
			if tt.installed {
				result.State = StatePendingActivation
				result.UpdateSubscription = &ODSAResponse{OperationResult: OperationResultSuccess, SubscriptionResult: SubscriptionResultDone}
			}
			next, err := client.Continue(t.Context(), result, ContinueRequest{})
			var denied *CarrierDeniedError
			if errors.As(err, &denied) != tt.denied {
				t.Fatalf("denial=%v, want %t", err, tt.denied)
			}
			if tt.pending {
				if !errors.Is(err, pause) || waits != 1 {
					t.Fatalf("pending error=%v waits=%d", err, waits)
				}
				return
			}
			if tt.denied {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := StatePendingDownload
			if tt.installed {
				want = StateDone
			}
			if next.State != want {
				t.Fatalf("state=%v, want %v", next.State, want)
			}
		})
	}
}

func TestConfigurationRejectsExplicitOperationFailure(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: &queueDoer{bodies: []string{`{"APPLICATION":{"OperationResult":100,"GeneralErrorText":"request rejected","PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`}}})
			result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: alignmentOffer()}}
			if installed {
				result.State = StatePendingActivation
				result.UpdateSubscription = &ODSAResponse{OperationResult: OperationResultSuccess, SubscriptionResult: SubscriptionResultDone}
			}
			next, err := client.Continue(t.Context(), result, ContinueRequest{})
			var denied *CarrierDeniedError
			if !errors.As(err, &denied) || denied.Result.OperationResult != 100 {
				t.Fatalf("error=%v, want explicit denial", err)
			}
			if next.State != result.State {
				t.Fatalf("state=%v", next.State)
			}
		})
	}
}

func TestConfigurationRetainsRotatedCredentials(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(fmt.Sprint(temporary), func(t *testing.T) {
			offer := alignmentOffer()
			if temporary {
				offer.TemporaryToken = TemporaryToken{Value: "temp", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
			}
			queries := 0
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, Sleep: func(context.Context, time.Duration) error { return nil }, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				queries++
				if queries > 2 {
					t.Fatal("unexpected repeated query")
				}
				if queries == 1 {
					return responseWithCookie(200, `{"TOKEN":{"token":"rotated","Validity":3600},"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":1}}}`, "SESSION=new"), nil
				}
				if got := req.Header.Get("Cookie"); got != "SESSION=new" {
					t.Errorf("cookie=%q", got)
				}
				if temporary {
					if req.URL.Query().Get("temporary_token") != "temp" {
						t.Error("temporary token changed to authentication token")
					}
				} else if req.URL.Query().Get("token") != "rotated" {
					t.Error("rotated token not used")
				}
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
			})})
			if temporary {
				client.transfer.entitlement.TransferType = TransferTypeTemporaryToken
			}
			result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: offer}}
			next, err := client.Continue(t.Context(), result, ContinueRequest{})
			if err != nil || next.State != StatePendingDownload || queries != 2 {
				t.Fatalf("state=%v queries=%d error=%v", next.State, queries, err)
			}
			if offer.AuthToken.Value != "rotated" {
				t.Error("refreshed authentication token was lost")
			}
		})
	}
}

func TestRenewTemporaryTokenWithoutSourceReauthentication(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for _, rejected := range []bool{false, true} {
		t.Run(fmt.Sprint(rejected), func(t *testing.T) {
			offer := alignmentOffer()
			offer.AuthToken.ExpiresAt = now.Add(time.Hour)
			offer.TemporaryToken = TemporaryToken{Value: "expired", ExpiresAt: now.Add(-time.Second)}
			if rejected {
				offer.TemporaryToken.ExpiresAt = now.Add(time.Minute)
			}
			var ops []Operation
			client := newClient(Config{Now: func() time.Time { return now }, Entitlement: Entitlement{Server: "https://carrier.example", TransferType: TransferTypeTemporaryToken}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				query := req.URL.Query()
				op := Operation(query.Get("operation"))
				ops = append(ops, op)
				switch op {
				case OperationTempToken:
					if query.Get("token") != "auth" {
						t.Error("valid authentication token not reused")
					}
					return response(200, `{"APPLICATION":{"OperationResult":1,"TemporaryToken":"renewed","TemporaryTokenExpiry":"2030-01-01T00:00:00Z","OperationTargets":"ManageSubscription,AcquireConfiguration"}}`), nil
				case OperationAcquireConf:
					if query.Get("temporary_token") == "expired" {
						return response(511, ""), nil
					}
					return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
				default:
					t.Fatalf("unexpected reauthentication operation %q", op)
					return nil, errors.New("unexpected operation")
				}
			})})
			result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: offer}}
			next, err := client.Continue(t.Context(), result, ContinueRequest{})
			if err != nil || next.State != StatePendingDownload {
				t.Fatalf("result=%v error=%v", next.State, err)
			}
			want := []Operation{OperationTempToken, OperationAcquireConf}
			if rejected {
				want = append([]Operation{OperationAcquireConf}, want...)
			}
			if !slices.Equal(ops, want) {
				t.Fatalf("operations=%v, want %v", ops, want)
			}
		})
	}
}

func TestContinuePendingConfigurationOnlyQueriesConfiguration(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("operation") != string(OperationAcquireConf) {
			t.Fatal("subscription submitted again")
		}
		return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":3,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
	})})
	next, err := client.Continue(t.Context(), &Result{State: StatePendingConfiguration, session: &transferSession{offer: alignmentOffer()}}, ContinueRequest{})
	if err != nil || next.State != StatePendingDownload {
		t.Fatalf("result=%v error=%v", next.State, err)
	}
}

func TestAndroidOptionalHintsFallback(t *testing.T) {
	for _, body := range []string{
		`{"TOKEN":{"token":"auth","Validity":"invalid"},"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":"invalid","PollingIntervalUnit":"invalid"}}}`,
		`<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="-1"/></characteristic><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="2"/><parm name="PollingInterval" value="invalid"/></characteristic></characteristic></wap-provisioningdoc>`,
	} {
		var resp ODSAResponse
		if err := resp.UnmarshalBinary([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if resp.AuthTokenValidity != 3600 || resp.PollingIntervalSet {
			t.Fatalf("validity=%d pollingSet=%t", resp.AuthTokenValidity, resp.PollingIntervalSet)
		}
		client := newClient(Config{Entitlement: Entitlement{PollInterval: 2 * time.Minute}})
		if interval, ok := client.pollInterval(&resp); !ok || interval != 2*time.Minute {
			t.Fatalf("Google polling=%s enabled=%t", interval, ok)
		}

	}
}

func TestInvalidUserInputRetainsPrompt(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: &queueDoer{bodies: []string{
		`{"APPLICATION":{"OperationResult":1,"SubscriptionResult":8,"MSG":{"Message":"Enter PIN","Accept_btn":1,"Accept_freetext":1}}}`,
		`{"APPLICATION":{"OperationResult":104,"GeneralErrorText":"invalid PIN"}}`,
		`{"APPLICATION":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`,
	}}})
	result, err := client.activate(t.Context(), alignmentOffer(), UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatal(err)
	}
	result, err = client.Continue(t.Context(), result, ContinueRequest{UserInput: &UserInputResult{Button: MessageButtonAccepted, Response: "bad"}})
	if err != nil || result.State != StatePendingUserInput {
		t.Fatalf("invalid input state=%v error=%v", result.State, err)
	}
	if event, ok := result.Event.(UserInputEvent); !ok || event.Message.Text != "Enter PIN" {
		t.Fatalf("prompt=%#v", result.Event)
	}
	result, err = client.Continue(t.Context(), result, ContinueRequest{UserInput: &UserInputResult{Button: MessageButtonAccepted, Response: "1234"}})
	if err != nil || result.State != StatePendingDownload {
		t.Fatalf("corrected input state=%v error=%v", result.State, err)
	}
}

func TestDiscoveryRetainsPollingDefaultsAndCarrierRetryLimit(t *testing.T) {
	for _, retry := range []int{0, 2} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			configs := &EntitlementConfigs{records: []entitlementRecord{{MCCMNC: "23420", EntitlementServer: "https://carrier.example", SupportEsimToEsimTransfer: true, TransferTypeEsim: 2, AutoRetry511Limit: &retry}}}
			client := newClient(Config{Entitlements: configs})
			if err := client.discoverEntitlement(Identity{MCC: "234", MNC: "20"}); err != nil {
				t.Fatal(err)
			}
			ent := client.transfer.entitlement
			if ent.PollTimeout != 10*time.Minute || ent.PollInterval != time.Minute || ent.AutoRetry511Limit != retry {
				t.Fatalf("discovered defaults: interval=%s timeout=%s retries=%d", ent.PollInterval, ent.PollTimeout, ent.AutoRetry511Limit)
			}
		})
	}
}
