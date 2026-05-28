package ts43

import (
	"errors"
	"testing"
	"time"

	"github.com/damonto/ts43-go/sim"
)

func TestDiscoverEntitlement(t *testing.T) {
	tests := []struct {
		name          string
		identity      sim.Identity
		sourceSIMType SIMType
		want          Entitlement
		wantErr       error
	}{
		{
			name:     "GID1 match wins over MCCMNC fallback",
			identity: sim.Identity{MCC: "204", MNC: "04", GID1: "5A"},
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
			identity: sim.Identity{MCC: "204", MNC: "04", GID1: "5a"},
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
			name:     "unique MCCMNC fallback",
			identity: sim.Identity{MCC: "310", MNC: "260"},
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
			name:          "pSIM transfer type",
			identity:      sim.Identity{MCC: "230", MNC: "03"},
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
			name:     "eSIM transfer type",
			identity: sim.Identity{MCC: "230", MNC: "03"},
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
			name:     "ambiguous MCCMNC fallback",
			identity: sim.Identity{MCC: "208", MNC: "01"},
			wantErr:  ErrEntitlementAmbiguous,
		},
		{
			name:     "missing MCCMNC",
			identity: sim.Identity{MCC: "999", MNC: "99"},
			wantErr:  ErrEntitlementNotFound,
		},
		{
			name:          "unsupported pSIM transfer",
			identity:      sim.Identity{MCC: "310", MNC: "012"},
			sourceSIMType: SIMTypePSIM,
			wantErr:       ErrEntitlementUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscoverEntitlement(tt.identity, tt.sourceSIMType)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("DiscoverEntitlement() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverEntitlement() error = %v", err)
			}
			if got.Server != tt.want.Server {
				t.Fatalf("Server = %q, want %q", got.Server, tt.want.Server)
			}
			if got.TransferType != tt.want.TransferType {
				t.Fatalf("TransferType = %v, want %v", got.TransferType, tt.want.TransferType)
			}
			if got.UsePost != tt.want.UsePost {
				t.Fatalf("UsePost = %t, want %t", got.UsePost, tt.want.UsePost)
			}
			if got.PollInterval != tt.want.PollInterval {
				t.Fatalf("PollInterval = %s, want %s", got.PollInterval, tt.want.PollInterval)
			}
			if got.PollTimeout != tt.want.PollTimeout {
				t.Fatalf("PollTimeout = %s, want %s", got.PollTimeout, tt.want.PollTimeout)
			}
			if got.AppName != tt.want.AppName {
				t.Fatalf("AppName = %q, want %q", got.AppName, tt.want.AppName)
			}
			if got.Version != tt.want.Version {
				t.Fatalf("Version = %q, want %q", got.Version, tt.want.Version)
			}
		})
	}
}

func TestDiscoverEntitlementForSamsungChannel(t *testing.T) {
	tests := []struct {
		name          string
		identity      sim.Identity
		sourceSIMType SIMType
		want          Entitlement
	}{
		{
			name:     "Samsung SK Telecom eSIM transfer",
			identity: sim.Identity{MCC: "450", MNC: "05"},
			want: Entitlement{
				Server:                  "https://entitlements-prod.sktdns.com:9901/DEGDROIDInterfaceTS43",
				Channel:                 EntitlementChannelSamsung,
				TransferType:            TransferTypeTS43V8,
				SetTargetTerminalFields: true,
				App:                     AppODSAPrimary,
				AppName:                 AppODSAPrimary,
				ProvisioningVersion:     "5.0",
				RequestVersion:          "1",
				Version:                 "8.0",
			},
		},
		{
			name:          "Samsung SK Telecom pSIM transfer keeps old terminal ICCID shape",
			identity:      sim.Identity{MCC: "450", MNC: "05"},
			sourceSIMType: SIMTypePSIM,
			want: Entitlement{
				Server:                  "https://entitlements-prod.sktdns.com:9901/DEGDROIDInterfaceTS43",
				Channel:                 EntitlementChannelSamsung,
				TransferType:            TransferTypeOldTerminalIDs,
				SetTargetTerminalFields: true,
				App:                     AppODSAPrimary,
				AppName:                 AppODSAPrimary,
				ProvisioningVersion:     "5.0",
				RequestVersion:          "1",
				Version:                 "8.0",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscoverEntitlementForChannel(tt.identity, tt.sourceSIMType, EntitlementChannelSamsung)
			if err != nil {
				t.Fatalf("DiscoverEntitlementForChannel() error = %v", err)
			}
			if got.Server != tt.want.Server {
				t.Fatalf("Server = %q, want %q", got.Server, tt.want.Server)
			}
			if got.Channel != tt.want.Channel {
				t.Fatalf("Channel = %q, want %q", got.Channel, tt.want.Channel)
			}
			if got.TransferType != tt.want.TransferType {
				t.Fatalf("TransferType = %v, want %v", got.TransferType, tt.want.TransferType)
			}
			if got.SourceSIMType != tt.sourceSIMType {
				t.Fatalf("SourceSIMType = %v, want %v", got.SourceSIMType, tt.sourceSIMType)
			}
			if got.SetTargetTerminalFields != tt.want.SetTargetTerminalFields {
				t.Fatalf("SetTargetTerminalFields = %t, want %t", got.SetTargetTerminalFields, tt.want.SetTargetTerminalFields)
			}
			if got.App != tt.want.App {
				t.Fatalf("App = %q, want %q", got.App, tt.want.App)
			}
			if got.AppName != tt.want.AppName {
				t.Fatalf("AppName = %q, want %q", got.AppName, tt.want.AppName)
			}
			if got.ProvisioningVersion != tt.want.ProvisioningVersion {
				t.Fatalf("ProvisioningVersion = %q, want %q", got.ProvisioningVersion, tt.want.ProvisioningVersion)
			}
			if got.RequestVersion != tt.want.RequestVersion {
				t.Fatalf("RequestVersion = %q, want %q", got.RequestVersion, tt.want.RequestVersion)
			}
			if got.Version != tt.want.Version {
				t.Fatalf("Version = %q, want %q", got.Version, tt.want.Version)
			}
			if got.Timeout != tt.want.Timeout {
				t.Fatalf("Timeout = %s, want %s", got.Timeout, tt.want.Timeout)
			}
		})
	}
}

func TestDiscoverEntitlementAutoChannel(t *testing.T) {
	tests := []struct {
		name          string
		identity      sim.Identity
		sourceSIMType SIMType
		wantChannel   EntitlementChannel
		wantServer    string
	}{
		{
			name:        "prefers Google when Google has the carrier",
			identity:    sim.Identity{MCC: "310", MNC: "260"},
			wantChannel: EntitlementChannelGoogle,
			wantServer:  "https://ts43.eas3.msg.t-mobile.com/google/",
		},
		{
			name:        "falls back to Samsung when Google has no carrier key",
			identity:    sim.Identity{MCC: "450", MNC: "05"},
			wantChannel: EntitlementChannelSamsung,
			wantServer:  "https://entitlements-prod.sktdns.com:9901/DEGDROIDInterfaceTS43",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscoverEntitlement(tt.identity, tt.sourceSIMType)
			if err != nil {
				t.Fatalf("DiscoverEntitlement() error = %v", err)
			}
			if got.Channel != tt.wantChannel {
				t.Fatalf("Channel = %q, want %q", got.Channel, tt.wantChannel)
			}
			if got.Server != tt.wantServer {
				t.Fatalf("Server = %q, want %q", got.Server, tt.wantServer)
			}
		})
	}
}
