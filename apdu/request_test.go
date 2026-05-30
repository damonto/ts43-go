package apdu

import (
	"bytes"
	"testing"
)

func TestRequestMarshalBinary(t *testing.T) {
	le := byte(0x00)
	tests := []struct {
		name    string
		request Request
		want    []byte
	}{
		{
			name:    "header only",
			request: Request{CLA: 0x00, INS: 0xa4, P1: 0x00, P2: 0x04},
			want:    []byte{0x00, 0xa4, 0x00, 0x04},
		},
		{
			name:    "data and le",
			request: Request{CLA: 0x00, INS: 0xa4, P1: 0x04, P2: 0x04, Data: []byte{0xa0, 0x00}, Le: &le},
			want:    []byte{0x00, 0xa4, 0x04, 0x04, 0x02, 0xa0, 0x00, 0x00},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.request.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("MarshalBinary = %x, want %x", got, tt.want)
			}
		})
	}
}
