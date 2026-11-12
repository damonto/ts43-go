package ts43

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRegressionGoogleAppName(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
	}{{"Verizon", Identity{MCC: "310", MNC: "012"}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ent, err := privateEntitlementConfigs(t).Discover(tt.identity, SIMTypeESIM)
			if err != nil {
				t.Fatal(err)
			}
			c := newClient(Config{Entitlement: ent})
			defer c.CloseIdleConnections()
			if got := c.baseRequest(Device{}, "audit", "").AppName; got != "" {
				t.Errorf("configured appName=%q became request app_name=%q", ent.AppName, got)
			}
		})
	}
}

func TestBuildHTTPRequest(t *testing.T) {
	tests := []struct {
		name   string
		post   bool
		method string
	}{
		{name: "get", method: http.MethodGet},
		{name: "post", post: true, method: http.MethodPost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildHTTPRequest(
				context.Background(),
				Entitlement{Server: "https://example.test/ts43", UsePost: tt.post},
				Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				ServiceEntitlementRequest{
					App:                AppODSAPrimary,
					EntitlementVersion: "12.0",
					TerminalID:         "123456789012345",
					TerminalVendor:     "Google",
					TerminalModel:      "Pixel 8 Pro",
					TerminalSoftware:   "15/AP3A.240905.015",
					AppName:            "androidTransfer",
					AppVersion:         "1.2.3",
					Accept:             DefaultResponseAccept,
				},
				ODSAOperation{Operation: OperationCheck, OperationType: OperationTypeOmit},
				DefaultODSAClientTS43,
				DefaultEAPAKARealm,
			)
			if err != nil {
				t.Fatalf("buildHTTPRequest() error = %v", err)
			}
			if req.Method != tt.method {
				t.Errorf("method = %s, want %s", req.Method, tt.method)
			}
			if req.Header.Get("Accept") != EAPRelayContentType+", "+DefaultResponseAccept {
				t.Errorf("Accept = %q", req.Header.Get("Accept"))
			}
			if req.Header.Get("User-Agent") == "" {
				t.Error("User-Agent is empty")
			}
		})
	}
}

func TestBuildHTTPRequestOmitsUnsetMessageButton(t *testing.T) {
	req, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		ServiceEntitlementRequest{
			AuthenticationToken: "auth",
			EntitlementVersion:  "2.0",
			App:                 AppODSAPrimary,
			TerminalID:          "123456789012345",
			TerminalVendor:      "Google",
			TerminalModel:       "Pixel 8 Pro",
			TerminalSoftware:    "15/AP3A.240905.015",
		},
		ODSAOperation{
			Operation:        OperationTempToken,
			OperationType:    OperationTypeOmit,
			OperationTargets: []Operation{OperationManage, OperationAcquireConf},
		},
		DefaultODSAClientTS43,
		DefaultEAPAKARealm,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got := req.URL.Query().Get("MSG_btn"); got != "" {
		t.Errorf("MSG_btn = %q, want empty", got)
	}
}

func TestBuildHTTPSendsConfiguredApp(t *testing.T) {
	tests := []struct {
		name string
		app  string
		want string
	}{
		{name: "explicit ODSA app", app: AppODSAPrimary, want: AppODSAPrimary},
		{name: "explicit VoWiFi app", app: AppVoWiFi, want: AppVoWiFi},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildHTTPRequest(
				context.Background(),
				Entitlement{Server: "https://example.test/ts43"},
				Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				ServiceEntitlementRequest{
					App:                tt.app,
					EntitlementVersion: "8.0",
					TerminalID:         "123456789012345",
					TerminalVendor:     "Google",
					TerminalModel:      "Pixel 8 Pro",
					TerminalSoftware:   "15/AP3A.240905.015",
				},
				ODSAOperation{},
				DefaultODSAClientTS43,
				DefaultEAPAKARealm,
			)
			if err != nil {
				t.Fatalf("buildHTTPRequest() error = %v", err)
			}
			if got := req.URL.Query().Get("app"); got != tt.want {
				t.Errorf("app = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildHTTPTruncatesDeviceFields(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name         string
			fields, want [3]string
		}{
			{
				name:   "ASCII above limit",
				fields: [3]string{"ExampleVendor", "ExampleDevice-ENTERPRISE", "16/UP1A.231005.007/long-build"},
				want:   [3]string{"Exam", "ExampleDev", "16/UP1A.231005.007/l"},
			},
			{
				name:   "Pixel model",
				fields: [3]string{"Google", "Pixel 10 Pro XL", "16/BP4A.251005.001/12345678"},
				want:   [3]string{"Goog", "Pixel 10 P", "16/BP4A.251005.001/1"},
			},
			{
				name:   "exact limit",
				fields: [3]string{"ABCD", "0123456789", "01234567890123456789"},
				want:   [3]string{"ABCD", "0123456789", "01234567890123456789"},
			},
			{
				name:   "below limit",
				fields: [3]string{"A", "Pixel", "16"},
				want:   [3]string{"A", "Pixel", "16"},
			},
			{name: "empty"},
			{
				name:   "Chinese UTF-16 units",
				fields: [3]string{"谷歌设备公司", "一二三四五六七八九十百", strings.Repeat("版本", 11)},
				want:   [3]string{"谷歌设备", "一二三四五六七八九十", strings.Repeat("版本", 10)},
			},
			{
				name:   "surrogate pairs count twice",
				fields: [3]string{"A😀BC", "12345678😀X", strings.Repeat("A", 18) + "😀tail"},
				want:   [3]string{"A😀B", "12345678😀", strings.Repeat("A", 18) + "😀"},
			},
			{
				name:   "split surrogate uses Java UTF-8 replacement",
				fields: [3]string{"ABC😀", "123456789😀", strings.Repeat("A", 19) + "😀"},
				want:   [3]string{"ABC?", "123456789?", strings.Repeat("A", 19) + "?"},
			},
		} {
			t.Run(fmt.Sprintf("post_%t/%s", post, tt.name), func(t *testing.T) {
				req, err := buildHTTPRequest(
					t.Context(),
					Entitlement{Server: "https://example.test/ts43", UsePost: post},
					Identity{IMSI: "001010123456789", MCC: "001", MNC: "01"},
					ServiceEntitlementRequest{
						AuthenticationToken: "auth",
						App:                 AppODSAPrimary,
						EntitlementVersion:  "9.0",
						TerminalID:          "123456789012345",
						TerminalVendor:      tt.fields[0],
						TerminalModel:       tt.fields[1],
						TerminalSoftware:    tt.fields[2],
						AppVersion:          "1.0",
					},
					ODSAOperation{},
					DefaultODSAClientTS43,
					DefaultEAPAKARealm,
				)
				if err != nil {
					t.Fatal(err)
				}
				fields := requestFields(t, req)
				for i, key := range []string{"terminal_vendor", "terminal_model", "terminal_sw_version"} {
					if got := fields[key]; got != tt.want[i] {
						t.Errorf("%s=%q, want %q", key, got, tt.want[i])
					}
				}
				wantAgent := fmt.Sprintf("PRD-TS43 term-%s/%s %s/1.0 OS-Android/%s", tt.want[0], tt.want[1], DefaultODSAClientTS43, tt.want[2])
				if got := req.Header.Get("User-Agent"); got != wantAgent {
					t.Errorf("User-Agent=%q, want %q", got, wantAgent)
				}
			})
		}
	}
}

func TestEAPRelayAccept(t *testing.T) {
	tests := []struct {
		name   string
		accept string
		want   string
	}{
		{
			name: "empty uses default response formats",
			want: EAPRelayContentType + ", " + DefaultResponseAccept,
		},
		{
			name:   "adds relay format",
			accept: DefaultResponseAccept,
			want:   EAPRelayContentType + ", " + DefaultResponseAccept,
		},
		{
			name:   "keeps existing relay format",
			accept: EAPRelayContentType + ", " + DefaultResponseAccept,
			want:   EAPRelayContentType + ", " + DefaultResponseAccept,
		},
		{
			name:   "matches relay format with parameter",
			accept: EAPRelayContentType + "; charset=utf-8, " + DefaultResponseAccept,
			want:   EAPRelayContentType + "; charset=utf-8, " + DefaultResponseAccept,
		},
		{
			name:   "matches relay format case-insensitively",
			accept: strings.ToUpper(EAPRelayContentType) + ", " + DefaultResponseAccept,
			want:   strings.ToUpper(EAPRelayContentType) + ", " + DefaultResponseAccept,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eapRelayAccept(tt.accept); got != tt.want {
				t.Errorf("eapRelayAccept() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildHTTPRequiresApp(t *testing.T) {
	_, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		ServiceEntitlementRequest{
			EntitlementVersion: "8.0",
			TerminalID:         "123456789012345",
			TerminalVendor:     "Google",
			TerminalModel:      "Pixel 8 Pro",
			TerminalSoftware:   "15/AP3A.240905.015",
		},
		ODSAOperation{},
		DefaultODSAClientTS43,
		DefaultEAPAKARealm,
	)
	if !errors.Is(err, errAppRequired) {
		t.Errorf("buildHTTPRequest() error = %v, want %v", err, errAppRequired)
	}
}

func TestBuildHTTPRequestSendsExplicitRejectedMessageButton(t *testing.T) {
	req, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		ServiceEntitlementRequest{
			TemporaryToken:     "tmp",
			EntitlementVersion: "2.0",
			App:                AppODSAPrimary,
			TerminalID:         "123456789012345",
			TerminalVendor:     "Google",
			TerminalModel:      "Pixel 8 Pro",
			TerminalSoftware:   "15/AP3A.240905.015",
		},
		ODSAOperation{
			Operation:        OperationManage,
			OperationType:    OperationTypeTransferSubscription,
			MessageButton:    MessageButtonRejected,
			MessageButtonSet: true,
		},
		DefaultODSAClientTS43,
		DefaultEAPAKARealm,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got := req.URL.Query().Get("MSG_btn"); got != MessageButtonRejected.String() {
		t.Errorf("MSG_btn = %q, want %s", got, MessageButtonRejected)
	}
}

func TestBuildHTTPRequestsSendsExtendedFields(t *testing.T) {
	tests := []struct {
		name string
		post bool
	}{
		{name: "get"},
		{name: "post", post: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildHTTPRequest(
				context.Background(),
				Entitlement{Server: "https://example.test/ts43", UsePost: tt.post},
				Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				ServiceEntitlementRequest{
					TemporaryToken:     "tmp",
					EntitlementVersion: "2.0",
					App:                AppODSAPrimary,
					TerminalID:         "123456789012345",
					TerminalVendor:     "Google",
					TerminalModel:      "Pixel 8 Pro",
					TerminalSoftware:   "15/AP3A.240905.015",
				},
				ODSAOperation{
					Operation:                     OperationManage,
					OperationType:                 OperationTypeTransferSubscription,
					TargetTerminalID:              "222222222222222",
					TargetTerminalICCID:           "8900000000000000000",
					TargetTerminalEID:             "89049032000001000000000000000000",
					TargetTerminalModel:           "Pixel",
					TargetTerminalSN:              "serial",
					TargetTerminalIMEIs:           []string{"222222222222222", "333333333333333"},
					CompanionTerminalID:           "444444444444444",
					CompanionTerminalVendor:       "Fit",
					CompanionTerminalModel:        "Watch",
					CompanionTerminalSoftware:     "1.0",
					CompanionTerminalFriendlyName: "Pixel Watch",
					CompanionTerminalService:      "SharedNumber",
					CompanionTerminalICCID:        "8910000000000000000",
					CompanionTerminalEID:          "89049032000001000000000000000001",
				},
				DefaultODSAClientTS43,
				DefaultEAPAKARealm,
			)
			if err != nil {
				t.Fatalf("buildHTTPRequest() error = %v", err)
			}
			fields := requestFields(t, req)
			want := map[string]string{
				"target_terminal_id":               "222222222222222",
				"target_terminal_iccid":            "8900000000000000000",
				"target_terminal_eid":              "89049032000001000000000000000000",
				"target_terminal_model":            "Pixel",
				"target_terminal_sn":               "serial",
				"companion_terminal_id":            "444444444444444",
				"companion_terminal_vendor":        "Fit",
				"companion_terminal_model":         "Watch",
				"companion_terminal_sw_version":    "1.0",
				"companion_terminal_friendly_name": "Pixel Watch",
				"companion_terminal_service":       "SharedNumber",
				"companion_terminal_iccid":         "8910000000000000000",
				"companion_terminal_eid":           "89049032000001000000000000000001",
			}
			for key, value := range want {
				if fields[key] != value {
					t.Errorf("%s = %q, want %q", key, fields[key], value)
				}
			}
			if tt.post {
				if fields["target_terminal_imeis"] != "333333333333333" {
					t.Errorf("target_terminal_imeis = %q, want last JSON value", fields["target_terminal_imeis"])
				}
			} else {
				values := req.URL.Query()["target_terminal_imeis"]
				if len(values) != 2 || values[0] != "222222222222222" || values[1] != "333333333333333" {
					t.Errorf("target_terminal_imeis values = %v, want repeated query parameters", values)
				}
			}
		})
	}
}

func requestFields(t *testing.T, req *http.Request) map[string]string {
	t.Helper()
	if req.Method == http.MethodGet {
		fields := map[string]string{}
		for key, values := range req.URL.Query() {
			if len(values) > 0 {
				fields[key] = values[0]
			}
		}
		return fields
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return fields
}

func TestTransferResponseNegotiation(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name                string
			token, accept, want string
		}{
			{name: "Google authentication", want: EAPRelayContentType + ", " + ODSAResponseAccept},
			{name: "Google operation", token: "auth", want: ODSAResponseAccept},
			{name: "Google explicit JSON", token: "auth", accept: "application/json", want: "application/json"},
		} {
			t.Run(fmt.Sprintf("post_%v/%s", post, tt.name), func(t *testing.T) {
				client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post}})
				offer := alignmentOffer()
				request := client.baseRequest(offer.SourceDevice, tt.token, "")
				if tt.accept != "" {
					request.Accept = tt.accept
				}
				built, err := buildHTTPRequest(t.Context(), client.transfer.entitlement, offer.SourceIdentity, request, ODSAOperation{}, client.clientTS43(), client.realm())
				if err != nil {
					t.Fatal(err)
				}
				if got := built.Header.Get("Accept"); got != tt.want {
					t.Fatalf("Accept=%q, want %q", got, tt.want)
				}
			})
		}
	}
}

func alignmentRequestFields(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	if req.Method == http.MethodGet {
		return req.URL.Query()
	}
	var body map[string]string
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	fields := make(url.Values)
	for key, value := range body {
		fields.Set(key, value)
	}
	return fields
}
