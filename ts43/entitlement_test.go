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
