package ts43

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestIMSIEAPIdentityFormatsMNC(t *testing.T) {
	tests := []struct {
		name   string
		mccmnc string
		imsi   string
		want   string
	}{
		{
			name:   "pads two digit MNC for realm",
			mccmnc: "23410",
			imsi:   "234101234567890",
			want:   "0234101234567890@nai.epc.mnc010.mcc234.3gppnetwork.org",
		},
		{
			name:   "keeps three digit MNC",
			mccmnc: "310260",
			imsi:   "310260123456789",
			want:   "0310260123456789@nai.epc.mnc260.mcc310.3gppnetwork.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IMSIEAPIdentity(tt.mccmnc, tt.imsi, DefaultEAPAkaRealm)
			if err != nil {
				t.Fatalf("IMSIEAPIdentity() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IMSIEAPIdentity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRespondToChallengeMatchesAndroidVector(t *testing.T) {
	tests := []struct {
		name      string
		challenge string
		response  string
		identity  string
		want      string
	}{
		{
			name:      "Android vector",
			challenge: "AQIAfBcBAAABBQAAXOZSkCjxysgE43GWqHJvgQIFAABrikWGrekAALNU4TxmCDPoCwUAAJT0nqXeAYlqzT0UGXINENWBBQAA7z3fhImkg+vcCKWIZBdvuYIJAAAPRUFp7KWFo+Thr78Qj9hEkB2zA0i6KakODsufBC+BJQ==",
			response:  "2wjHnwKln8mjjxDzMKJvLBzMHtm0X9SNBsUWEAbEiAdD7xeqqZ7nsXzukRkIhd6SDZ4bj7s=",
			identity:  "0234107813240779@nai.epc.mnc010.mcc234.3gppnetwork.org",
			want:      "020200281701000003030040C79F02A59FC9A38F0B0500001C141BDDDE8BEF2E502FB6793808DE7C",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responseData, err := base64.StdEncoding.DecodeString(tt.response)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			want, err := hex.DecodeString(tt.want)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}

			var challenge AKAChallenge
			if err := challenge.UnmarshalText([]byte(tt.challenge)); err != nil {
				t.Fatalf("UnmarshalText() error = %v", err)
			}
			var response AKAResponse
			if err := response.UnmarshalBinary(responseData); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}
			got, err := RespondToChallenge(&challenge, &response, tt.identity)
			if err != nil {
				t.Fatalf("RespondToChallenge() error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("RespondToChallenge() = %X, want %X", got, want)
			}
		})
	}
}
