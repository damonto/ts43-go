package ts43

import (
	"encoding/json"
	"testing"
)

func TestCarrierMatchScore(t *testing.T) {
	pattern := "26203293x"
	empty := ""
	tests := []struct {
		name     string
		match    carrierMatch
		identity Identity
		want     int
		ok       bool
	}{
		{
			name:     "missing IMSI condition is a wildcard",
			match:    carrierMatch{MCCMNC: "00101"},
			identity: Identity{MCC: "001", MNC: "01", IMSI: "001010123456789"},
			want:     0,
			ok:       true,
		},
		{
			name:     "IMSI prefix with wildcard matches",
			match:    carrierMatch{MCCMNC: "26203", IMSIPrefixXPattern: &pattern},
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262032931234567"},
			want:     1,
			ok:       true,
		},
		{
			name:     "IMSI prefix mismatch rejects",
			match:    carrierMatch{MCCMNC: "26203", IMSIPrefixXPattern: &pattern},
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262033301234567"},
			ok:       false,
		},
		{
			name:     "explicit empty IMSI pattern matches empty IMSI",
			match:    carrierMatch{MCCMNC: "00101", IMSIPrefixXPattern: &empty},
			identity: Identity{MCC: "001", MNC: "01"},
			want:     1,
			ok:       true,
		},
		{
			name:     "explicit empty IMSI pattern rejects non-empty IMSI",
			match:    carrierMatch{MCCMNC: "00101", IMSIPrefixXPattern: &empty},
			identity: Identity{MCC: "001", MNC: "01", IMSI: "001010123456789"},
			ok:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.match.score(tt.identity)
			if ok != tt.ok {
				t.Errorf("score() matched = %t, want %t", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("score() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEmbeddedCarrierConfigsIncludeIdentityFields(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "WFC", data: wfcJSON},
		{name: "entitlement", data: entitlementJSON},
	}
	wantFields := []string{"gid1", "gid2", "spn", "imsiPrefixXpattern"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(tt.data, &records); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			for i, record := range records {
				for _, field := range wantFields {
					if _, ok := record[field]; !ok {
						t.Errorf("record %d is missing %q", i, field)
					}
				}
			}
		})
	}
}
