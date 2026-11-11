package ts43

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPostInstallationRequestFields(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name         string
			channel      EntitlementChannel
			transferType TransferType
			extraTarget  bool
		}{
			{name: "Google temporary token", transferType: TransferTypeTemporaryToken},
			{name: "Google extra target fields", channel: EntitlementChannelGoogle, transferType: TransferTypeTemporaryToken, extraTarget: true},
			{name: "Google source credential", channel: EntitlementChannelGoogle, transferType: TransferTypeTS43V8},
			{name: "Samsung temporary token", channel: EntitlementChannelSamsung, transferType: TransferTypeTemporaryToken, extraTarget: true},
		} {
			t.Run(fmt.Sprintf("post_%v/%s", post, tt.name), func(t *testing.T) {
				offer := alignmentOffer()
				offer.TargetDevice.Model = "Target"
				offer.TargetDevice.SerialNumber = "target-serial"
				if tt.channel != EntitlementChannelSamsung {
					offer.TargetDevice.IMEIs = []string{offer.TargetDevice.IMEI}
				}
				offer.TemporaryToken = TemporaryToken{Value: "temporary", Targets: []Operation{OperationManage, OperationAcquireConf}}
				transfers, updates, queries := 0, 0, 0
				client := newClient(Config{
					Entitlement: Entitlement{
						Server:                     "https://carrier.example",
						Channel:                    tt.channel,
						TransferType:               tt.transferType,
						UsePost:                    post,
						AppName:                    "transfer-app",
						SetTargetTerminalFields:    tt.extraTarget,
						SamsungExtraTerminalParams: tt.extraTarget,
					},
					HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
						fields := alignmentRequestFields(t, req)
						initial := fields.Get("operation_type") == "3"
						wantApp := "transfer-app"
						if !initial && tt.channel != EntitlementChannelSamsung {
							wantApp = ""
							for key := range fields {
								if strings.HasPrefix(key, "target_terminal_") {
									t.Errorf("post-installation request contains %s", key)
								}
							}
						}
						if got := fields.Get("app_name"); got != wantApp {
							t.Errorf("app_name=%q, want %q", got, wantApp)
						}
						if !initial {
							if got := fields.Get("terminal_id"); got != offer.TargetDevice.IMEI {
								t.Errorf("terminal_id=%q, want target IMEI", got)
							}
							if got := fields.Get("terminal_eid"); got != offer.TargetDevice.EID {
								t.Errorf("terminal_eid=%q, want target EID", got)
							}
							if tt.channel == EntitlementChannelSamsung && fields.Get("target_terminal_id") != offer.TargetDevice.IMEI {
								t.Error("Samsung request dropped target identity")
							}
						}
						switch Operation(fields.Get("operation")) {
						case OperationManage:
							if initial {
								transfers++
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`), nil
							}
							updates++
							if fields.Get("operation_type") != "4" || fields.Get("terminal_iccid") != "installed" {
								t.Error("update does not identify the installed profile")
							}
							return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
						case OperationAcquireConf:
							queries++
							if queries == 1 {
								return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":0}}}`), nil
							}
							return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
						default:
							t.Fatalf("unexpected operation %q", fields.Get("operation"))
							return nil, nil
						}
					}),
				})
				result, err := client.activate(t.Context(), offer, UserInputResult{Button: MessageButtonUnknown})
				if err != nil || result.State != StatePendingDownload {
					t.Fatalf("download result=%v err=%v", result, err)
				}
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
				if err != nil || result.State != StatePendingActivation {
					t.Fatalf("activation result=%v err=%v", result, err)
				}
				result, err = client.Continue(t.Context(), result, ContinueRequest{})
				if err != nil || result.State != StateDone || transfers != 1 || updates != 1 || queries != 2 {
					t.Fatalf("result=%v transfers=%d updates=%d queries=%d err=%v", result, transfers, updates, queries, err)
				}
			})
		}
	}
}

func TestUnsupportedActivationUpdateRemainsPending(t *testing.T) {
	for _, channel := range []EntitlementChannel{EntitlementChannelGoogle, EntitlementChannelSamsung} {
		t.Run(string(channel), func(t *testing.T) {
			calls := 0
			client := newClient(Config{
				Entitlement: Entitlement{Server: "https://carrier.example", Channel: channel},
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
		})
	}
}
