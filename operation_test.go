package ts43

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPostInstallationRequestFields(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name         string
			transferType TransferType
			extraTarget  bool
		}{
			{name: "Google temporary token", transferType: TransferTypeTemporaryToken},
			{name: "Google extra target fields", transferType: TransferTypeTemporaryToken, extraTarget: true},
			{name: "Google source credential", transferType: TransferTypeTS43V8},
		} {
			t.Run(fmt.Sprintf("post_%v/%s", post, tt.name), func(t *testing.T) {
				offer := alignmentOffer()
				offer.TargetDevice.Model = "Target"
				offer.TargetDevice.SerialNumber = "target-serial"
				offer.TargetDevice.IMEIs = []string{offer.TargetDevice.IMEI}
				offer.TemporaryToken = TemporaryToken{Value: "temporary", Targets: []Operation{OperationManage, OperationAcquireConf}}
				transfers, updates, queries := 0, 0, 0
				client := newClient(Config{
					Entitlement: Entitlement{
						Server:                  "https://carrier.example",
						TransferType:            tt.transferType,
						UsePost:                 post,
						AppName:                 "transfer-app",
						SetTargetTerminalFields: tt.extraTarget,
					},
					HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
						fields := alignmentRequestFields(t, req)
						initial := fields.Get("operation_type") == "3"
						wantApp := "transfer-app"
						if !initial {
							wantApp = ""
							for key := range fields {
								if strings.HasPrefix(key, "target_terminal_") {
									t.Errorf("post-installation request contains %s", key)
								}
							}
						}
						if got := fields.Get("app_name"); got != wantApp {
							t.Errorf("app_name=%q, want %q", got, wantApp)
						}
						if !initial {
							if got := fields.Get("terminal_id"); got != offer.TargetDevice.IMEI {
								t.Errorf("terminal_id=%q, want target IMEI", got)
							}
							if got := fields.Get("terminal_eid"); got != offer.TargetDevice.EID {
								t.Errorf("terminal_eid=%q, want target EID", got)
							}
						}
						switch Operation(fields.Get("operation")) {
						case OperationManage:
							if initial {
								transfers++
								return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":2,"DownloadInfo":{"ProfileSmdpAddress":"smdp.example"}}}`), nil
							}
							updates++
							if fields.Get("operation_type") != "4" || fields.Get("terminal_iccid") != "installed" {
								t.Error("update does not identify the installed profile")
							}
							return response(200, `{"ap2009":{"OperationResult":1,"SubscriptionResult":3}}`), nil
						case OperationAcquireConf:
							queries++
							if queries == 1 {
								return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":2,"PollingInterval":0}}}`), nil
							}
							return response(200, `{"ap2009":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
						default:
							t.Fatalf("unexpected operation %q", fields.Get("operation"))
							return nil, nil
						}
					}),
				})
				result, err := client.activate(t.Context(), offer, UserInputResult{Button: MessageButtonUnknown})
				if err != nil || result.State != StatePendingDownload {
					t.Fatalf("download result=%v err=%v", result, err)
				}
				result, err = client.CompleteActivation(t.Context(), result, ActivationResult{ICCID: "installed"})
				if err != nil || result.State != StatePendingActivation {
					t.Fatalf("activation result=%v err=%v", result, err)
				}
				result, err = client.Continue(t.Context(), result, ContinueRequest{})
				if err != nil || result.State != StateDone || transfers != 1 || updates != 1 || queries != 2 {
					t.Fatalf("result=%v transfers=%d updates=%d queries=%d err=%v", result, transfers, updates, queries, err)
				}
			})
		}
	}
}

func TestCheckEligibilityTargetTerminalID(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name    string
			version string
			setID   bool
			imeis   []string
			wantID  string
		}{
			{name: "carrier omits target", version: "13.0", imeis: []string{"222222222222222", "333333333333333"}},
			{name: "carrier requests target", version: "13.0", setID: true, imeis: []string{"222222222222222", "333333333333333"}, wantID: "222222222222222"},
			{name: "version enables all targets", version: "14.0", imeis: []string{"222222222222222", "333333333333333"}, wantID: "222222222222222,333333333333333"},
			{name: "version with whitespace", version: " 14.1 ", imeis: []string{"333333333333333"}, wantID: "333333333333333"},
			{name: "missing list uses primary", version: "14.0", wantID: "222222222222222"},
			{name: "malformed version uses carrier flag", version: "invalid", setID: true, imeis: []string{"333333333333333"}, wantID: "222222222222222"},
			{name: "malformed version without flag", version: "invalid", imeis: []string{"333333333333333"}},
		} {
			t.Run(fmt.Sprintf("post_%t/%s", post, tt.name), func(t *testing.T) {
				offer := alignmentOffer()
				offer.TargetDevice.IMEIs = tt.imeis
				offer.TargetDevice.Model = "Pixel"
				calls := 0
				client := newClient(Config{
					Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post, Version: tt.version, SetCheckEligibilityTargetTerminalID: tt.setID},
					HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
						calls++
						fields := alignmentRequestFields(t, req)
						for key, want := range map[string]string{
							"operation":          OperationCheck.String(),
							"terminal_id":        offer.SourceDevice.IMEI,
							"IMSI":               offer.SourceIdentity.IMSI,
							"token":              offer.AuthToken.Value,
							"target_terminal_id": tt.wantID,
						} {
							if got := fields.Get(key); got != want {
								t.Errorf("%s=%q, want %q", key, got, want)
							}
						}
						for key := range fields {
							if strings.HasPrefix(key, "target_terminal_") && key != "target_terminal_id" {
								t.Errorf("unexpected target field %s", key)
							}
						}
						if fields.Has("target_terminal_id") != (tt.wantID != "") || fields.Has("temporary_token") || fields.Has("EAP_ID") || fields.Has("operation_type") {
							t.Error("incorrect eligibility identity or authentication fields")
						}
						return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryAppEligibility":1}}`), nil
					}),
				})
				if _, err := client.CheckEligibility(t.Context(), SubscriptionTransferRequest{Identity: offer.SourceIdentity, Source: offer.SourceDevice, Target: offer.TargetDevice}, offer.AuthToken); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("requests=%d, want 1", calls)
				}
			})
		}
	}
}

func TestTransferTargetTerminalIDs(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name         string
			transferType TransferType
			version      string
			setTarget    bool
			list         bool
			wantTarget   bool
			wantAll      bool
		}{
			{"source credentials", TransferTypeReusableActivationCode, "14.0", false, true, true, true},
			{"source credentials type six", TransferTypeTS43V8, "14.0", false, true, true, true},
			{"temporary token", TransferTypeTemporaryToken, "14.0", true, true, true, true},
			{"type nine carrier target", TransferTypeTemporaryTokenOldTerminalIDs, "14.0", true, true, true, true},
			{"type eleven carrier target", TransferTypeTemporaryTokenVariant11, "14.0", true, true, true, true},
			{"type seven", TransferTypeOldTerminalIDs, "14.0", false, true, true, true},
			{"type two carrier omits target", TransferTypeTemporaryToken, "14.0", false, true, false, false},
			{"type nine carrier omits target", TransferTypeTemporaryTokenOldTerminalIDs, "14.0", false, true, false, false},
			{"type eleven carrier omits target", TransferTypeTemporaryTokenVariant11, "14.0", false, true, false, false},
			{"protocol below threshold", TransferTypeTS43V8, "13.0", false, true, true, false},
			{"primary fallback", TransferTypeTemporaryToken, "14.0", true, false, true, false},
		} {
			t.Run(fmt.Sprintf("post_%t/%s", post, tt.name), func(t *testing.T) {
				offer := alignmentOffer()
				offer.TargetDevice.ICCID = "8920000000000000000"
				offer.TargetDevice.Model = "Pixel"
				offer.TargetDevice.SerialNumber = "target-serial"
				offer.TargetDevice.EntitlementProtocol = 1
				if tt.list {
					offer.TargetDevice.IMEIs = []string{"222222222222222", "333333333333333"}
				}
				var operations []Operation
				client := newClient(Config{
					Entitlement: Entitlement{Server: "https://carrier.example", UsePost: post, TransferType: tt.transferType, Version: tt.version, SetTargetTerminalFields: tt.setTarget},
					HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
						fields := alignmentRequestFields(t, req)
						op := Operation(fields.Get("operation"))
						operations = append(operations, op)
						wantID := ""
						if tt.wantTarget {
							wantID = offer.TargetDevice.IMEI
							if tt.wantAll {
								wantID = strings.Join(offer.TargetDevice.IMEIs, ",")
							}
						}
						if got := fields.Get("target_terminal_id"); got != wantID {
							t.Errorf("%s target_terminal_id=%q, want %q", op, got, wantID)
						}
						wantTerminal := offer.TargetDevice.IMEI
						wantTargetEID, wantTerminalEID := "", offer.TargetDevice.EID
						if tt.wantTarget {
							wantTargetEID = offer.TargetDevice.EID
						}
						if tt.transferType == TransferTypeReusableActivationCode || tt.transferType == TransferTypeTS43V8 {
							wantTerminal = offer.SourceDevice.IMEI
						}
						if tt.transferType == TransferTypeReusableActivationCode || tt.transferType == TransferTypeTS43V8 || tt.transferType == TransferTypeOldTerminalIDs {
							wantTerminalEID = ""
						}
						if fields.Get("terminal_id") != wantTerminal || fields.Get("target_terminal_eid") != wantTargetEID || fields.Get("terminal_eid") != wantTerminalEID {
							t.Error("transfer changed source/target identity or EID placement")
						}
						for _, key := range []string{"terminal_iccid", "target_terminal_iccid", "target_terminal_model", "target_terminal_sn", "target_terminal_imeis", "target_terminal_entitlement_protocol"} {
							if fields.Has(key) {
								t.Errorf("%s contains non-Pixel transfer field %s", op, key)
							}
						}
						if tt.transferType == TransferTypeTemporaryToken || tt.transferType == TransferTypeTemporaryTokenOldTerminalIDs || tt.transferType == TransferTypeTemporaryTokenVariant11 {
							if fields.Get("temporary_token") != "temporary" || fields.Has("token") || fields.Has("IMSI") {
								t.Error("temporary transfer used source authentication")
							}
						} else if fields.Get("token") != offer.AuthToken.Value || fields.Get("IMSI") != offer.SourceIdentity.IMSI || fields.Has("temporary_token") {
							t.Error("transfer lost authentication token")
						}
						if op == OperationManage {
							return response(200, `{"APPLICATION":{"OperationResult":1,"SubscriptionResult":3}}`), nil
						}
						return response(200, `{"APPLICATION":{"OperationResult":1,"PrimaryConfiguration":{"ServiceStatus":1}}}`), nil
					}),
				})
				credential := authCredential(offer.AuthToken)
				if tt.transferType == TransferTypeTemporaryToken || tt.transferType == TransferTypeTemporaryTokenOldTerminalIDs || tt.transferType == TransferTypeTemporaryTokenVariant11 {
					credential = temporaryCredential(TemporaryToken{Value: "temporary"})
				}
				if _, err := client.manageSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, UserInputResult{Button: MessageButtonUnknown}); err != nil {
					t.Fatal(err)
				}
				if _, err := client.acquireConfiguration(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(operations, []Operation{OperationManage, OperationAcquireConf}) {
					t.Fatalf("operations=%v", operations)
				}
			})
		}
	}
}

func TestGoogleOldTerminalFieldsByPhase(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, restricted := range []bool{false, true} {
			for _, tt := range []struct {
				transferType TransferType
				transferOld  bool
				updateOld    bool
			}{
				{TransferTypeTemporaryToken, false, false},
				{TransferTypeReusableActivationCode, true, false},
				{TransferTypeTS43V8, true, false},
				{TransferTypeOldTerminalIDs, true, true},
				{TransferTypeTemporaryTokenOldTerminalIDs, true, true},
				{TransferTypeTemporaryTokenVariant11, false, false},
			} {
				t.Run(fmt.Sprintf("post_%v/restricted_%v/type_%d", post, restricted, tt.transferType), func(t *testing.T) {
					offer := alignmentOffer()
					calls := 0
					client := newClient(Config{
						Entitlement: Entitlement{
							Server:                    "https://carrier.example",
							TransferType:              tt.transferType,
							UsePost:                   post,
							RestrictOldTerminalFields: restricted,
						},
						HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
							fields := alignmentRequestFields(t, req)
							wantOld := !restricted || tt.transferOld
							wantOperation := OperationTypeTransferSubscription
							if calls == 1 {
								wantOld = !restricted || tt.updateOld
								wantOperation = OperationTypeUpdateSubscription
							}
							if got := fields.Get("operation_type"); got != wantOperation.String() {
								t.Errorf("operation_type=%q, want %s", got, wantOperation)
							}
							for key, value := range map[string]string{
								"old_terminal_id":    offer.SourceDevice.IMEI,
								"old_terminal_iccid": offer.SourceDevice.ICCID,
							} {
								if !wantOld {
									value = ""
								}
								if got := fields.Get(key); got != value {
									t.Errorf("operation %s: %s=%q, want %q", wantOperation, key, got, value)
								}
							}
							if got := fields.Get("old_terminal_entitlement_protocol"); got != "1" {
								t.Errorf("old_terminal_entitlement_protocol=%q, want 1", got)
							}
							calls++
							return response(http.StatusOK, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
						}),
					})
					credential := authCredential(offer.AuthToken)
					if transferTypeUsesTemporaryToken(tt.transferType) {
						credential = temporaryCredential(TemporaryToken{Value: "temporary"})
					}
					if _, err := client.manageSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, UserInputResult{Button: MessageButtonUnknown}); err != nil {
						t.Fatal(err)
					}
					if _, err := client.updateSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential); err != nil {
						t.Fatal(err)
					}
					if calls != 2 {
						t.Fatalf("requests=%d, want 2", calls)
					}
				})
			}
		}
	}
}

func TestManageSubscriptionAcceptsActionableResults(t *testing.T) {
	tests := []struct {
		name               string
		subscriptionResult string
		websheetURL        string
		message            string
		activationCode     string
		wantErr            bool
	}{
		{name: "done", subscriptionResult: "3"},
		{name: "download profile", subscriptionResult: "2", activationCode: "LPA:1$smdp.example$MATCH"},
		{name: "websheet", subscriptionResult: "1", websheetURL: "https://example.test/websheet"},
		{name: "delete profile in use", subscriptionResult: "6"},
		{name: "user input", subscriptionResult: "8", message: "Enter PIN"},
		{name: "dismiss", subscriptionResult: "5"},
		{name: "download profile without download info", subscriptionResult: "2", wantErr: true},
		{name: "websheet without URL", subscriptionResult: "1", wantErr: true},
		{name: "user input without message", subscriptionResult: "8", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(*http.Request) (*http.Response, error) {
					body := `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="` + tt.subscriptionResult + `"/>`
					if tt.websheetURL != "" {
						body += `<parm name="SubscriptionServiceURL" value="` + tt.websheetURL + `"/>`
					}
					if tt.message != "" {
						body += `<characteristic type="MSG"><parm name="Message" value="` + tt.message + `"/></characteristic>`
					}
					if tt.activationCode != "" {
						body += `<characteristic type="DownloadInfo"><parm name="ProfileActivationCode" value="` + tt.activationCode + `"/></characteristic>`
					}
					body += `</characteristic></wap-provisioningdoc>`
					return response(200, body), nil
				}),
				Entitlement: Entitlement{Server: "https://example.test/ts43"},
				Logger:      slog.Default(),
			})
			_, err := client.ManageSubscription(
				context.Background(),
				ManageSubscriptionRequest{
					Identity:  Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
					Source:    Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
					Target:    Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
					UserInput: UserInputResult{Button: MessageButtonUnknown},
				},
				AuthToken{Value: "tmp"},
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ManageSubscription() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManageSubscriptionOldTerminalIDGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		sourceProto int
		wantOld     bool
		wantProto   string
	}{
		{name: "default sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43"}, wantOld: true},
		{name: "source credential type three sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode}, wantOld: true},
		{name: "source credential type six sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}, wantOld: true},
		{name: "type seven sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeOldTerminalIDs}, wantOld: true},
		{name: "type nine sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantOld: true},
		{name: "type two sends old IDs by default", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}, wantOld: true},
		{name: "type eleven sends old IDs by default", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenVariant11}, wantOld: true},
		{name: "restricted type two omits old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, RestrictOldTerminalFields: true}},
		{name: "restricted type three sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode, RestrictOldTerminalFields: true}, wantOld: true},
		{name: "restricted type six sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8, RestrictOldTerminalFields: true}, wantOld: true},
		{name: "restricted type seven sends old IDs", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeOldTerminalIDs, RestrictOldTerminalFields: true}, wantOld: true},

		{name: "explicit old terminal protocol sends proto", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeReusableActivationCode}, sourceProto: 1, wantOld: true, wantProto: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("old_terminal_id") != ""; got != tt.wantOld {
						t.Errorf("old_terminal_id present = %v, want %v", got, tt.wantOld)
					}
					if got := query.Get("old_terminal_iccid") != ""; got != tt.wantOld {
						t.Errorf("old_terminal_iccid present = %v, want %v", got, tt.wantOld)
					}
					if got := query.Get("old_terminal_entitlement_protocol"); got != tt.wantProto {
						t.Errorf("old_terminal_entitlement_protocol = %q, want %q", got, tt.wantProto)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			})
			credential := authCredential(AuthToken{Value: "tmp"})
			if transferTypeUsesTemporaryToken(tt.entitlement.TransferType) {
				credential = temporaryCredential(TemporaryToken{Value: "tmp"})
			}
			_, err := client.manageSubscription(
				context.Background(),
				Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EntitlementProtocol: tt.sourceProto},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				credential,
				UserInputResult{Button: MessageButtonUnknown},
			)
			if err != nil {
				t.Fatalf("ManageSubscription() error = %v", err)
			}
		})
	}
}

func TestAcquireConfigurationSourceCredentialShape(t *testing.T) {
	tests := []struct {
		name         string
		transferType TransferType
	}{
		{name: "type three", transferType: TransferTypeReusableActivationCode},
		{name: "type six", transferType: TransferTypeTS43V8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(&Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("operation"); got != OperationAcquireConf.String() {
						t.Errorf("operation = %q, want %s", got, OperationAcquireConf)
					}
					if got := query.Get("operation_type"); got != "" {
						t.Errorf("operation_type = %q, want empty", got)
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
					if got := query.Get("old_terminal_entitlement_protocol"); got != "1" {
						t.Errorf("old_terminal_entitlement_protocol = %q, want 1", got)
					}
					if got := query.Get("old_terminal_id"); got != "" {
						t.Errorf("old_terminal_id = %q, want empty", got)
					}
					if got := query.Get("old_terminal_iccid"); got != "" {
						t.Errorf("old_terminal_iccid = %q, want empty", got)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: Entitlement{
					Server:       "https://example.test/ts43",
					PollTimeout:  time.Second,
					TransferType: tt.transferType,
				},
				Source: Endpoint{Channel: fakeChannel{}, Device: Device{IMEI: "111111111111111"}},
				Target: Endpoint{Device: Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
				Logger: slog.Default(),
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = client.acquireConfiguration(
				context.Background(),
				Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000", EntitlementProtocol: 1},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
				authCredential(AuthToken{Value: "tmp"}),
			)
			if err != nil {
				t.Fatalf("acquireConfiguration() error = %v", err)
			}
		})
	}
}

func TestTransferRequestAuthMode(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantAuth    string
		wantTemp    string
	}{
		{name: "default uses auth token", wantAuth: "transfer"},
		{name: "type two uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken}, wantTemp: "transfer"},
		{name: "source credential type three uses auth token by default", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode}, wantAuth: "transfer"},
		{name: "source credential type six uses auth token by default", entitlement: Entitlement{TransferType: TransferTypeTS43V8}, wantAuth: "transfer"},
		{name: "source credential type three ignores temporary token conversion", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode, UseTempTokenConversion: true}, wantAuth: "transfer"},
		{name: "source credential type six ignores temporary token conversion", entitlement: Entitlement{TransferType: TransferTypeTS43V8, UseTempTokenConversion: true}, wantAuth: "transfer"},
		{name: "type seven uses auth token", entitlement: Entitlement{TransferType: TransferTypeOldTerminalIDs}, wantAuth: "transfer"},
		{name: "type nine uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenOldTerminalIDs}, wantTemp: "transfer"},
		{name: "type eleven uses temporary token", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11}, wantTemp: "transfer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{Entitlement: tt.entitlement})

			req := client.transferRequest(Device{IMEI: "111111111111111"}, client.transferCredentialFromTokens(TransferTokenResult{
				Auth:      AuthToken{Value: "transfer"},
				Temporary: TemporaryToken{Value: "transfer"},
			}))

			if req.AuthenticationToken != tt.wantAuth {
				t.Errorf("AuthenticationToken = %q, want %q", req.AuthenticationToken, tt.wantAuth)
			}
			if req.TemporaryToken != tt.wantTemp {
				t.Errorf("TemporaryToken = %q, want %q", req.TemporaryToken, tt.wantTemp)
			}
		})
	}
}

func TestGoogleManageSubscriptionOperationType(t *testing.T) {
	tests := []struct {
		name         string
		transferType TransferType
	}{
		{name: "default"},
		{name: "temporary token", transferType: TransferTypeTemporaryToken},
		{name: "reusable activation code", transferType: TransferTypeReusableActivationCode},
		{name: "TS43 v8", transferType: TransferTypeTS43V8},
		{name: "old terminal IDs", transferType: TransferTypeOldTerminalIDs},
		{name: "temporary token old terminal IDs", transferType: TransferTypeTemporaryTokenOldTerminalIDs},
		{name: "temporary token variant eleven", transferType: TransferTypeTemporaryTokenVariant11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := manageSubscriptionOperation(
				Entitlement{TransferType: tt.transferType},
				ManageSubscriptionRequest{
					Source:    Device{IMEI: "111111111111111"},
					Target:    Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"},
					UserInput: UserInputResult{Button: MessageButtonUnknown},
				},
			)
			if op.OperationType != OperationTypeTransferSubscription {
				t.Errorf("OperationType = %v, want %v", op.OperationType, OperationTypeTransferSubscription)
			}
		})
	}
}

func TestSubscriptionUpdateAccepted(t *testing.T) {
	tests := []struct {
		name               string
		operationResult    OperationResult
		subscriptionResult SubscriptionResult
		want               bool
	}{
		{name: "Google accepts activated subscription", operationResult: OperationResultSuccess, subscriptionResult: SubscriptionResultDone, want: true},
		{name: "Google rejects dismiss", operationResult: OperationResultSuccess, subscriptionResult: SubscriptionResultDismiss},
		{name: "Google rejects delayed download", operationResult: OperationResultSuccess, subscriptionResult: SubscriptionResultDelayedDownload},

		{name: "operation failure is rejected", operationResult: OperationResultInvalidMessageResponse, subscriptionResult: SubscriptionResultDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subscriptionUpdateAccepted(
				&ODSAResponse{
					OperationResult:    tt.operationResult,
					SubscriptionResult: tt.subscriptionResult,
				},
			)
			if got != tt.want {
				t.Errorf("subscriptionUpdateAccepted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSourceCredentialTransferType(t *testing.T) {
	tests := []struct {
		name string
		t    TransferType
		want bool
	}{
		{name: "type three", t: TransferTypeReusableActivationCode, want: true},
		{name: "type six", t: TransferTypeTS43V8, want: true},
		{name: "temporary token", t: TransferTypeTemporaryToken},
		{name: "old terminal IDs", t: TransferTypeOldTerminalIDs},
		{name: "disabled", t: TransferTypeDisabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSourceCredentialTransferType(tt.t); got != tt.want {
				t.Errorf("IsSourceCredentialTransferType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGoogleIncludeTargetTerminalIdentity(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		want        bool
	}{
		{name: "default sends target identity", want: true},
		{name: "type two follows set target terminal fields", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target terminal fields sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, want: true},
		{name: "source credential type three sends target identity", entitlement: Entitlement{TransferType: TransferTypeReusableActivationCode}, want: true},
		{name: "source credential type six sends target identity", entitlement: Entitlement{TransferType: TransferTypeTS43V8}, want: true},
		{name: "type seven sends target identity", entitlement: Entitlement{TransferType: TransferTypeOldTerminalIDs}, want: true},
		{name: "type nine follows set target terminal fields", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenOldTerminalIDs}},
		{name: "type nine with set target terminal fields sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenOldTerminalIDs, SetTargetTerminalFields: true}, want: true},
		{name: "type eleven follows set target terminal fields", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11}},
		{name: "type eleven with set target terminal fields sends target identity", entitlement: Entitlement{TransferType: TransferTypeTemporaryTokenVariant11, SetTargetTerminalFields: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := includeTargetTerminalIdentity(tt.entitlement); got != tt.want {
				t.Errorf("includeTargetTerminalIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManageSubscriptionTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target terminal fields sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}, wantTarget: true},
		{name: "source credential type six sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}, wantTarget: true},
		{name: "type nine omits target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}},
		{name: "type nine with set target terminal fields sends target identity", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs, SetTargetTerminalFields: true}, wantTarget: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_model"); got != "" {
						t.Errorf("target_terminal_model = %q, want omitted", got)
					}
					wantOldICCID := "8910000000000000000"
					if got := query.Get("old_terminal_iccid"); got != wantOldICCID {
						t.Errorf("old_terminal_iccid = %q, want %q", got, wantOldICCID)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			})

			credential := authCredential(AuthToken{Value: "tmp"})
			if transferTypeUsesTemporaryToken(tt.entitlement.TransferType) {
				credential = temporaryCredential(TemporaryToken{Value: "tmp"})
			}
			_, err := client.manageSubscription(
				context.Background(),
				Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
				Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
				Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
				credential,
				UserInputResult{Button: MessageButtonUnknown},
			)
			if err != nil {
				t.Fatalf("ManageSubscription() error = %v", err)
			}
		})
	}
}

func TestCheckEligibilityTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target fields omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}},
		{name: "source credential type six omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTS43V8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_model") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_model present = %v, want %v", got, tt.wantTarget)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="checked-auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			})

			_, err := client.CheckEligibility(
				context.Background(),
				SubscriptionTransferRequest{
					Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
					Source:   Device{IMEI: "111111111111111"},
					Target:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
				},
				AuthToken{Value: "auth"},
			)
			if err != nil {
				t.Fatalf("CheckEligibility() error = %v", err)
			}
		})
	}
}

func TestAcquireTemporaryTokenTargetIdentityGating(t *testing.T) {
	tests := []struct {
		name        string
		entitlement Entitlement
		wantTarget  bool
	}{
		{name: "type two omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken}},
		{name: "type two with set target fields omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryToken, SetTargetTerminalFields: true}},
		{name: "type nine omits target fields", entitlement: Entitlement{Server: "https://example.test/ts43", TransferType: TransferTypeTemporaryTokenOldTerminalIDs}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newClient(Config{
				HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
					query := req.URL.Query()
					if got := query.Get("target_terminal_id") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_id present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_eid") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_eid present = %v, want %v", got, tt.wantTarget)
					}
					if got := query.Get("target_terminal_model") != ""; got != tt.wantTarget {
						t.Errorf("target_terminal_model present = %v, want %v", got, tt.wantTarget)
					}
					return response(200, `<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="TemporaryToken" value="tmp"/><parm name="TemporaryTokenExpiry" value="2030-01-01T00:00:00Z"/><parm name="OperationTargets" value="ManageSubscription,AcquireConfiguration"/></characteristic></wap-provisioningdoc>`), nil
				}),
				Entitlement: tt.entitlement,
				Logger:      slog.Default(),
			})

			tokens, err := client.AcquireTemporaryToken(
				context.Background(),
				SubscriptionTransferRequest{
					Identity: Identity{IMSI: "310260123456789", MCC: "310", MNC: "260"},
					Source:   Device{IMEI: "111111111111111"},
					Target:   Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000", Model: "Pixel"},
				},
				AuthToken{Value: "auth"},
			)
			if err != nil {
				t.Fatalf("AcquireTemporaryToken() error = %v", err)
			}
			if !slices.Equal(tokens.Temporary.Targets, []Operation{OperationManage, OperationAcquireConf}) {
				t.Errorf("Temporary.Targets = %v, want ManageSubscription and AcquireConfiguration", tokens.Temporary.Targets)
			}
		})
	}
}

func TestTransferRequestsSendTargetAndConfiguredFields(t *testing.T) {
	client := newClient(Config{
		HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
			query := req.URL.Query()
			switch Operation(query.Get("operation")) {
			case OperationManage, OperationAcquireConf:
				want := map[string]string{
					"target_terminal_id":               "222222222222222",
					"target_terminal_eid":              "89049032000001000000000000000000",
					"companion_terminal_id":            "444444444444444",
					"companion_terminal_vendor":        "Fit",
					"companion_terminal_model":         "Watch",
					"companion_terminal_sw_version":    "1.0",
					"companion_terminal_friendly_name": "Pixel Watch",
					"companion_terminal_service":       "SharedNumber",
					"companion_terminal_iccid":         "8910000000000000001",
					"companion_terminal_eid":           "89049032000001000000000000000001",
				}
				for key, value := range want {
					if got := query.Get(key); got != value {
						t.Errorf("%s = %q, want %q", key, got, value)
					}
				}
				for _, key := range []string{"target_terminal_model", "target_terminal_sn", "target_terminal_imeis"} {
					if query.Has(key) {
						t.Errorf("unexpected transfer field %s", key)
					}
				}
			default:
				t.Fatalf("unexpected operation %q", query.Get("operation"))
			}
			if Operation(query.Get("operation")) == OperationManage {
				return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
			}
			return response(200, `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`), nil
		}),
		Entitlement: Entitlement{
			Server:      "https://example.test/ts43",
			PollTimeout: time.Second,
		},
		Logger: slog.Default(),
		Companion: Device{
			IMEI:            "444444444444444",
			ICCID:           "8910000000000000001",
			EID:             "89049032000001000000000000000001",
			Vendor:          "Fit",
			Model:           "Watch",
			SoftwareVersion: "1.0",
			FriendlyName:    "Pixel Watch",
			Service:         "SharedNumber",
		},
	})

	_, err := client.activate(context.Background(), &transferOffer{
		SourceIdentity: Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"},
		SourceDevice:   Device{IMEI: "111111111111111", ICCID: "8910000000000000000"},
		TargetDevice: Device{
			IMEI:         "222222222222222",
			IMEIs:        []string{"222222222222222", "333333333333333"},
			EID:          "89049032000001000000000000000000",
			Model:        "Pixel",
			SerialNumber: "serial",
		},
		AuthToken: AuthToken{Value: "tmp"},
	}, UserInputResult{Button: MessageButtonUnknown})
	if err != nil {
		t.Fatalf("activate() error = %v", err)
	}
}
