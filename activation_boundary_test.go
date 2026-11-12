package ts43

import (
	"net/http"
	"testing"
)

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
