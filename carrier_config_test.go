package ts43

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"testing"
)

func TestConfigCodecs(t *testing.T) {
	for _, tt := range []struct {
		name  string
		data  string
		value interface {
			encoding.BinaryMarshaler
			encoding.BinaryUnmarshaler
		}
	}{
		{"entitlement", `[{"mccmnc":"00101","carrierName":"test","channel":"google","entitlementServer":"https://one.example","supportEsimToEsimTransfer":true,"transferTypeEsim":7}]`, new(EntitlementConfigs)},
		{"wfc", `[{"mccmnc":"00101","carrierName":"test","entitlementServer":"https://wfc.example"}]`, new(WFCConfigs)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			zero, err := json.Marshal(tt.value)
			if err != nil || string(zero) != "[]" {
				t.Fatalf("zero = %s, %v", zero, err)
			}
			input := []byte(tt.data)
			if err := tt.value.UnmarshalBinary(input); err != nil {
				t.Fatal(err)
			}
			clear(input)
			original, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			binary, err := tt.value.MarshalBinary()
			if err != nil || !bytes.Equal(binary, original) {
				t.Fatalf("binary = %s, %v", binary, err)
			}
			clear(binary)
			for _, data := range []string{"", "null", "{}", "[] garbage", "[", `[1]`} {
				if err := tt.value.UnmarshalBinary([]byte(data)); err == nil {
					t.Errorf("accepted %q", data)
				}
				got, err := json.Marshal(tt.value)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("invalid decode changed config: %s, %v", got, err)
				}
			}
			if err := json.Unmarshal(original, tt.value); err != nil {
				t.Fatal(err)
			}
			got, err := tt.value.MarshalBinary()
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("roundtrip = %s, %v", got, err)
			}
			identity := Identity{MCC: "001", MNC: "01"}
			switch config := tt.value.(type) {
			case *EntitlementConfigs:
				found, err := config.Discover(identity, SIMTypeESIM)
				if err != nil || found.Server != "https://one.example" {
					t.Fatalf("entitlement = %+v, %v", found, err)
				}
			case *WFCConfigs:
				found, err := config.Discover(identity)
				if err != nil || found.Server != "https://wfc.example" {
					t.Fatalf("WFC = %+v, %v", found, err)
				}
			}
		})
	}
}

func TestMissingConfig(t *testing.T) {
	var entitlements *EntitlementConfigs
	if _, err := entitlements.Discover(Identity{MCC: "001", MNC: "01"}, SIMTypeESIM); !errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("entitlement = %v", err)
	}
	var wfc *WFCConfigs
	if _, err := wfc.Discover(Identity{MCC: "001", MNC: "01"}); !errors.Is(err, ErrWFCConfigNotFound) {
		t.Fatalf("WFC = %v", err)
	}
}
