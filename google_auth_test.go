package ts43

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

func TestGoogleInvalidUserInputPreservesCredentials(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, temporary := range []bool{false, true} {
			for _, rotate := range []bool{false, true} {
				t.Run(fmt.Sprintf("post=%v/temporary=%v/rotate=%v", post, temporary, rotate), func(t *testing.T) {
					now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
					offer := alignmentOffer()
					offer.AuthToken = AuthToken{Value: "auth", ExpiresAt: now.Add(2 * time.Hour), Cookies: []string{"sticky=keep", "remove=gone", "sid=old"}}
					offer.TemporaryToken = TemporaryToken{Value: "temporary", ExpiresAt: now.Add(time.Hour), Cookies: offer.AuthToken.Cookies, Targets: []Operation{OperationManage, OperationAcquireConf}}
					transferType := TransferTypeOldTerminalIDs
					if temporary {
						transferType = TransferTypeTemporaryToken
					}
					calls := 0
					client := newClient(Config{
						Now:         func() time.Time { return now },
						Entitlement: Entitlement{Server: "https://carrier.example/ts43", Channel: EntitlementChannelGoogle, TransferType: transferType, UsePost: post},
						HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
							calls++
							fields := alignmentRequestFields(t, req)
							if fields.Get("operation") != string(OperationManage) {
								t.Fatalf("unexpected operation %q", fields.Get("operation"))
							}
							switch calls {
							case 1:
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":8,"MSG":{"Message":"Enter PIN","Accept_btn":1,"Accept_freetext":1}}}`), nil
							case 2:
								if fields.Get("MSG_response") != "bad" {
									t.Errorf("invalid answer = %q", fields.Get("MSG_response"))
								}
								token := ""
								if rotate {
									token = `"TOKEN":{"token":"rotated","Validity":600},`
								}
								resp := responseWithCookie(200, `{`+token+`"ap2009":{"OperationResult":104,"GeneralErrorText":"invalid PIN"}}`, "sid=new; Path=/")
								resp.Header.Add("Set-Cookie", "remove=; Max-Age=0; Path=/")
								return resp, nil
							case 3:
								wantAuth, wantTemporary := "auth", ""
								if rotate {
									wantAuth = "rotated"
								}
								if temporary {
									wantAuth, wantTemporary = "", "temporary"
								}
								if fields.Get("token") != wantAuth || fields.Get("temporary_token") != wantTemporary {
									t.Errorf("retry credentials: token=%q temporary_token=%q", fields.Get("token"), fields.Get("temporary_token"))
								}
								for name, value := range map[string]string{"sticky": "keep", "sid": "new"} {
									cookie, err := req.Cookie(name)
									if err != nil || cookie.Value != value {
										t.Errorf("retry cookie %s=%v err=%v, want %q", name, cookie, err, value)
									}
								}
								if len(req.Cookies()) != 2 {
									t.Errorf("retry cookies = %v, want sticky and sid", req.Cookies())
								}
								if fields.Get("MSG_response") != "1234" || fields.Get("MSG_btn") != MessageButtonAccepted.String() {
									t.Error("corrected answer was not sent")
								}
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`), nil
							default:
								t.Fatalf("unexpected HTTP call %d", calls)
								return nil, nil
							}
						}),
					})
					result, err := client.activate(t.Context(), offer, UserInputResult{Button: MessageButtonUnknown})
					if err != nil {
						t.Fatal(err)
					}
					result, err = client.Continue(t.Context(), result, ContinueRequest{UserInput: &UserInputResult{Button: MessageButtonAccepted, Response: "bad"}})
					if err != nil || result.State != StatePendingUserInput {
						t.Fatalf("invalid input result=%v err=%v", result, err)
					}
					if event, ok := result.Event.(UserInputEvent); !ok || event.Message.Text != "Enter PIN" {
						t.Fatalf("prompt = %#v", result.Event)
					}
					wantAuth, wantExpiry := "auth", now.Add(2*time.Hour)
					if rotate {
						wantAuth, wantExpiry = "rotated", now.Add(10*time.Minute)
					}
					if offer.AuthToken.Value != wantAuth || !offer.AuthToken.ExpiresAt.Equal(wantExpiry) {
						t.Errorf("saved auth token = %+v", offer.AuthToken)
					}
					if temporary && (offer.TemporaryToken.Value != "temporary" || !offer.TemporaryToken.ExpiresAt.Equal(now.Add(time.Hour)) || !slices.Equal(offer.TemporaryToken.Targets, []Operation{OperationManage, OperationAcquireConf})) {
						t.Errorf("temporary token changed: %+v", offer.TemporaryToken)
					}
					result, err = client.Continue(t.Context(), result, ContinueRequest{UserInput: &UserInputResult{Button: MessageButtonAccepted, Response: "1234"}})
					if err != nil || result.State != StatePendingDownload || calls != 3 {
						t.Fatalf("corrected input result=%v calls=%d err=%v", result, calls, err)
					}
				})
			}
		}
	}
}

func TestGoogleAuthenticationRetryPreservesIdentity(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, transfer := range []bool{false, true} {
			for _, tt := range []struct {
				name       string
				limit      int
				failures   int
				status     int
				wantCalls  int
				wantStatus int
			}{
				{name: "retry succeeds", limit: 1, failures: 1, status: 511, wantCalls: 2},
				{name: "retry budget", limit: 2, failures: 10, status: 511, wantCalls: 3, wantStatus: 511},
				{name: "retries disabled", limit: -1, failures: 10, status: 511, wantCalls: 1, wantStatus: 511},
				{name: "other error", limit: 2, failures: 10, status: 503, wantCalls: 1, wantStatus: 503},
			} {
				t.Run(fmt.Sprintf("post=%v/transfer=%v/%s", post, transfer, tt.name), func(t *testing.T) {
					offer := alignmentOffer()
					wantEAPID, err := IMSIEAPIdentity(mccMNC(offer.SourceIdentity), offer.SourceIdentity.IMSI, DefaultEAPAKARealm)
					if err != nil {
						t.Fatal(err)
					}
					calls := 0
					client := newClient(Config{
						Entitlement: Entitlement{Server: "https://carrier.example/ts43", Channel: EntitlementChannelGoogle, UsePost: post, AutoRetry511Limit: tt.limit},
						Source:      Endpoint{Channel: identityChannel{identity: offer.SourceIdentity}, Device: offer.SourceDevice},
						Target:      Endpoint{Device: offer.TargetDevice},
						HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
							fields := alignmentRequestFields(t, req)
							switch Operation(fields.Get("operation")) {
							case "":
								calls++
								if fields.Get("EAP_ID") != wantEAPID || fields.Get("terminal_id") != offer.SourceDevice.IMEI || fields.Get("token") != "" || fields.Get("temporary_token") != "" {
									t.Errorf("authentication identity = %v", fields)
								}
								if got, want := req.Header.Get("Accept"), EAPRelayContentType+", "+ODSAResponseAccept; got != want {
									t.Errorf("Accept = %q, want %q", got, want)
								}
								if calls <= tt.failures {
									return response(tt.status, ""), nil
								}
								return response(200, `{"TOKEN":{"token":"authenticated","Validity":3600}}`), nil
							case OperationCheck:
								return response(200, `{"ap2009":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
							case OperationManage:
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
							default:
								t.Fatalf("unexpected operation %q", fields.Get("operation"))
								return nil, nil
							}
						}),
					})
					if transfer {
						var result *Result
						result, err = client.Transfer(t.Context())
						if tt.wantStatus == 0 && err == nil && result.State != StateDone {
							t.Errorf("transfer state = %v, want done", result.State)
						}
					} else {
						var token AuthToken
						token, err = client.Authenticate(t.Context(), offer.SourceIdentity, offer.SourceDevice)
						if tt.wantStatus == 0 && err == nil && token.Value != "authenticated" {
							t.Errorf("authentication token = %q", token.Value)
						}
					}
					if tt.wantStatus == 0 && err != nil || tt.wantStatus != 0 && !isHTTPStatus(err, tt.wantStatus) {
						t.Errorf("error = %v, want HTTP status %d", err, tt.wantStatus)
					}
					if calls != tt.wantCalls {
						t.Errorf("authentication calls = %d, want %d", calls, tt.wantCalls)
					}
				})
			}
		}
	}
}

func TestGoogleAuthenticationRetryHonorsCatalogZero(t *testing.T) {
	retryLimit, calls := 0, 0
	offer := alignmentOffer()
	client := newClient(Config{
		Entitlements: &EntitlementConfigs{records: []entitlementRecord{{
			MCCMNC:                    mccMNC(offer.SourceIdentity),
			EntitlementServer:         "https://carrier.example/ts43",
			SupportEsimToEsimTransfer: true,
			TransferTypeEsim:          int(TransferTypeOldTerminalIDs),
			AutoRetry511Limit:         &retryLimit,
		}}},
		Source: Endpoint{Channel: identityChannel{identity: offer.SourceIdentity}, Device: offer.SourceDevice},
		Target: Endpoint{Device: offer.TargetDevice},
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			calls++
			return response(http.StatusNetworkAuthenticationRequired, ""), nil
		}),
	})
	if _, err := client.Transfer(t.Context()); !isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) {
		t.Fatalf("transfer error = %v, want HTTP 511", err)
	}
	if calls != 1 {
		t.Errorf("authentication calls = %d, want 1", calls)
	}
}
