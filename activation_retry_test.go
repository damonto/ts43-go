package ts43

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

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

func TestActivationDismissalClosesPortal(t *testing.T) {
	calls := 0
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", Channel: EntitlementChannelSamsung}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":5}}`), nil
	})})
	result := &Result{State: StatePendingDownload, session: &transferSession{offer: alignmentOffer(), websheetOpen: true}}
	result, err := client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
	if err != nil || result.State != StateDismissed || result.session.websheetOpen {
		t.Fatalf("state=%v err=%v", result.State, err)
	}
	result, err = client.Continue(t.Context(), result, ContinueRequest{Websheet: &WebsheetResult{Event: WebsheetEventFinishFlow}})
	if err == nil || result.State != StateDismissed || calls != 1 {
		t.Fatalf("state=%v calls=%d", result.State, calls)
	}
}

func TestTokenOperationPreservesCookieDeletion(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		resp := responseWithCookie(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`, "remove=; Max-Age=0")
		resp.Header.Add("Set-Cookie", "relay=two")
		return resp, nil
	})})
	identity := Identity{MCC: "310", MNC: "260", IMSI: "310260123456789"}
	token, err := client.checkEligibility(t.Context(), nil, identity, Device{}, AuthToken{Value: "old", Cookies: []string{"sticky=keep", "remove=gone", "relay=one"}}, Device{})
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
