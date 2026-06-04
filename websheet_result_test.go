package ts43

import "testing"

func TestWebsheetResultUnmarshalText(t *testing.T) {
	tests := []struct {
		name           string
		text           string
		wantEvent      WebsheetCallbackEvent
		wantActivation string
		wantSMDP       string
		wantICCID      string
		wantIMEI       string
		wantNext       Operation
	}{
		{
			name:           "activation JavaScript callback",
			text:           `ODSAServiceFlow.profileReadyWithActivationCode("LPA:1$smdp.example$matching","8900000000000000000","123456789012345")`,
			wantEvent:      WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
			wantIMEI:       "123456789012345",
		},
		{
			name:      "default smdp JSON callback",
			text:      `{"event":"profileReadyWithDefaultSmdp","defaultSmdpAddress":"smdp.example","iccid":"8900000000000000000","imei":"123456789012345"}`,
			wantEvent: WebsheetEventProfileReadyWithDefaultSMDP,
			wantSMDP:  "smdp.example",
			wantICCID: "8900000000000000000",
			wantIMEI:  "123456789012345",
		},
		{
			name:      "prefixed adapter JSON callback",
			text:      `TS43_ODSA_CALLBACK {"event":"finishFlow","nextAction":"AcquireConfiguration","source":"odsa"}`,
			wantEvent: WebsheetEventFinishFlow,
			wantNext:  OperationAcquireConf,
		},
		{
			name:      "VoWiFi entitlement changed JSON callback",
			text:      `TS43_VOWIFI_CALLBACK {"event":"entitlementChanged","source":"vowifi"}`,
			wantEvent: WebsheetEventVoWiFiEntitlementChanged,
		},
		{
			name:      "VoWiFi dismiss JavaScript callback",
			text:      `VoWiFiWebServiceFlow.dismissFlow()`,
			wantEvent: WebsheetEventVoWiFiDismissFlow,
		},
		{
			name:           "callback URL activation",
			text:           "https://carrier.test/_callback/esim?activationCode=LPA%3A1%24smdp.example%24matching&iccid=8900000000000000000",
			wantEvent:      WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got WebsheetResult
			if err := got.UnmarshalText([]byte(tt.text)); err != nil {
				t.Fatalf("UnmarshalText error = %v", err)
			}
			if got.Event != tt.wantEvent {
				t.Fatalf("Event = %d, want %d", got.Event, tt.wantEvent)
			}
			if got.ActivationCode != tt.wantActivation {
				t.Fatalf("ActivationCode = %q, want %q", got.ActivationCode, tt.wantActivation)
			}
			if got.SMDPFQDN != tt.wantSMDP {
				t.Fatalf("SMDPFQDN = %q, want %q", got.SMDPFQDN, tt.wantSMDP)
			}
			if got.ICCID != tt.wantICCID {
				t.Fatalf("ICCID = %q, want %q", got.ICCID, tt.wantICCID)
			}
			if got.IMEI != tt.wantIMEI {
				t.Fatalf("IMEI = %q, want %q", got.IMEI, tt.wantIMEI)
			}
			if got.NextAction != tt.wantNext {
				t.Fatalf("NextAction = %q, want %q", got.NextAction, tt.wantNext)
			}
		})
	}
}
