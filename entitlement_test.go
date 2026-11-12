package ts43

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDiscoverTransferConfiguration(t *testing.T) {
	var configs EntitlementConfigs
	if err := json.Unmarshal([]byte(`[
		{"mccmnc":"00101","carrierName":"generic","entitlementServer":"https://generic.example","supportPsimToEsimTransfer":true,"supportEsimToEsimTransfer":true,"transferTypePsim":9,"transferTypeEsim":2},
		{"mccmnc":"00101","gid1":"AA","carrierName":"specific","entitlementServer":"https://specific.example","supportPsimToEsimTransfer":true,"transferTypePsim":7}
	]`), &configs); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name          string
		identity      Identity
		sourceSIMType SIMType
		wantServer    string
		wantType      TransferType
		wantErr       error
	}{
		{
			name: "eSIM transfer", identity: Identity{MCC: "001", MNC: "01"},
			wantServer: "https://generic.example", wantType: TransferTypeTemporaryToken,
		},
		{
			name: "pSIM conversion", identity: Identity{MCC: "001", MNC: "01"}, sourceSIMType: SIMTypePSIM,
			wantServer: "https://generic.example", wantType: TransferTypeTemporaryTokenOldTerminalIDs,
		},
		{
			name: "GID prefix selects specific carrier", identity: Identity{MCC: "001", MNC: "01", GID1: "aa1234"}, sourceSIMType: SIMTypePSIM,
			wantServer: "https://specific.example", wantType: TransferTypeOldTerminalIDs,
		},
		{
			name: "unsupported specific carrier does not use generic record", identity: Identity{MCC: "001", MNC: "01", GID1: "aa1234"},
			wantErr: ErrEntitlementUnsupported,
		},
		{
			name: "unknown carrier", identity: Identity{MCC: "001", MNC: "02"},
			wantErr: ErrEntitlementNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configs.Discover(tt.identity, tt.sourceSIMType)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Discover() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Server != tt.wantServer || got.TransferType != tt.wantType || got.SourceSIMType != tt.sourceSIMType {
				t.Fatalf("Discover() = %+v, want server %q, type %v, source SIM %v", got, tt.wantServer, tt.wantType, tt.sourceSIMType)
			}
		})
	}
}

func TestDiscoverEntitlement(t *testing.T) {
	tests := []struct {
		name          string
		identity      Identity
		sourceSIMType SIMType
		want          Entitlement
		wantErr       error
	}{
		{
			name:     "GID1 match wins over MCCMNC fallback",
			identity: Identity{MCC: "204", MNC: "04", GID1: "5A"},
			want: Entitlement{
				Server:       "https://aes.mnc780.mcc302.pub.3gppnetwork.org",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "GID1 match is case insensitive",
			identity: Identity{MCC: "204", MNC: "04", GID1: "5a"},
			want: Entitlement{
				Server:       "https://aes.mnc780.mcc302.pub.3gppnetwork.org",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "GID1 and GID2 match wins over MCCMNC fallback",
			identity: Identity{MCC: "311", MNC: "480", GID1: "ba01570000000000", GID2: "b100000000000000"},
			want: Entitlement{
				Server:       "https://spc-ecs.myvzw.com:443/",
				TransferType: TransferTypeTS43V8,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				Version:      "8.0",
			},
		},
		{
			name:     "unique MCCMNC fallback",
			identity: Identity{MCC: "310", MNC: "260"},
			want: Entitlement{
				Server:       "https://ts43.eas3.msg.t-mobile.com/google/",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "Austrian two-digit MNC carrier key",
			identity: Identity{MCC: "232", MNC: "05"},
			want: Entitlement{
				Server:       "https://esim-es-open-prod-3at.esimcloud.net/3at/prod/executeGSMAActions",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "11.0",
			},
		},
		{
			name:          "pSIM transfer type selected by SPN",
			identity:      Identity{MCC: "230", MNC: "03", SPN: "Vodafone CZ"},
			sourceSIMType: SIMTypePSIM,
			want: Entitlement{
				Server:       "https://es-cz.multiopco.entitlement.vodafone.com:5452/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "eSIM transfer type selected by SPN",
			identity: Identity{MCC: "230", MNC: "03", SPN: "Vodafone CZ"},
			want: Entitlement{
				Server:       "https://es-cz.multiopco.entitlement.vodafone.com:5452/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeReusableActivationCode,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "IMSI prefix wildcard selects carrier",
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262032931234567"},
			want: Entitlement{
				Server:                 "https://aes.mnc023.mcc262.pub.3gppnetwork.org/",
				TransferType:           TransferTypeTemporaryToken,
				PollInterval:           2 * time.Minute,
				PollTimeout:            10 * time.Minute,
				UseTempTokenConversion: true,
				AppName:                "androidTransfer",
				Version:                "9.0",
			},
		},
		{
			name:     "IMSI and SPN conditions match together",
			identity: Identity{MCC: "262", MNC: "02", IMSI: "262021910123456", SPN: "Vodafone.de"},
			want: Entitlement{
				Server:       "https://es-de.vodafone.com:5445/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeReusableActivationCode,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "SPN without required IMSI does not match",
			identity: Identity{MCC: "262", MNC: "02", SPN: "Vodafone.de"},
			wantErr:  ErrEntitlementNotFound,
		},
		{
			name:     "missing MCCMNC",
			identity: Identity{MCC: "999", MNC: "99"},
			wantErr:  ErrEntitlementNotFound,
		},
		{
			name:          "unsupported pSIM transfer",
			identity:      Identity{MCC: "310", MNC: "012"},
			sourceSIMType: SIMTypePSIM,
			wantErr:       ErrEntitlementUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateEntitlementConfigs(t).Discover(tt.identity, tt.sourceSIMType)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("privateEntitlementConfigs(t).Discover() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("privateEntitlementConfigs(t).Discover() error = %v", err)
			}
			if got.Server != tt.want.Server {
				t.Errorf("Server = %q, want %q", got.Server, tt.want.Server)
			}
			if got.TransferType != tt.want.TransferType {
				t.Errorf("TransferType = %v, want %v", got.TransferType, tt.want.TransferType)
			}
			if got.UsePost != tt.want.UsePost {
				t.Errorf("UsePost = %t, want %t", got.UsePost, tt.want.UsePost)
			}
			if got.UseTempTokenConversion != tt.want.UseTempTokenConversion {
				t.Errorf("UseTempTokenConversion = %t, want %t", got.UseTempTokenConversion, tt.want.UseTempTokenConversion)
			}
			if got.PollInterval != tt.want.PollInterval {
				t.Errorf("PollInterval = %s, want %s", got.PollInterval, tt.want.PollInterval)
			}
			if got.PollTimeout != tt.want.PollTimeout {
				t.Errorf("PollTimeout = %s, want %s", got.PollTimeout, tt.want.PollTimeout)
			}
			if got.AppName != tt.want.AppName {
				t.Errorf("AppName = %q, want %q", got.AppName, tt.want.AppName)
			}
			if got.Version != tt.want.Version {
				t.Errorf("Version = %q, want %q", got.Version, tt.want.Version)
			}
		})
	}
}

func TestSelectEntitlementRecordIdentityMatching(t *testing.T) {
	gid1 := "AA"
	gid2 := "BB"
	spn := "Example"
	records := []entitlementRecord{
		{MCCMNC: "00101", CarrierName: "fallback", EntitlementServer: "fallback"},
		{MCCMNC: "00101", GID1: &gid1, CarrierName: "gid1", EntitlementServer: "gid1"},
		{MCCMNC: "00101", GID1: &gid1, GID2: &gid2, CarrierName: "gid12", EntitlementServer: "gid12"},
		{MCCMNC: "00101", SPN: &spn, CarrierName: "spn", EntitlementServer: "spn"},
	}
	tests := []struct {
		name     string
		identity Identity
		want     string
		wantErr  error
	}{
		{
			name:     "most specific GID1 and GID2 match wins",
			identity: Identity{MCC: "001", MNC: "01", GID1: "aa", GID2: "bb"},
			want:     "gid12",
		},
		{
			name:     "SPN match wins over MCCMNC fallback",
			identity: Identity{MCC: "001", MNC: "01", SPN: "Example"},
			want:     "spn",
		},
		{
			name:     "mismatched specific fields use fallback",
			identity: Identity{MCC: "001", MNC: "01", GID1: "CC"},
			want:     "fallback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectEntitlementRecord(records, tt.identity)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("selectEntitlementRecord() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectEntitlementRecord() error = %v", err)
			}
			if got.CarrierName != tt.want {
				t.Errorf("CarrierName = %q, want %q", got.CarrierName, tt.want)
			}
		})
	}
}

func TestSelectEntitlementRecordEqualSpecificityIsAmbiguous(t *testing.T) {
	first := "AA"
	second := "AA"
	records := []entitlementRecord{
		{MCCMNC: "00101", GID1: &first, CarrierName: "first", EntitlementServer: "first"},
		{MCCMNC: "00101", GID1: &second, CarrierName: "second", EntitlementServer: "second"},
	}
	_, err := selectEntitlementRecord(records, Identity{MCC: "001", MNC: "01", GID1: "aa"})
	if !errors.Is(err, ErrEntitlementAmbiguous) {
		t.Errorf("selectEntitlementRecord() error = %v, want %v", err, ErrEntitlementAmbiguous)
	}
}
