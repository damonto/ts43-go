package ts43

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

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
		if got.Websheet != (Websheet{}) || got.Message.Text != "" || got.TemporaryToken != "" || got.AuthenticationToken != "" || got.DownloadInfoPresent || got.Download.SMDPFQDN != "" {
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
					if fields.Get("operation_type") == "4" {
						return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
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
			want := []string{":", "CheckEligibility:", "AcquireTemporaryToken:", "ManageSubscription:3", "AcquireConfiguration:", "ManageSubscription:4", "AcquireConfiguration:"}
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
