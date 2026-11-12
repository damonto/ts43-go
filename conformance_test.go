package ts43

import (
	"context"
	"encoding"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	_ encoding.TextMarshaler   = encodedText("")
	_ encoding.TextUnmarshaler = (*encodedText)(nil)
	_ encoding.TextMarshaler   = operationList(nil)
	_ encoding.TextUnmarshaler = (*operationList)(nil)
)

func TestWireTextCodecs(t *testing.T) {
	for _, tt := range []struct {
		name, wire, want string
		invalid          bool
	}{
		{"plain base64 alphabet", "test", "test", false},
		{"plain whitespace", "  test  ", "  test  ", false},
		{"encoded", "encodedValue=dGVzdA==", "test", false},
		{"malformed prefix", "encodedValue=!!!", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var text encodedText
			err := text.UnmarshalText([]byte(tt.wire))
			if tt.invalid {
				if err == nil {
					t.Fatal("malformed Base64 accepted")
				}
				return
			}
			if err != nil || string(text) != tt.want {
				t.Fatalf("decoded = %q, %v", text, err)
			}
			wire, err := text.MarshalText()
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip encodedText
			if err := roundtrip.UnmarshalText(wire); err != nil || roundtrip != text {
				t.Errorf("round trip = %q, %v", roundtrip, err)
			}
		})
	}
	for _, wire := range []string{"", "ManageSubscription,AcquireConfiguration", "ManageSubscription,ExtensionOperation"} {
		t.Run("operations_"+wire, func(t *testing.T) {
			var ops operationList
			if err := ops.UnmarshalText([]byte(wire)); err != nil {
				t.Fatal(err)
			}
			data, err := ops.MarshalText()
			if err != nil || string(data) != wire {
				t.Errorf("round trip = %q, %v", data, err)
			}
		})
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

func TestWFCResetAndContinuation(t *testing.T) {
	for _, tt := range []struct {
		name             string
		ims, enabled     bool
		body             string
		wantError, reset bool
	}{
		{"reset disabled", true, false, `{"VERS":{"version":0,"validity":0}}`, false, true},
		{"reset enabled", true, true, `{"VERS":{"version":-1,"validity":-1}}`, false, true},
		{"stop until setting changes", true, true, `{"VERS":{"version":-2,"validity":-2}}`, false, true},
		{"setup mode missing WFC", false, true, `{"VERS":{"version":-1,"validity":-1}}`, true, false},
		{"empty body is not default", true, true, "", true, false},
		{"other application", false, false, `{"ap2003":{"EntitlementStatus":1}}`, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newClient(Config{HTTPClient: &queueDoer{bodies: []string{tt.body}}})
			got, err := c.CheckWFCEntitlement(context.Background(), WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, AuthToken: AuthToken{Value: "old"}, Config: WFCConfig{Server: "https://example.test", NeedsIMSProvisioning: tt.ims, DefaultServiceEntitlementEnabled: tt.enabled}})
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				return
			}
			if got.ConfigurationReset != tt.reset || got.Status.Entitled() != tt.enabled || got.AuthToken.Value != "" {
				t.Errorf("result = %+v", got)
			}
		})
	}
	for _, event := range []WebsheetCallbackEvent{WebsheetEventVoWiFiDismissFlow, WebsheetEventVoWiFiEntitlementChanged} {
		t.Run(fmt.Sprintf("callback_%v", event), func(t *testing.T) {
			doer := &queueDoer{bodies: []string{`{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":0,"TC_Status":0,"AddrStatus":0,"ProvStatus":0}}`, `{"APPLICATION":{"AppID":"ap2004","EntitlementStatus":1,"TC_Status":2,"AddrStatus":2,"ProvStatus":2}}`}}
			c := newClient(Config{HTTPClient: doer})
			previous, err := c.CheckWFCEntitlement(context.Background(), WFCEntitlementRequest{Identity: Identity{MCC: "208", MNC: "01", IMSI: "208011234567890"}, Config: WFCConfig{Server: "https://example.test"}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.ContinueWFC(context.Background(), previous, WebsheetResult{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			if event == WebsheetEventVoWiFiDismissFlow {
				if !got.Cancelled || got.Status != previous.Status || doer.calls != 1 {
					t.Errorf("cancel = %+v calls=%d", got, doer.calls)
				}
			} else if !got.Status.Entitled() || doer.calls != 2 {
				t.Errorf("complete = %+v calls=%d", got, doer.calls)
			}
		})
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
