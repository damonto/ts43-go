package ts43

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

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
		{
			name: "absent download information",
			body: []byte(`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ODSAResponse
			if err := got.UnmarshalBinary(tt.body); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}
			if got.Download != tt.want {
				t.Errorf("Download = %+v, want %+v", got.Download, tt.want)
			}
		})
	}
}

func TestOptionalIntUnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    optionalInt
		wantErr bool
	}{
		{name: "empty", input: "  ", want: optionalInt{}},
		{name: "zero", input: "0", want: optionalInt{value: 0, set: true}},
		{name: "positive", input: " 42 ", want: optionalInt{value: 42, set: true}},
		{name: "invalid", input: "forty-two", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got optionalInt
			err := got.UnmarshalText([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalText(%q) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("UnmarshalText(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestODSAResponseRejectsInvalidIntegers(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantContext string
	}{
		{name: "register notification status", body: `{"RegisterNotifStatus":"invalid"}`, wantContext: "parse register notification status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response ODSAResponse
			err := response.UnmarshalBinary([]byte(tt.body))
			if err == nil {
				t.Fatalf("UnmarshalBinary(%s) error = nil, want error", tt.body)
			}
			if _, ok := errors.AsType[*strconv.NumError](err); !ok {
				t.Errorf("UnmarshalBinary(%s) error = %T, want *strconv.NumError in chain", tt.body, err)
			}
			if !strings.Contains(err.Error(), tt.wantContext) {
				t.Errorf("UnmarshalBinary(%s) error = %q, want context %q", tt.body, err, tt.wantContext)
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
		t.Errorf("SubscriptionResult = %s, want %s", got.SubscriptionResult, SubscriptionResultRequiresUserInput)
	}
	if got.Message.Text != "Enter PIN" || !got.Message.AcceptButton || !got.Message.AcceptFreeText {
		t.Errorf("Message = %+v", got.Message)
	}
	if got.Message.AcceptFreeTextValidation != `^\d{4}$` {
		t.Errorf("AcceptFreeTextValidation = %q", got.Message.AcceptFreeTextValidation)
	}
}

func TestODSAResponseSelectsPrimaryApplication(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       OperationResult
	}{
		{"2degrees JSON", `{"Vers":{"version":"1","validity":"172800"},"Token":{"token":"rotated","validity":null},"ap2009":{"OperationResult":1,"PrimaryAppEligibility":1}}`, OperationResultSuccess},
		{"JSON explicit application wins", `{"APPLICATION":{"OperationResult":1},"ap2009":{"OperationResult":100,"GeneralErrorText":"denied"}}`, 100},
		{"JSON explicit application ignores unused legacy shape", `{"APPLICATION":5,"ap2009":{"OperationResult":1}}`, OperationResultSuccess},
		{"JSON explicit application ignores unused root shape", `{"DownloadInfo":5,"ap2009":{"OperationResult":1}}`, OperationResultSuccess},
		{"JSON mixed applications", `{"ap2004":{"OperationResult":100},"ap2009":{"OperationResult":1}}`, OperationResultSuccess},
		{"JSON other application ignored", `{"APPLICATION":{"AppID":"ap2004","OperationResult":1}}`, OperationResultUnknown},
		{"JSON other flat application ignored", `{"AppID":"ap2004","OperationResult":1}`, OperationResultUnknown},
		{"JSON matching generic application", `{"APPLICATION":{"AppID":"ap2009","OperationResult":1}}`, OperationResultSuccess},
		{"JSON mixed case names", `{"tOkEn":{"token":"rotated","validity":null},"AP2009":{"OperationResult":"1"}}`, OperationResultSuccess},
		{"XML last application is unrelated", `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="OperationResult" value="100"/></characteristic></wap-provisioningdoc>`, OperationResultSuccess},
		{"XML matching application after unrelated", `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="OperationResult" value="100"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/></characteristic></wap-provisioningdoc>`, OperationResultSuccess},
		{"XML other application ignored", `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="OperationResult" value="1"/></characteristic></wap-provisioningdoc>`, OperationResultUnknown},
		{"XML sole legacy application", `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/></characteristic></wap-provisioningdoc>`, OperationResultSuccess},
		{"XML ambiguous legacy application ignored", `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/></characteristic><characteristic type="APPLICATION"><parm name="AppID" value="ap2004"/><parm name="OperationResult" value="100"/></characteristic></wap-provisioningdoc>`, OperationResultUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got ODSAResponse
			if err := got.UnmarshalBinary([]byte(tt.body)); err != nil {
				t.Fatal(err)
			}
			if got.OperationResult != tt.want {
				t.Fatalf("operation result=%v, want %v", got.OperationResult, tt.want)
			}
			if tt.name == "2degrees JSON" && (got.PrimaryAppEligibility != AppEligibilityEnabled || got.AuthenticationToken != "rotated" || got.AuthTokenValidity != 3600) {
				t.Fatalf("eligibility=%v token=%q validity=%d", got.PrimaryAppEligibility, got.AuthenticationToken, got.AuthTokenValidity)
			}
		})
	}
}

func TestODSAResponseRejectsMalformedPrimaryApplication(t *testing.T) {
	for _, body := range []string{`{"ap2009":1}`, `{"ap2009":[]}`, `{"ap2009":{"AppID":"ap2004","OperationResult":1}}`, `{"ap2009":{"OperationResult":"bad"}}`, `{"ap2009":{"PrimaryConfiguration":{"ServiceStatus":"bad"}}}`} {
		var got ODSAResponse
		if err := got.UnmarshalBinary([]byte(body)); err == nil {
			t.Errorf("accepted malformed response %s", body)
		}
	}
}

func TestODSAResponseReuseIsAtomicAndClearsAbsentFields(t *testing.T) {
	const portal = `{"Token":{"token":"old","validity":60},"APPLICATION":{"OperationResult":1,"SubscriptionResult":1,"SubscriptionServiceURL":"https://portal.example","SubscriptionServiceUserData":"old-data","TemporaryToken":"old-temp","MSG":{"Message":"old message"},"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`
	for _, body := range []string{`{"ap2009":{"OperationResult":1,"PrimaryAppEligibility":1}}`, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`} {
		var got ODSAResponse
		if err := got.UnmarshalBinary([]byte(portal)); err != nil {
			t.Fatal(err)
		}
		if err := got.UnmarshalBinary([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if got.Websheet != (Websheet{}) || got.Message.Text != "" || got.TemporaryToken != "" || got.AuthenticationToken != "" || got.Download.SMDPFQDN != "" {
			t.Fatalf("stale fields remained: %+v", got)
		}
		before := got
		before.Raw = append([]byte(nil), got.Raw...)
		if err := got.UnmarshalBinary([]byte(`{"APPLICATION":{"RegisterNotifStatus":"bad"}}`)); err == nil {
			t.Fatal("malformed response accepted")
		}
		if !reflect.DeepEqual(got, before) {
			t.Fatal("failed decoding changed the previous response")
		}
	}
}

func TestODSAResponseToleratesBareXMLAmpersands(t *testing.T) {
	var got ODSAResponse
	body := `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="SubscriptionResult" value="1"/><parm name="SubscriptionServiceURL" value="https://portal.example/?a=1&b=2&amp;c=3"/></characteristic></wap-provisioningdoc>`
	if err := got.UnmarshalBinary([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if got.Websheet.URL != "https://portal.example/?a=1&b=2&c=3" {
		t.Fatalf("URL=%q", got.Websheet.URL)
	}
}

func TestODSAResponsePrimaryConfigurations(t *testing.T) {
	for _, tt := range []struct {
		name, body   string
		wantStatus   ServiceStatus
		wantICCID    string
		wantDownload bool
	}{
		{
			name:       "nested XML terminal response",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ICCID" value="8900000000000000000"/><parm name="ServiceStatus" value="4"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "JSON collection",
			body:       `{"APPLICATION":{"OperationResult":1,"PrimaryConfigurations":[{"PrimaryConfiguration":{"ICCID":"8900000000000000000","ServiceStatus":4}}]}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "JSON object container",
			body:       `{"APPLICATION":{"OperationResult":1,"PrimaryConfigurations":{"PrimaryConfiguration":{"ICCID":"8900000000000000000","ServiceStatus":4}}}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "XML first configuration wins",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="4"/></characteristic><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusDeactivatedNoReuse,
		},
		{
			name:       "JSON first configuration wins",
			body:       `{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":4}},{"PrimaryConfiguration":{"ServiceStatus":1}}]}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse,
		},
		{
			name:       "standalone XML overrides collection",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="4"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusActivated,
		},
		{
			name:       "standalone JSON overrides collection",
			body:       `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1},"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":4}}]}}`,
			wantStatus: ServiceStatusActivated,
		},
		{
			name:       "nested download info",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ICCID" value="8900000000000000000"/><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusActivated, wantICCID: "8900000000000000000", wantDownload: true,
		},
		{
			name:       "download ICCID takes precedence",
			body:       `{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":{"ICCID":"old","ServiceStatus":1,"DownloadInfo":{"ProfileIccid":"new","ProfileSmdpAddress":"smdp.example"}}}]}}`,
			wantStatus: ServiceStatusActivated, wantICCID: "new", wantDownload: true,
		},
		{name: "empty collection", body: `{"APPLICATION":{"PrimaryConfigurations":[]}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got ODSAResponse
			if err := got.UnmarshalBinary([]byte(tt.body)); err != nil {
				t.Fatal(err)
			}
			if got.ServiceStatus != tt.wantStatus || got.Download.ProfileICCID != tt.wantICCID {
				t.Fatalf("status=%v ICCID=%q, want %v %q", got.ServiceStatus, got.Download.ProfileICCID, tt.wantStatus, tt.wantICCID)
			}
			if tt.wantDownload && got.Download.SMDPFQDN != "smdp.example" {
				t.Errorf("SM-DP+ = %q", got.Download.SMDPFQDN)
			}
		})
	}
}

func TestODSAResponsePrimaryConfigurationsRejectsMalformedInput(t *testing.T) {
	for _, body := range []string{
		`{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":1}]}}`,
		`{"APPLICATION":{"PrimaryConfigurations":{"PrimaryConfiguration":{"ServiceStatus":"invalid"}}}}`,
		`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"></wap-provisioningdoc>`,
	} {
		var got ODSAResponse
		if err := got.UnmarshalBinary([]byte(body)); err == nil {
			t.Fatalf("accepted malformed response %s", body)
		}
	}
}
