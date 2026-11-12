package ts43

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
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

func TestContinuePendingConfigurationAndActivation(t *testing.T) {
	for _, tt := range []struct {
		name          string
		afterDownload bool
		completeAgain bool
	}{
		{"before download", false, false}, {"after download continue", true, false}, {"after download CompleteActivation", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []Operation
			c := newClient(Config{Entitlement: Entitlement{Server: "https://example.test", TransferType: TransferTypeTS43V8}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				op := Operation(req.URL.Query().Get("operation"))
				calls = append(calls, op)
				if op == OperationManage {
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				}
				if slices.Contains(calls[:len(calls)-1], OperationAcquireConf) {
					return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.test"}}}}`), nil
				}
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":0}}}`), nil
			})})
			result := &Result{State: StatePendingDownload, session: &transferSession{offer: &transferOffer{SourceIdentity: Identity{MCC: "450", MNC: "05", IMSI: "450051234567890"}, AuthToken: AuthToken{Value: "token"}}}}
			var err error
			if tt.afterDownload {
				result, err = c.CompleteActivation(context.Background(), result, ActivationResult{ICCID: "new-iccid"})
			} else {
				result, err = c.continueConfiguration(context.Background(), result)
			}
			wantPending := StatePendingConfiguration
			if tt.afterDownload {
				wantPending = StatePendingActivation
			}
			if err != nil || result.State != wantPending {
				t.Fatalf("pending result = %+v, %v", result, err)
			}
			if tt.completeAgain {
				result, err = c.CompleteActivation(context.Background(), result, ActivationResult{})
			} else {
				result, err = c.Continue(context.Background(), result, ContinueRequest{})
			}
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := []Operation{OperationAcquireConf, OperationAcquireConf}
			wantState := StatePendingDownload
			if tt.afterDownload {
				wantCalls = append([]Operation{OperationManage}, wantCalls...)
				wantState = StateDone
			}
			if !slices.Equal(calls, wantCalls) || result.State != wantState {
				t.Errorf("calls=%v state=%v, want %v %v", calls, result.State, wantCalls, wantState)
			}
		})
	}
}

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
						Entitlement: Entitlement{Server: "https://carrier.example/ts43", TransferType: transferType, UsePost: post},
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

func TestTransferPropagatesEligibilityTarget(t *testing.T) {
	for _, post := range []bool{false, true} {
		t.Run(fmt.Sprint(post), func(t *testing.T) {
			offer := alignmentOffer()
			offer.TargetDevice.IMEIs = []string{"222222222222222", "333333333333333"}
			var operations []Operation
			client := newClient(Config{
				Source:      Endpoint{Channel: fakeChannel{}, Device: offer.SourceDevice},
				Target:      Endpoint{Device: offer.TargetDevice},
				Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post, Version: "14.0", TransferType: TransferTypeTS43V8},
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					fields := alignmentRequestFields(t, req)
					op := Operation(fields.Get("operation"))
					operations = append(operations, op)
					switch op {
					case "":
						return response(200, `{"TOKEN":{"token":"auth","Validity":3600}}`), nil
					case OperationCheck:
						if fields.Get("target_terminal_id") != "222222222222222,333333333333333" {
							t.Error("Transfer dropped eligibility target IMEIs")
						}
						return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
					case OperationManage:
						return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`), nil
					default:
						t.Fatalf("unexpected operation %q", op)
						return nil, nil
					}
				}),
			})
			result, err := client.Transfer(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if result.State != StatePendingDownload || !slices.Equal(operations, []Operation{"", OperationCheck, OperationManage}) {
				t.Fatalf("state=%v operations=%v", result.State, operations)
			}
		})
	}
}

func TestDownloadConfigurationRetryAfter(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, temporary := range []bool{false, true} {
			for _, tt := range []struct {
				name       string
				support    bool
				server     bool
				status     int
				retryAfter string
				wantDelay  []time.Duration
			}{
				{"carrier interval", true, false, 503, "7", []time.Duration{2 * time.Minute}},
				{"server seconds", true, true, 503, "7", []time.Duration{7 * time.Second}},
				{"zero delay", true, true, 503, "0", []time.Duration{0}},
				{"missing header", true, true, 503, "", []time.Duration{2 * time.Minute}},
				{"malformed header", true, true, 503, "later", []time.Duration{2 * time.Minute}},
				{"HTTP date", true, true, 503, "Thu, 12 Nov 2026 12:00:00 GMT", []time.Duration{2 * time.Minute}},
				{"negative delay", true, true, 503, "-1", []time.Duration{2 * time.Minute}},
				{"integer overflow", true, true, 503, "9223372036854775808", []time.Duration{2 * time.Minute}},
				{"duration overflow", true, true, 503, "9223372036854775807", []time.Duration{2 * time.Minute}},
				{"carrier disables retry", false, true, 503, "7", nil},
				{"other server error", true, true, 500, "7", nil},
				{"rate limit", true, true, 429, "7", nil},
			} {
				t.Run(fmt.Sprintf("post_%t/temporary_%t/%s", post, temporary, tt.name), func(t *testing.T) {
					now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
					offer := alignmentOffer()
					transferType := TransferTypeTS43V8
					if temporary {
						transferType = TransferTypeTemporaryToken
						offer.TemporaryToken = TemporaryToken{Value: "temporary", ExpiresAt: now.Add(time.Hour)}
					}
					var delays []time.Duration
					queries := 0
					client := newClient(Config{
						Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post, TransferType: transferType, SupportRetryAfter: tt.support, UseServerRetryAfterDelay: tt.server, PollInterval: 2 * time.Minute},
						Now:         func() time.Time { return now },
						Sleep: func(_ context.Context, delay time.Duration) error {
							delays = append(delays, delay)
							now = now.Add(delay)
							return nil
						},
						HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
							fields := alignmentRequestFields(t, req)
							if fields.Get("operation") != OperationAcquireConf.String() {
								t.Fatal("retry resubmitted subscription or refreshed credentials")
							}
							if temporary {
								if fields.Get("temporary_token") != "temporary" || fields.Has("token") {
									t.Error("retry changed temporary authentication")
								}
							} else if fields.Get("token") != "auth" || fields.Has("temporary_token") {
								t.Error("retry changed source authentication")
							}
							queries++
							if queries == 1 {
								resp := response(tt.status, "")
								resp.Header = make(http.Header)
								resp.Header.Set("Retry-After", tt.retryAfter)
								return resp, nil
							}
							if queries > 2 {
								t.Fatal("unexpected repeated configuration request")
							}
							return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
						}),
					})
					result := &Result{State: StatePendingConfiguration, session: &transferSession{offer: offer}}
					next, err := client.Continue(t.Context(), result, ContinueRequest{})
					if len(tt.wantDelay) == 0 {
						if !isHTTPStatus(err, tt.status) || next != result || next.State != StatePendingConfiguration || queries != 1 {
							t.Fatalf("unretried status: result=%v error=%v queries=%d", next, err, queries)
						}
						next, err = client.Continue(t.Context(), next, ContinueRequest{})
					}
					if err != nil || next == nil || next.State != StatePendingDownload || next.DownloadConfig.SMDPFQDN != "smdp.example" {
						t.Fatalf("result=%v error=%v", next, err)
					}
					if queries != 2 || !slices.Equal(delays, tt.wantDelay) {
						t.Fatalf("queries=%d delays=%v, want 2 and %v", queries, delays, tt.wantDelay)
					}
				})
			}
		}
	}
}

func TestDownloadRetryTimeoutAndCancellationRemainResumable(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled_%t", canceled), func(t *testing.T) {
			now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var operations []Operation
			queries := 0
			client := newClient(Config{
				Entitlement: Entitlement{Server: "https://carrier.example", TransferType: TransferTypeTS43V8, SupportRetryAfter: true, UseServerRetryAfterDelay: true, PollTimeout: 45 * time.Second},
				Now:         func() time.Time { return now },
				Sleep: func(ctx context.Context, delay time.Duration) error {
					if delay != 45*time.Second {
						t.Fatalf("delay=%s exceeds polling deadline", delay)
					}
					if canceled {
						cancel()
						return ctx.Err()
					}
					now = now.Add(delay)
					return nil
				},
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					op := Operation(req.URL.Query().Get("operation"))
					operations = append(operations, op)
					if op == OperationManage {
						return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":4}}`), nil
					}
					if op != OperationAcquireConf {
						t.Fatalf("unexpected operation %q", op)
					}
					queries++
					if queries == 1 {
						resp := response(503, "")
						resp.Header = http.Header{"Retry-After": []string{"300"}}
						return resp, nil
					}
					return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
				}),
			})
			result, err := client.activate(ctx, alignmentOffer(), UserInputResult{Button: MessageButtonUnknown})
			if err == nil || canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("poll error=%v", err)
			}
			if result == nil || result.State != StatePendingConfiguration || queries != 1 {
				t.Fatalf("pending result=%v queries=%d", result, queries)
			}
			var pending *Result
			pending, err = client.Continue(t.Context(), result, ContinueRequest{})
			if err != nil || pending != result || pending.State != StatePendingDownload {
				t.Fatalf("resumed result=%v error=%v", pending, err)
			}
			if !slices.Equal(operations, []Operation{OperationManage, OperationAcquireConf, OperationAcquireConf}) {
				t.Fatalf("resumed operations=%v", operations)
			}
		})
	}
}

func TestRegressionGoogleTransferBranches(t *testing.T) {
	tests := []struct {
		name, body string
		wantCalls  int
		wantState  State
		invalid    bool
	}{
		{"invalid message 104", `{"APPLICATION":{"OperationResult":104,"GeneralErrorText":"invalid input"}}`, 1, StatePendingUserInput, true},
		{"delayed without SMDS", `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":4}}`, 2, StatePendingDownload, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doer := &queueDoer{bodies: []string{tt.body, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`}}
			c := newClient(Config{HTTPClient: doer, Entitlement: Entitlement{Server: "https://example.test", TransferType: TransferTypeTS43V8}})
			result, err := c.activate(context.Background(), &transferOffer{SourceIdentity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"}, AuthToken: AuthToken{Value: "audit"}}, UserInputResult{Button: MessageButtonUnknown})
			if tt.invalid {
				if denied, ok := errors.AsType[*CarrierDeniedError](err); !ok || denied.Result.GeneralErrorText != "invalid input" {
					t.Fatalf("104 error = %v", err)
				}
				if result.Event != nil {
					t.Errorf("invalid input became event %T", result.Event)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tt.wantState || doer.calls != tt.wantCalls {
				t.Errorf("state=%d calls=%d, want state=%d calls=%d", result.State, doer.calls, tt.wantState, tt.wantCalls)
			}
		})
	}
}

func TestRegressionAfterSourceDeletion(t *testing.T) {
	tests := []struct {
		name     string
		transfer TransferType
	}{
		{"temporary token", TransferTypeTemporaryToken},
		{"old terminal", TransferTypeOldTerminalIDs},
		{"temporary token old terminal", TransferTypeTemporaryTokenOldTerminalIDs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			doer := fakeDoer(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Query().Get("operation"))
				if len(calls) == 1 {
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":6}}`), nil
				}
				return response(200, `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}}`), nil
			})
			c := newClient(Config{HTTPClient: doer, Entitlement: Entitlement{Server: "https://example.test", TransferType: tt.transfer}})
			r, err := c.activate(context.Background(), &transferOffer{SourceIdentity: Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}, AuthToken: AuthToken{Value: "audit"}, TemporaryToken: TemporaryToken{Value: "temp"}}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Continue(context.Background(), r, ContinueRequest{SourceProfileDeleted: true})
			if len(calls) != 2 || calls[1] != OperationAcquireConf.String() {
				t.Errorf("requests after deletion=%v; want [ManageSubscription AcquireConfiguration]; error=%v", calls, err)
			}
		})
	}
}

func TestJSONApplicationTransferCompletes(t *testing.T) {
	for _, post := range []bool{false, true} {
		t.Run(fmt.Sprintf("post_%v", post), func(t *testing.T) {
			offer := alignmentOffer()
			var operations []string
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post, TransferType: TransferTypeTemporaryToken}, Source: Endpoint{Channel: fakeChannel{}, Device: offer.SourceDevice}, Target: Endpoint{Device: offer.TargetDevice}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				fields := alignmentRequestFields(t, req)
				operation := fields.Get("operation")
				operations = append(operations, operation+":"+fields.Get("operation_type"))
				switch Operation(operation) {
				case "":
					return response(200, `{"Token":{"token":"auth","validity":"86400"}}`), nil
				case OperationCheck:
					return response(200, `{"Token":{"token":"rotated","validity":null},"ap2009":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
				case OperationTempToken:
					if fields.Get("token") != "rotated" {
						t.Error("rotated authentication token was not used")
					}
					return response(200, `{"ap2009":{"OperationResult":1,"TemporaryToken":"temporary","TemporaryTokenExpiry":"2030-01-01T00:00:00Z","OperationTargets":"ManageSubscription,AcquireConfiguration"}}`), nil
				case OperationManage:
					if fields.Get("operation_type") != "3" {
						t.Fatal("already-activated download triggered another subscription update")
					}
					return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":4}}`), nil
				case OperationAcquireConf:
					return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":1,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example","MatchingID":"matching","ProfileIccid":"8900000000000000000"}}}]}}`), nil
				default:
					t.Fatalf("unexpected operation %q", operation)
					return nil, nil
				}
			})})
			result, err := client.Transfer(t.Context())
			if err != nil || result == nil {
				t.Fatalf("transfer error=%v", err)
			}
			if result.State != StatePendingDownload || result.DownloadConfig.SMDPFQDN != "smdp.example" {
				t.Fatalf("state=%v download=%+v", result.State, result.DownloadConfig)
			}
			result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "8900000000000000000"})
			if err != nil || result.State != StateDone {
				t.Fatalf("state=%v err=%v", result.State, err)
			}
			want := []string{":", "CheckEligibility:", "AcquireTemporaryToken:", "ManageSubscription:3", "AcquireConfiguration:"}
			if !reflect.DeepEqual(operations, want) {
				t.Fatalf("operations=%v, want %v", operations, want)
			}
		})
	}
}

func TestPrimaryJSONDenialRemainsTerminal(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: &queueDoer{bodies: []string{`{"ap2009":{"OperationResult":100,"PrimaryAppEligibility":1}}`}}})
	offer := alignmentOffer()
	_, err := client.CheckEligibility(t.Context(), SubscriptionTransferRequest{Identity: offer.SourceIdentity, Source: offer.SourceDevice, Target: offer.TargetDevice}, AuthToken{Value: "auth"})
	var denied *CarrierDeniedError
	if !errors.As(err, &denied) || denied.Result.OperationResult != 100 {
		t.Fatalf("error=%v", err)
	}
}

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
			client := newClient(Config{Entitlement: Entitlement{Server: "https://example.test", TransferType: TransferTypeTS43V8}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
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

func TestActivateDownloadAndCompleteActivation(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	doer := &queueDoer{bodies: []string{
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + base64.StdEncoding.EncodeToString([]byte(activationCode)) + `"/><parm name="ProfileIccid" value="8900000000000000000"/></characteristic></characteristic></wap-provisioningdoc>`,
		`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`,
		`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`,
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

	result, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if event, ok := result.Event.(DownloadReadyEvent); !ok || event.Config.ActivationCode != activationCode {
		t.Fatalf("Event = %#v", result.Event)
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{ICCID: "8900000000000000000"})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if result.State != StateDone {
		t.Errorf("State = %d, want %d", result.State, StateDone)
	}
	if _, ok := result.Event.(DoneEvent); !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if doer.calls != 3 {
		t.Errorf("HTTP calls = %d, want download, activation, and configuration requests", doer.calls)
	}
}

func TestTransferDiscoversEntitlementServer(t *testing.T) {
	var calls []Operation
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "carrier.example" {
				t.Errorf("host = %q, want discovered carrier server", req.URL.Host)
			}
			query := req.URL.Query()
			if got := query.Get("entitlement_version"); got != "8.0" {
				t.Errorf("entitlement_version = %q, want discovered version 8.0", got)
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
					t.Errorf("temporary_token = %q, want tmp", got)
				}
				if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Errorf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				t.Fatal("AcquireConfiguration was called before profile download")
				return response(500, ""), nil
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement:  Entitlement{},
		Entitlements: transferDiscoveryConfigs(t),
		Source:       Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:       Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:       slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Transfer(context.Background())
	if err != nil {
		t.Fatalf("Transfer() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if client.transfer.entitlement.Server != "https://carrier.example/ts43" {
		t.Errorf("Server = %q, want discovered carrier server", client.transfer.entitlement.Server)
	}
	if got := requestOperationNames(calls); got != ",CheckEligibility,AcquireTemporaryToken,ManageSubscription" {
		t.Errorf("calls = %s", got)
	}
}

func transferDiscoveryConfigs(t *testing.T) *EntitlementConfigs {
	t.Helper()
	var configs EntitlementConfigs
	if err := json.Unmarshal([]byte(`[{
		"mccmnc": "310260",
		"carrierName": "Test Carrier",
		"entitlementServer": "https://carrier.example/ts43",
		"entitlementVersion": "8.0",
		"supportEsimToEsimTransfer": true,
		"transferTypeEsim": 2
	}]`), &configs); err != nil {
		t.Fatal(err)
	}
	return &configs
}

func TestTransferKeepsExplicitEntitlementServer(t *testing.T) {
	var calls int
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Host != "example.test" {
				t.Errorf("host = %q, want explicit server", req.URL.Host)
			}
			switch Operation(req.URL.Query().Get("operation")) {
			case "":
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic></wap-provisioningdoc>`), nil
			case OperationCheck:
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := req.URL.Query().Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Errorf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				t.Fatal("AcquireConfiguration was called before profile download")
				return response(500, ""), nil
			default:
				t.Fatalf("unexpected operation %q", req.URL.Query().Get("operation"))
				return response(500, ""), nil
			}
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Source: Endpoint{
			Channel: identityChannel{identity: Identity{ICCID: "8910000000000000000", IMSI: "208011234567890", MCC: "208", MNC: "01"}},
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
	if result.State != StatePendingDownload {
		t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if calls != 3 {
		t.Errorf("HTTP calls = %d, want 3", calls)
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

	result, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StatePendingDownload {
		t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
	}
	if event, ok := result.Event.(DownloadReadyEvent); !ok || event.Config.ActivationCode != activationCode {
		t.Fatalf("Event = %#v", result.Event)
	}
	if doer.calls != 1 {
		t.Errorf("HTTP calls = %d, want 1", doer.calls)
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
		{name: "old terminal ID type keeps protocol empty", transferType: TransferTypeOldTerminalIDs, wantSecond: OperationAcquireConf},
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
							t.Errorf("first request body = %s, want ManageSubscription", body)
						}
						if !strings.Contains(body, `"token":"old-token"`) {
							t.Errorf("first ManageSubscription body = %s, want old token", body)
						}
						if strings.Contains(body, `"old_terminal_entitlement_protocol"`) {
							t.Errorf("first ManageSubscription body = %s, want no old terminal protocol", body)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="new-token"/><parm name="validity" value="3600"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="6"/></characteristic></wap-provisioningdoc>`), nil
					case 2:
						if !strings.Contains(body, `"operation":"`+tt.wantSecond.String()+`"`) {
							t.Errorf("second request body = %s, want %s", body, tt.wantSecond)
						}
						if !strings.Contains(body, `"token":"new-token"`) {
							t.Errorf("second request body = %s, want returned token", body)
						}
						hasOldProto := strings.Contains(body, `"old_terminal_entitlement_protocol":"1"`)
						if hasOldProto != tt.wantOldProto {
							t.Errorf("second request body = %s, want old terminal protocol %v", body, tt.wantOldProto)
						}
						if tt.wantSecond == OperationAcquireConf {
							if strings.Contains(body, `"operation_type"`) {
								t.Errorf("AcquireConfiguration body = %s, want no operation_type", body)
							}
							if tt.wantOldProto && !strings.Contains(body, `"terminal_id":"111111111111111"`) {
								t.Errorf("AcquireConfiguration body = %s, want source terminal_id", body)
							}
							if !strings.Contains(body, `"target_terminal_id":"222222222222222"`) {
								t.Errorf("AcquireConfiguration body = %s, want target_terminal_id", body)
							}
							if !strings.Contains(body, `"target_terminal_eid":"89049032000001000000000000000000"`) {
								t.Errorf("AcquireConfiguration body = %s, want target_terminal_eid", body)
							}
							if strings.Contains(body, `"old_terminal_iccid"`) || strings.Contains(body, `"old_terminal_id"`) {
								t.Errorf("AcquireConfiguration body = %s, want no old terminal identifiers", body)
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

			result, err := client.activate(context.Background(), &transferOffer{
				SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "old-token"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if result.State != StatePendingSourceProfileDeletion {
				t.Errorf("State = %d, want %d", result.State, StatePendingSourceProfileDeletion)
			}

			result, err = client.Continue(context.Background(), result, ContinueRequest{SourceProfileDeleted: true})
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if result.State != StatePendingDownload {
				t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
			}
			if result.DownloadConfig.ActivationCode != activationCode {
				t.Errorf("ActivationCode = %q, want %q", result.DownloadConfig.ActivationCode, activationCode)
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
					t.Errorf("CheckEligibility token = %q, want auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("token"); got != "checked-auth" {
					t.Errorf("ManageSubscription token = %q, want checked-auth", got)
				}
				if query.Get("operation_type") == OperationTypeUpdateSubscription.String() {
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				}
				if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Errorf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
				}
				if got := query.Get("terminal_id"); got != "111111111111111" {
					t.Errorf("terminal_id = %q, want source IMEI", got)
				}
				if got := query.Get("terminal_eid"); got != "" {
					t.Errorf("terminal_eid = %q, want empty", got)
				}
				if got := query.Get("terminal_iccid"); got != "" {
					t.Errorf("terminal_iccid = %q, want empty", got)
				}
				if got := query.Get("target_terminal_id"); got != "222222222222222" {
					t.Errorf("target_terminal_id = %q, want target IMEI", got)
				}
				if got := query.Get("target_terminal_eid"); got != "89049032000001000000000000000000" {
					t.Errorf("target_terminal_eid = %q, want target EID", got)
				}
				if got := query.Get("target_terminal_model"); got != "" {
					t.Errorf("target_terminal_model = %q, want empty", got)
				}
				if got := query.Get("old_terminal_id"); got != "111111111111111" {
					t.Errorf("old_terminal_id = %q, want source IMEI", got)
				}
				if got := query.Get("old_terminal_iccid"); got != "8910000000000000000" {
					t.Errorf("old_terminal_iccid = %q, want source ICCID", got)
				}
				if got := query.Get("old_terminal_entitlement_protocol"); got != "" {
					t.Errorf("old_terminal_entitlement_protocol = %q, want empty", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="2"/><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="`+base64.StdEncoding.EncodeToString([]byte(activationCode))+`"/></characteristic></characteristic></wap-provisioningdoc>`), nil
			case OperationAcquireConf:
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
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
		t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
	}
	event, ok := result.Event.(DownloadReadyEvent)
	if !ok {
		t.Fatalf("Event = %#v, want DownloadReadyEvent", result.Event)
	}
	if event.Config.ActivationCode != activationCode {
		t.Errorf("ActivationCode = %q, want %q", event.Config.ActivationCode, activationCode)
	}
	wantCalls := []Operation{"", OperationCheck, OperationManage}
	if !slices.Equal(calls, wantCalls) {
		t.Errorf("calls = %v, want %v", calls, wantCalls)
	}

	result, err = client.CompleteActivation(context.Background(), result, ActivationResult{ICCID: "8900000000000000000"})
	if err != nil {
		t.Fatalf("CompleteActivation() error = %v", err)
	}
	if result.State != StateDone {
		t.Errorf("State after CompleteActivation = %d, want %d", result.State, StateDone)
	}
	if _, ok := result.Event.(DoneEvent); !ok {
		t.Errorf("Event after CompleteActivation = %#v, want DoneEvent", result.Event)
	}
	wantCalls = append(wantCalls, OperationManage, OperationAcquireConf)
	if !slices.Equal(calls, wantCalls) {
		t.Errorf("calls after CompleteActivation = %v, want %v", calls, wantCalls)
	}
}

func TestActivateInvalidMessageResponse(t *testing.T) {
	tests := []struct {
		name         string
		transferType TransferType
		useSMDS      bool
	}{
		{
			name: "ordinary auth rejects invalid input",
		},
		{
			name:         "type three rejects invalid input",
			transferType: TransferTypeReusableActivationCode,
		},
		{
			name:         "SM-DS does not accept invalid input",
			transferType: TransferTypeTS43V8,
			useSMDS:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="104"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:                 "https://example.test/ts43",
					TransferType:           tt.transferType,
					UseSMDSDelayedDownload: tt.useSMDS,
				},
			})

			_, err := client.activate(context.Background(), &transferOffer{
				SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "auth"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if _, ok := errors.AsType[*CarrierDeniedError](err); !ok {
				t.Fatalf("activate() error = %v, want CarrierDeniedError", err)
			}
		})
	}
}

func TestTransferRejectsUnsupportedCapabilitiesBeforeHTTP(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantErr     error
	}{
		{
			name:        "RAC requires LPA integration",
			entitlement: Entitlement{TransferType: TransferTypeReusableActivationCodeSMDP},
			wantErr:     ErrSMDPTransferRequiresLPA,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			tt.entitlement.Server = "https://example.test/ts43"
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
					called = true
					return response(500, ""), nil
				}),
				Entitlement: tt.entitlement,
				Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = client.Transfer(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Transfer() error = %v, want %v", err, tt.wantErr)
			}
			if called {
				t.Fatal("Transfer() sent an HTTP request")
			}
		})
	}
}

func TestActivateDoesNotRunTargetActivationBeforeDownload(t *testing.T) {
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

	result, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		AuthToken:      AuthToken{Value: "temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if result.State != StateDone {
		t.Errorf("State = %d, want %d", result.State, StateDone)
	}
	if doer.calls != 1 {
		t.Errorf("HTTP calls = %d, want only transfer ManageSubscription", doer.calls)
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
					t.Errorf("CheckEligibility token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				tempFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Errorf("AcquireTemporaryToken token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="new-temp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
			case OperationManage:
				if got := query.Get("operation_type"); got != OperationTypeTransferSubscription.String() {
					t.Errorf("operation_type = %q, want %s", got, OperationTypeTransferSubscription)
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
		Entitlement: Entitlement{Server: "https://example.test/ts43", AutoRetry511Limit: 1, PollTimeout: time.Second, TransferType: TransferTypeTemporaryToken, UseSMDSDelayedDownload: true},
		Source:      Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:      Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
		Logger:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
		TemporaryToken: TemporaryToken{Value: "old-temp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	if !authFetched || !eligibilityFetched || !tempFetched {
		t.Errorf("refresh auth=%v eligibility=%v temp=%v, want all true", authFetched, eligibilityFetched, tempFetched)
	}
	if got := strings.Join(manageTokens, ","); got != "old-temp,new-temp" {
		t.Errorf("ManageSubscription tokens = %q, want old-temp,new-temp", got)
	}
	if result.State != StatePendingSMDSDiscovery {
		t.Errorf("State = %d, want %d", result.State, StatePendingSMDSDiscovery)
	}
	if _, ok := result.Event.(SMDSDiscoveryEvent); !ok {
		t.Fatalf("Event = %#v, want SMDSDiscoveryEvent", result.Event)
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
					t.Errorf("CheckEligibility token = %q, want new-auth", got)
				}
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
			case OperationTempToken:
				tempFetched = true
				if got := query.Get("token"); got != "new-auth" {
					t.Errorf("AcquireTemporaryToken token = %q, want new-auth", got)
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

	resp, err := client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, temporaryCredential(TemporaryToken{Value: "old-temp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if !authFetched || !eligibilityFetched || !tempFetched {
		t.Errorf("refresh auth=%v eligibility=%v temp=%v, want all true", authFetched, eligibilityFetched, tempFetched)
	}
	if got := strings.Join(acquireTokens, ","); got != "old-temp,new-temp" {
		t.Errorf("AcquireConfiguration tokens = %q, want old-temp,new-temp", got)
	}
	if resp.Download.SMDPFQDN != "smdp.example" {
		t.Errorf("Download = %+v", resp.Download)
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
							t.Errorf("CheckEligibility token = %q, want new-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						t.Fatal("AcquireTemporaryToken was called")
						return response(500, ""), nil
					case OperationManage:
						manageTokens = append(manageTokens, query.Get("temporary_token"))
						if got := query.Get("token"); got != "" {
							t.Errorf("ManageSubscription token = %q, want empty", got)
						}
						if got := query.Get("temporary_token"); got != "old-temp" {
							t.Errorf("ManageSubscription temporary_token = %q, want old-temp", got)
						}
						if got := query.Get("terminal_id"); got != "222222222222222" {
							t.Errorf("ManageSubscription terminal_id = %q, want target IMEI", got)
						}
						if got := query.Get("terminal_eid"); got != "89049032000001000000000000000000" {
							t.Errorf("ManageSubscription terminal_eid = %q, want target EID", got)
						}
						if got := query.Get("target_terminal_id"); got != "" {
							t.Errorf("ManageSubscription target_terminal_id = %q, want empty", got)
						}
						if got := query.Get("old_terminal_id"); got != "111111111111111" {
							t.Errorf("ManageSubscription old_terminal_id = %q, want source IMEI", got)
						}
						if got := query.Get("old_terminal_iccid"); got != "8910000000000000000" {
							t.Errorf("ManageSubscription old_terminal_iccid = %q, want source ICCID", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`), nil
					case OperationAcquireConf:
						acquireTokens = append(acquireTokens, query.Get("temporary_token"))
						if got := query.Get("token"); got != "" {
							t.Errorf("AcquireConfiguration token = %q, want empty", got)
						}
						if got := query.Get("temporary_token"); got != "old-temp" {
							t.Errorf("AcquireConfiguration temporary_token = %q, want old-temp", got)
						}
						if got := query.Get("terminal_id"); got != "222222222222222" {
							t.Errorf("AcquireConfiguration terminal_id = %q, want target IMEI", got)
						}
						if got := query.Get("terminal_eid"); got != "89049032000001000000000000000000" {
							t.Errorf("AcquireConfiguration terminal_eid = %q, want target EID", got)
						}
						if got := query.Get("target_terminal_id"); got != "" {
							t.Errorf("AcquireConfiguration target_terminal_id = %q, want empty", got)
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

			result, err := client.activate(context.Background(), &transferOffer{
				SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "old-auth"},
				TemporaryToken: TemporaryToken{Value: "old-temp"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if authFetched || eligibilityFetched {
				t.Errorf("refresh auth=%v eligibility=%v, want no refresh", authFetched, eligibilityFetched)
			}
			if got := strings.Join(manageTokens, ","); got != "old-temp" {
				t.Errorf("ManageSubscription tokens = %q, want old-temp", got)
			}
			if len(acquireTokens) != 1 || acquireTokens[0] != "old-temp" {
				t.Errorf("AcquireConfiguration tokens = %q, want old-temp", strings.Join(acquireTokens, ","))
			}
			if result.State != StatePendingDownload {
				t.Errorf("State = %d, want %d", result.State, StatePendingDownload)
			}
			if _, ok := result.Event.(DownloadReadyEvent); !ok {
				t.Fatalf("Event = %#v, want DownloadReadyEvent", result.Event)
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
			result, err := client.activate(context.Background(), &transferOffer{
				SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				AuthToken:      AuthToken{Value: "tmp"},
			}, UserInputResult{Button: MessageButtonUnknown})
			if err != nil {
				t.Fatalf("activate() error = %v", err)
			}
			if result.State != tt.wantState {
				t.Errorf("State = %d, want %d", result.State, tt.wantState)
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
			result: &Result{State: StatePendingUserInput, session: &transferSession{offer: &transferOffer{}}},
		},
		{
			name:   "missing websheet result",
			result: &Result{State: StatePendingWebsheet, session: &transferSession{offer: &transferOffer{}}},
		},
		{
			name:   "missing source profile deletion confirmation",
			result: &Result{State: StatePendingSourceProfileDeletion, session: &transferSession{offer: &transferOffer{}}},
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

	_, err = client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 2*time.Minute {
		t.Errorf("sleeps = %v, want [2m]", sleeps)
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

	_, err = client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want [2s]", sleeps)
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

	_, err = client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err != nil {
		t.Fatalf("pollConfiguration() error = %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 3*time.Second {
		t.Errorf("sleeps = %v, want [3s]", sleeps)
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

	_, err = client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if !errors.Is(err, errPollingNotTriggered) {
		t.Fatalf("pollConfiguration() error = %v, want errPollingNotTriggered", err)
	}
}

func TestApplyDownloadConfigurationRejectsMissingDownloadInfo(t *testing.T) {
	client := &Client{}

	result, err := client.applyDownloadConfiguration(&Result{
		session: &transferSession{
			offer: &transferOffer{
				TargetDevice: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			},
		},
	}, DownloadConfig{}, ServiceStatusUnknown)
	if !errors.Is(err, errDownloadInfoMissing) {
		t.Fatalf("applyDownloadConfiguration() error = %v, want errDownloadInfoMissing", err)
	}
	if result == nil || result.State == StatePendingDownload {
		t.Errorf("result = %+v, should not enter download", result)
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

	result, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
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
		t.Errorf("State = %d, want %d", result.State, StatePendingSMDSDiscovery)
	}
	event, ok := result.Event.(SMDSDiscoveryEvent)
	if !ok {
		t.Fatalf("Event = %#v", result.Event)
	}
	if event.SourceICCID != "8910000000000000000" {
		t.Errorf("Event.SourceICCID = %q, want %q", event.SourceICCID, "8910000000000000000")
	}
	if event.TargetEID != "89049032000001000000000000000000" {
		t.Errorf("Event.TargetEID = %q, want %q", event.TargetEID, "89049032000001000000000000000000")
	}
	if event.TargetIMEI != "222222222222222" {
		t.Errorf("Event.TargetIMEI = %q, want %q", event.TargetIMEI, "222222222222222")
	}
	if event.SubscriptionResult != SubscriptionResultDelayedDownload {
		t.Errorf("Event.SubscriptionResult = %d, want %d", event.SubscriptionResult, SubscriptionResultDelayedDownload)
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
			resp, err := client.pollConfiguration(context.Background(), &transferOffer{
				SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
			}, authCredential(AuthToken{Value: "tmp"}))
			if err == nil {
				t.Fatal("pollConfiguration() error = nil, want deactivated error")
			}
			var denied *CarrierDeniedError
			if !errors.As(err, &denied) || denied.Operation != OperationAcquireConf || denied.Result != resp {
				t.Fatalf("error = %v, want classified carrier denial with response", err)
			}
			if resp == nil || (resp.ServiceStatus != ServiceStatusDeactivated && resp.ServiceStatus != ServiceStatusDeactivatedNoReuse) {
				t.Errorf("ServiceStatus = %v", resp)
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

	_, err = client.pollConfiguration(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
	}, authCredential(AuthToken{Value: "tmp"}))
	if err == nil {
		t.Fatal("pollConfiguration() error = nil, want unknown service status error")
	}
}
