package ts43

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTokenOperationPreservesCookieDeletion(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		resp := responseWithCookie(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`, "remove=; Max-Age=0")
		resp.Header.Add("Set-Cookie", "relay=two")
		return resp, nil
	})})
	identity := Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}
	token, err := client.checkEligibility(t.Context(), nil, identity, Device{}, Device{}, AuthToken{Value: "old", Cookies: []string{"sticky=keep", "remove=gone", "relay=one"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(token.Cookies) != 2 {
		t.Fatalf("cookies=%v", token.Cookies)
	}
	values := map[string]bool{}
	for _, cookie := range token.Cookies {
		values[cookie] = true
	}
	if !values["sticky=keep"] || !values["relay=two"] {
		t.Fatalf("cookies=%v", token.Cookies)
	}
}

func TestEAPRelayRetainsCookiesAndBoundsReplies(t *testing.T) {
	for _, tt := range []struct {
		name      string
		replies   int
		exhausted bool
	}{
		{name: "third reply succeeds", replies: 3},
		{name: "fourth reply succeeds", replies: 4},
		{name: "fifth challenge exhausts retries", replies: 4, exhausted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			posts := 0
			client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example/ts43"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				posts++
				wantRelay := "one"
				if posts > 1 {
					wantRelay = "two"
				}
				for name, value := range map[string]string{"sticky": "keep", "relay": wantRelay} {
					cookie, err := req.Cookie(name)
					if err != nil || cookie.Value != value {
						t.Errorf("post %d cookie %s=%v err=%v", posts, name, cookie, err)
					}
				}
				if posts > 1 {
					if _, err := req.Cookie("remove"); !errors.Is(err, http.ErrNoCookie) {
						t.Errorf("deleted cookie retained: %v", err)
					}
				}
				if posts == 1 {
					r := responseWithCookie(200, eapRelayBody(t, 2), "relay=two")
					r.Header.Add("Set-Cookie", "remove=; Max-Age=0")
					return r, nil
				}
				if posts < tt.replies || tt.exhausted {
					return response(200, eapRelayBody(t, byte(posts+1))), nil
				}
				return response(200, `{"TOKEN":{"token":"new","Validity":3600}}`), nil
			})})
			body, cookies, err := (entitlementTransport{client: client, channel: akaChannel{}}).eapRelay(t.Context(), Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}, []byte(eapRelayBody(t, 1)), ServiceEntitlementRequest{}, []*http.Cookie{{Name: "sticky", Value: "keep"}, {Name: "relay", Value: "one"}, {Name: "remove", Value: "gone"}})
			if tt.exhausted {
				if err == nil || posts != tt.replies {
					t.Fatalf("posts=%d err=%v", posts, err)
				}
				return
			}
			if err != nil || posts != tt.replies || relayPacket(body) != "" {
				t.Fatalf("posts=%d err=%v", posts, err)
			}
			values := map[string]string{}
			for _, cookie := range cookies {
				values[cookie.Name] = cookie.Value
			}
			if len(values) != 2 || values["sticky"] != "keep" || values["relay"] != "two" {
				t.Fatalf("cookies=%v", values)
			}
		})
	}
}

func TestZeroValidityAuthTokenExpiresImmediately(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	client := newClient(Config{Now: func() time.Time { return now }, Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: &queueDoer{bodies: []string{`{"TOKEN":{"token":"new","Validity":0}}`}}})
	auth, err := client.authenticate(t.Context(), nil, Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}, Device{})
	if err != nil {
		t.Fatal(err)
	}
	rotated := authTokenFromResponse(AuthToken{Value: "old"}, &ODSAResponse{AuthenticationToken: "new", AuthTokenValidity: 0}, nil, now)
	for _, token := range []AuthToken{auth, rotated} {
		if !token.ExpiresAt.Equal(now) || authCredential(token).validAt(now) {
			t.Errorf("zero-validity token=%+v", token)
		}
	}
}

func TestCredentialExpiryAndRejection(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, temporary := range []bool{false, true} {
		for _, expiry := range []time.Time{{}, now.Add(time.Second), now, now.Add(-time.Second)} {
			t.Run(fmt.Sprintf("temporary_%v/expiry_%s", temporary, expiry), func(t *testing.T) {
				cred := authCredential(AuthToken{Value: "token", ExpiresAt: expiry})
				if temporary {
					cred = temporaryCredential(TemporaryToken{Value: "token", ExpiresAt: expiry})
				}
				if got, want := cred.validAt(now), expiry.IsZero() || expiry.After(now); got != want {
					t.Errorf("valid=%v want %v", got, want)
				}
				for _, status := range []int{401, 403, 511} {
					if got, want := cred.rejectedBy(&HTTPError{StatusCode: status}), status == 511 || temporary && status == 401; got != want {
						t.Errorf("status %d retry=%v want %v", status, got, want)
					}
				}
			})
		}
	}
	for _, text := range []string{"bad expiry", "2030-01-01T00:00:00Z"} {
		t.Run(text, func(t *testing.T) {
			_, err := temporaryTokenExpiresAt(text)
			if strings.HasPrefix(text, "bad") {
				if _, ok := errors.AsType[*time.ParseError](err); !ok {
					t.Fatalf("expiry error lost cause: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTemporaryCredentialRefreshFlow(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		expired bool
		status  int
		wantErr bool
	}{
		{"expired before request", true, 200, false}, {"HTTP 401", false, 401, false}, {"HTTP 403 is not renewed", false, 403, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			c := newClient(Config{Now: func() time.Time { return now }, Source: Endpoint{Channel: fakeChannel{}}, Entitlement: Entitlement{Server: "https://example.test", TransferType: TransferTypeTemporaryToken}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
				q := req.URL.Query()
				op := Operation(q.Get("operation"))
				calls = append(calls, op.String()+":"+q.Get("temporary_token"))
				switch op {
				case "":
					return response(200, `{"TOKEN":{"token":"fresh-auth","validity":3600}}`), nil
				case OperationCheck:
					return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
				case OperationTempToken:
					return response(200, `{"APPLICATION":{"OperationResult":1,"TemporaryToken":"fresh-temp","TemporaryTokenExpiry":"2030-01-01T00:00:00Z","OperationTargets":"ManageSubscription,AcquireConfiguration"}}`), nil
				case OperationManage:
					if q.Get("temporary_token") == "old-temp" {
						return response(tt.status, ""), nil
					}
					return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
				default:
					t.Fatalf("unexpected operation %q", op)
					return nil, errors.New("unexpected operation")
				}
			})})
			expiry := now.Add(time.Hour)
			if tt.expired {
				expiry = now
			}
			result, err := c.activate(context.Background(), &transferOffer{SourceIdentity: Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}, TemporaryToken: TemporaryToken{Value: "old-temp", ExpiresAt: expiry}}, UserInputResult{Button: MessageButtonUnknown})
			if tt.wantErr {
				if !isHTTPStatus(err, 403) || len(calls) != 1 {
					t.Fatalf("error=%v calls=%v", err, calls)
				}
				return
			}
			if err != nil || result.State != StateDone {
				t.Fatalf("result=%+v, error=%v", result, err)
			}
			want := []string{":", "CheckEligibility:", "AcquireTemporaryToken:", "ManageSubscription:fresh-temp"}
			if !tt.expired {
				want = append([]string{"ManageSubscription:old-temp"}, want...)
			}
			if !slices.Equal(calls, want) {
				t.Errorf("calls=%v, want %v", calls, want)
			}
		})
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
						Entitlement: Entitlement{Server: "https://carrier.example/ts43", UsePost: post, AutoRetry511Limit: tt.limit},
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

func TestGoogleTransferRelayNegotiatesXML(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Accept"), EAPRelayContentType+", "+ODSAResponseAccept; got != want {
			t.Fatalf("Accept=%q, want %q", got, want)
		}
		return response(200, `<wap-provisioningdoc/>`), nil
	})})
	if _, _, err := client.postRelay(t.Context(), "synthetic-eap-packet", ServiceEntitlementRequest{App: AppODSAPrimary}, nil); err != nil {
		t.Fatal(err)
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
	if client.runtime.logger == nil {
		t.Fatal("Logger = nil, want default logger")
	}
	_, _, err = client.call(
		context.Background(),
		Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		client.baseRequest(Device{IMEI: "111111111111111"}, "", ""),
		ODSAOperation{},
	)
	if err != nil {
		t.Fatalf("call() error = %v", err)
	}
}

func TestNewAllowsMissingEntitlementServer(t *testing.T) {
	client, err := New(&Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP request before transfer")
			return nil, nil
		}),
		Entitlement:  Entitlement{},
		Entitlements: transferDiscoveryConfigs(t),
		Source:       Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
		Target:       Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.transfer.entitlement.Server != "" {
		t.Errorf("Server = %q, want empty before discovery", client.transfer.entitlement.Server)
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
					t.Errorf("relay call 1 cookie = %v, %v; want relay=one", cookie, err)
				}
				calls++
				return responseWithCookie(http.StatusOK, eapRelayBody(t, 2), "relay=two"), nil
			case 2:
				cookie, err := req.Cookie("relay")
				if err != nil || cookie.Value != "two" {
					t.Errorf("relay call 2 cookie = %v, %v; want relay=two", cookie, err)
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
		Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		client.baseRequest(Device{IMEI: "111111111111111"}, "", ""),
		ODSAOperation{},
	)
	if err != nil {
		t.Fatalf("call() error = %v", err)
	}
	if calls != 3 {
		t.Errorf("HTTP calls = %d, want 3", calls)
	}
}

func TestAcquireTemporaryTokenRequiresTransferTargets(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="OperationTargets" value="ManageSubscription"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	})

	_, err := client.AcquireTemporaryToken(
		context.Background(),
		SubscriptionTransferRequest{
			Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Source:   Device{IMEI: "111111111111111"},
			Target:   Device{IMEI: "222222222222222"},
		},
		AuthToken{Value: "auth"},
	)
	if err == nil {
		t.Fatal("AcquireTemporaryToken() error = nil, want operation target error")
	}
}

func TestAcquireTemporaryTokenRequiresExpiry(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	})

	_, err := client.AcquireTemporaryToken(
		context.Background(),
		SubscriptionTransferRequest{
			Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
			Source:   Device{IMEI: "111111111111111"},
			Target:   Device{IMEI: "222222222222222"},
		},
		AuthToken{Value: "auth"},
	)
	if err == nil {
		t.Fatal("AcquireTemporaryToken() error = nil, want expiry error")
	}
}

func TestAuthenticateInitialEAPRequestUsesRelayAccept(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			wantAccept := EAPRelayContentType + ", " + ODSAResponseAccept
			if got := req.Header.Get("Accept"); got != wantAccept {
				t.Errorf("Accept = %q, want %q", got, wantAccept)
			}
			if got := req.URL.Query().Get("EAP_ID"); got == "" {
				t.Fatal("EAP_ID is empty")
			}
			return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/><parm name="Validity" value="3600"/></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{Server: "https://example.test/ts43"},
		Logger:      slog.Default(),
	})

	token, err := client.Authenticate(
		context.Background(),
		Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		Device{IMEI: "111111111111111"},
	)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if token.Value != "auth" {
		t.Errorf("token = %q, want auth", token.Value)
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
						t.Errorf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Errorf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Errorf("ManageSubscription temporary_token = %q, want empty", got)
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
						t.Errorf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Errorf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Errorf("ManageSubscription temporary_token = %q, want empty", got)
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
						t.Errorf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("token"); got != "checked-auth" {
						t.Errorf("ManageSubscription token = %q, want checked-auth", got)
					}
					if got := query.Get("temporary_token"); got != "" {
						t.Errorf("ManageSubscription temporary_token = %q, want empty", got)
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
						t.Errorf("CheckEligibility token = %q, want auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				case OperationTempToken:
					if got := query.Get("token"); got != "checked-auth" {
						t.Errorf("AcquireTemporaryToken token = %q, want checked-auth", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="temp-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
				case OperationManage:
					if got := query.Get("temporary_token"); got != "tmp" {
						t.Errorf("ManageSubscription temporary_token = %q, want tmp", got)
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
							t.Errorf("CheckEligibility retry token = %q, want fresh-auth", got)
						}
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
					case OperationTempToken:
						if got := query.Get("token"); got != "fresh-auth" {
							t.Errorf("AcquireTemporaryToken token = %q, want fresh-auth", got)
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
							t.Errorf("AcquireTemporaryToken retry token = %q, want fresh-auth", got)
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
					case OperationAcquireConf:
						calls = append(calls, "acquire")
						return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`), nil
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
			if result.session.offer.AuthToken.Value != tt.wantAuth {
				t.Errorf("session AuthToken = %q, want %q", result.session.offer.AuthToken.Value, tt.wantAuth)
			}
			if got := strings.Join(calls, ","); got != tt.wantCalls {
				t.Errorf("calls = %q, want %q", got, tt.wantCalls)
			}
		})
	}
}

func TestPostRelayRequestShape(t *testing.T) {
	tests := []struct {
		name                    string
		packet                  string
		accept                  string
		cookies                 []*http.Cookie
		wantBody                string
		wantAccept              string
		wantCookie              string
		wantUserAgentContaining string
	}{
		{
			name:                    "Google relay packet JSON",
			packet:                  "base64-eap-response",
			accept:                  DefaultResponseAccept,
			cookies:                 []*http.Cookie{{Name: "SESSION", Value: "abc"}},
			wantBody:                `{"eap-relay-packet":"base64-eap-response"}`,
			wantAccept:              EAPRelayContentType + ", " + DefaultResponseAccept,
			wantCookie:              "SESSION=abc",
			wantUserAgentContaining: DefaultODSAClientTS43,
		},
		{
			name:                    "Google accept already includes relay packet JSON",
			packet:                  "base64-eap-response",
			accept:                  EAPRelayContentType + ", " + DefaultResponseAccept,
			cookies:                 []*http.Cookie{{Name: "SESSION", Value: "abc"}},
			wantBody:                `{"eap-relay-packet":"base64-eap-response"}`,
			wantAccept:              EAPRelayContentType + ", " + DefaultResponseAccept,
			wantCookie:              "SESSION=abc",
			wantUserAgentContaining: DefaultODSAClientTS43,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodPost {
						t.Errorf("method = %q, want POST", req.Method)
					}
					if got := req.URL.String(); got != "https://example.test/ts43" {
						t.Errorf("URL = %q, want https://example.test/ts43", got)
					}
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatalf("read body: %v", err)
					}
					if got := string(body); got != tt.wantBody {
						t.Errorf("body = %q, want %q", got, tt.wantBody)
					}
					if got := req.Header.Get("Content-Type"); got != EAPRelayContentType {
						t.Errorf("Content-Type = %q, want %q", got, EAPRelayContentType)
					}
					if got := req.Header.Get("Accept"); got != tt.wantAccept {
						t.Errorf("Accept = %q, want %q", got, tt.wantAccept)
					}
					if got := req.Header.Get("Cookie"); got != tt.wantCookie {
						t.Errorf("Cookie = %q, want %q", got, tt.wantCookie)
					}
					if got := req.Header.Get("Accept-Language"); got != "" {
						t.Errorf("Accept-Language = %q, want empty", got)
					}
					if got := req.Header.Get("User-Agent"); !strings.Contains(got, tt.wantUserAgentContaining) {
						t.Errorf("User-Agent = %q, want marker %q", got, tt.wantUserAgentContaining)
					}
					return response(200, `<wap-provisioningdoc/>`), nil
				}),
				Entitlement: Entitlement{Server: "https://example.test/ts43"},
				Logger:      slog.Default(),
			})

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
				t.Errorf("selectedTransferType() = %v, want %v", got, tt.want)
			}
		})
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
			got, err := temporaryTokenExpiresAt(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("temporaryTokenExpiresAt() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("temporaryTokenExpiresAt() = %s, want %s", got, tt.want)
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
	challenge := AKAChallenge{Identifier: identifier, Rand: rand, Autn: autn}
	packet, err := challenge.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText error = %v", err)
	}
	return `{"eap-relay-packet":"` + string(packet) + `"}`
}

type fakeChannel struct{}

func (fakeChannel) Identity(context.Context) (*Identity, error) {
	return &Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}, nil
}

func (fakeChannel) AuthenticateAKA(context.Context, *AKARequest) (*AKAResponse, error) {
	return nil, nil
}

type identityChannel struct {
	identity Identity
}

func (c identityChannel) Identity(context.Context) (*Identity, error) {
	return &c.identity, nil
}

func (identityChannel) AuthenticateAKA(context.Context, *AKARequest) (*AKAResponse, error) {
	return nil, nil
}

type akaChannel struct{}

func (akaChannel) Identity(context.Context) (*Identity, error) {
	return &Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}, nil
}

func (akaChannel) AuthenticateAKA(context.Context, *AKARequest) (*AKAResponse, error) {
	return &AKAResponse{
		Res: []byte{0x01, 0x02, 0x03, 0x04},
		CK:  []byte{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f},
		IK:  []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f},
	}, nil
}

func requestOperationNames(ops []Operation) string {
	values := make([]string, 0, len(ops))
	for _, op := range ops {
		values = append(values, op.String())
	}
	return strings.Join(values, ",")
}
