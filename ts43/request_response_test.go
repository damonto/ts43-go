package ts43

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/damonto/ts43-go/sim"
)

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
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				ServiceEntitlementRequest{
					App:                AppODSAPrimary,
					EntitlementVersion: "12.0",
					TerminalID:         "123456789012345",
					TerminalVendor:     "Google",
					TerminalModel:      "Pixel 8 Pro",
					TerminalSoftware:   "15/AP3A.240905.015",
					AppName:            "androidTransfer",
					AppVersion:         "1.2.3",
					Accept:             DefaultAccept,
				},
				ODSAOperation{Operation: OperationCheck, OperationType: OperationTypeOmit},
				DefaultODSAClientTS43,
				DefaultEAPAkaRealm,
			)
			if err != nil {
				t.Fatalf("buildHTTPRequest() error = %v", err)
			}
			if req.Method != tt.method {
				t.Fatalf("method = %s, want %s", req.Method, tt.method)
			}
			if req.Header.Get("Accept") != DefaultAccept {
				t.Fatalf("Accept = %q", req.Header.Get("Accept"))
			}
			if req.Header.Get("User-Agent") == "" {
				t.Fatal("User-Agent is empty")
			}
		})
	}
}

func TestBuildHTTPRequestOmitsUnsetMessageButton(t *testing.T) {
	req, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
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
		DefaultEAPAkaRealm,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got := req.URL.Query().Get("MSG_btn"); got != "" {
		t.Fatalf("MSG_btn = %q, want empty", got)
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
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
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
				DefaultEAPAkaRealm,
			)
			if err != nil {
				t.Fatalf("buildHTTPRequest() error = %v", err)
			}
			if got := req.URL.Query().Get("app"); got != tt.want {
				t.Fatalf("app = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildHTTPRequiresApp(t *testing.T) {
	_, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
		ServiceEntitlementRequest{
			EntitlementVersion: "8.0",
			TerminalID:         "123456789012345",
			TerminalVendor:     "Google",
			TerminalModel:      "Pixel 8 Pro",
			TerminalSoftware:   "15/AP3A.240905.015",
		},
		ODSAOperation{},
		DefaultODSAClientTS43,
		DefaultEAPAkaRealm,
	)
	if !errors.Is(err, errAppRequired) {
		t.Fatalf("buildHTTPRequest() error = %v, want %v", err, errAppRequired)
	}
}

func TestBuildHTTPRequestSendsExplicitRejectedMessageButton(t *testing.T) {
	req, err := buildHTTPRequest(
		context.Background(),
		Entitlement{Server: "https://example.test/ts43"},
		sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
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
		DefaultEAPAkaRealm,
	)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got := req.URL.Query().Get("MSG_btn"); got != MessageButtonRejected.String() {
		t.Fatalf("MSG_btn = %q, want %s", got, MessageButtonRejected)
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
				sim.Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
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
				DefaultEAPAkaRealm,
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
					t.Fatalf("%s = %q, want %q", key, fields[key], value)
				}
			}
			if tt.post {
				if fields["target_terminal_imeis"] != "333333333333333" {
					t.Fatalf("target_terminal_imeis = %q, want last JSON value", fields["target_terminal_imeis"])
				}
			} else {
				values := req.URL.Query()["target_terminal_imeis"]
				if len(values) != 2 || values[0] != "222222222222222" || values[1] != "333333333333333" {
					t.Fatalf("target_terminal_imeis values = %v, want repeated query parameters", values)
				}
			}
		})
	}
}

func TestODSAResponseDownloadConfig(t *testing.T) {
	activationCode := "LPA:1$smdp.example$MATCH"
	tests := []struct {
		name string
		body []byte
		want DownloadConfig
	}{
		{
			name: "profile activation code",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + base64.StdEncoding.EncodeToString([]byte(activationCode)) + `"/><parm name="ProfileIccid" value="8900000000000000000"/></characteristic></characteristic></wap-provisioningdoc>`),
			want: DownloadConfig{ActivationCode: activationCode, ProfileICCID: "8900000000000000000"},
		},
		{
			name: "smdp and iccid",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><characteristic type="DownloadInfo"><parm name="ProfileIccid" value="8900000000000000000"/><parm name="ProfileSmdpAddress" value="smdp1.example, smdp2.example"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`),
			want: DownloadConfig{SMDPFQDN: "smdp1.example", ProfileICCID: "8900000000000000000"},
		},
		{
			name: "explicit matching id",
			body: []byte(`{"DownloadInfo":{"MatchingID":"MATCH","ProfileIccid":"8900000000000000000","ProfileSmdpAddress":"smdp.example"}}`),
			want: DownloadConfig{SMDPFQDN: "smdp.example", MatchingID: "MATCH", ProfileICCID: "8900000000000000000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ODSAResponse
			if err := got.UnmarshalBinary(tt.body); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}
			if got.Download != tt.want {
				t.Fatalf("Download = %+v, want %+v", got.Download, tt.want)
			}
		})
	}
}

func TestODSAResponseMessageAndWebsheet(t *testing.T) {
	validation := base64.StdEncoding.EncodeToString([]byte(`^\d{4}$`))
	body := []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="8"/><characteristic type="MSG"><parm name="Message" value="Enter PIN"/><parm name="Accept_btn" value="1"/><parm name="Accept_freetext" value="1"/><parm name="Accept_freetext_validation" value="encodedValue=` + validation + `"/></characteristic></characteristic></wap-provisioningdoc>`)

	var got ODSAResponse
	if err := got.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary() error = %v", err)
	}
	if got.SubscriptionResult != SubscriptionResultRequiresUserInput {
		t.Fatalf("SubscriptionResult = %s, want %s", got.SubscriptionResult, SubscriptionResultRequiresUserInput)
	}
	if got.Message.Text != "Enter PIN" || !got.Message.AcceptButton || !got.Message.AcceptFreeText {
		t.Fatalf("Message = %+v", got.Message)
	}
	if got.Message.AcceptFreeTextValidation != `^\d{4}$` {
		t.Fatalf("AcceptFreeTextValidation = %q", got.Message.AcceptFreeTextValidation)
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
