package ts43

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestDownloadActivationStateControlsCompletion(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name        string
			channel     EntitlementChannel
			status      int
			delayed     bool
			expireToken bool
			wantUpdates int
		}{
			{name: "Google activated delayed download", status: 1, delayed: true},
			{name: "Google activated without surviving credentials", status: 1, delayed: true, expireToken: true},
			{name: "Google reusable download needs activation", status: 3, delayed: true, wantUpdates: 1},
			{name: "Google direct download needs activation", wantUpdates: 1},
			{name: "Samsung activated download retains update", channel: EntitlementChannelSamsung, status: 1, delayed: true, wantUpdates: 1},
			{name: "Samsung activating download retains update", channel: EntitlementChannelSamsung, status: 2, delayed: true, wantUpdates: 1},
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
						Channel:      tt.channel,
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
				if result.State == StatePendingDelayedDownload {
					result, err = client.Continue(t.Context(), result, ContinueRequest{})
				}
				if err != nil || result.State != StatePendingDownload {
					t.Fatalf("download state=%v err=%v", result.State, err)
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
