package ts43

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestContinueAfterConfigurationHTTPError(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed bool
		wantState State
	}{
		{"download preparation", false, StatePendingDownload}, {"activation confirmation", true, StateDone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []Operation
			queries := 0
			client := newClient(Config{Entitlement: Entitlement{Server: "https://example.test", Channel: EntitlementChannelSamsung, TransferType: TransferTypeTS43V8}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				op := Operation(req.URL.Query().Get("operation"))
				calls = append(calls, op)
				if op == OperationManage {
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				}
				queries++
				if queries == 1 {
					return response(http.StatusServiceUnavailable, ""), nil
				}
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.test"}}}}`), nil
			})})
			result := &Result{State: StatePendingDownload, session: &transferSession{offer: &transferOffer{SourceIdentity: Identity{MCC: "450", MNC: "05", IMSI: "450051234567890"}, AuthToken: AuthToken{Value: "auth"}}}}
			var err error
			if tt.installed {
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
			} else {
				result, err = client.continueConfiguration(t.Context(), result)
			}
			if !isHTTPStatus(err, http.StatusServiceUnavailable) {
				t.Fatalf("poll error=%v", err)
			}
			wantPending := StatePendingConfiguration
			if tt.installed {
				wantPending = StatePendingActivation
			}
			if result.State != wantPending {
				t.Fatalf("state=%v, want %v", result.State, wantPending)
			}
			result, err = client.Continue(t.Context(), result, ContinueRequest{})
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := []Operation{OperationAcquireConf, OperationAcquireConf}
			if tt.installed {
				wantCalls = append([]Operation{OperationManage}, wantCalls...)
			}
			if !slices.Equal(calls, wantCalls) || result.State != tt.wantState {
				t.Errorf("calls=%v state=%v, want %v %v", calls, result.State, wantCalls, tt.wantState)
			}
		})
	}
}

func TestWebsheetWaitsForConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name  string
		event WebsheetCallbackEvent
	}{{"finish flow", WebsheetEventFinishFlow}, {"check status", WebsheetEventCheckProfileServiceStatus}} {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{Entitlement: Entitlement{Server: "https://example.test", Channel: EntitlementChannelSamsung}, HTTPClient: &queueDoer{bodies: []string{`{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":2}}}`}}})
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

type failingIdentityChannel struct {
	fakeChannel
	identity *Identity
	err      error
}

func (c failingIdentityChannel) Identity(context.Context) (*Identity, error) {
	return c.identity, c.err
}

func TestTransferRequiresSourceIdentity(t *testing.T) {
	sentinel := errors.New("SIM unavailable")
	for _, tt := range []struct {
		name    string
		channel Channel
		cause   error
	}{
		{"no channel", nil, nil}, {"no identity", failingIdentityChannel{}, nil}, {"read error", failingIdentityChannel{err: sentinel}, sentinel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{Source: Endpoint{Channel: tt.channel}})
			t.Cleanup(client.CloseIdleConnections)
			_, err := client.Transfer(t.Context())
			if err == nil {
				t.Fatal("missing identity accepted")
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("error lost cause: %v", err)
			}
		})
	}
}
