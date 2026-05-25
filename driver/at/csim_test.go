package at

import (
	"bytes"
	"encoding"
	"strings"
	"testing"
)

var (
	_ encoding.TextMarshaler   = CSIMCommand(nil)
	_ encoding.TextMarshaler   = CSIMResponse(nil)
	_ encoding.TextUnmarshaler = (*CSIMResponse)(nil)
)

func TestCSIMCommandMarshalText(t *testing.T) {
	tests := []struct {
		name    string
		command CSIMCommand
		want    string
	}{
		{name: "select", command: CSIMCommand{0x00, 0xA4, 0x04, 0x00}, want: `AT+CSIM=8,"00A40400"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.command.MarshalText()
			if err != nil {
				t.Fatalf("CSIMCommand.MarshalText failed: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("CSIMCommand.MarshalText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCSIMResponseMarshalText(t *testing.T) {
	tests := []struct {
		name     string
		response CSIMResponse
		want     string
	}{
		{name: "ok", response: CSIMResponse{0x90, 0x00}, want: `4,"9000"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.response.MarshalText()
			if err != nil {
				t.Fatalf("CSIMResponse.MarshalText failed: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("CSIMResponse.MarshalText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCSIMResponseUnmarshalText(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     []byte
	}{
		{
			name:     "prefixed",
			response: "+CMTI: \"SM\",1\n+CSIM: 4,\"9000\"",
			want:     []byte{0x90, 0x00},
		},
		{
			name:     "bare hex",
			response: "+CMTI: \"SM\",1\n9000",
			want:     []byte{0x90, 0x00},
		},
		{
			name:     "bare quoted hex",
			response: `"9000"`,
			want:     []byte{0x90, 0x00},
		},
		{
			name:     "bare length and quoted hex",
			response: `4,"9000"`,
			want:     []byte{0x90, 0x00},
		},
		{
			name:     "bare length and hex",
			response: `4,9000`,
			want:     []byte{0x90, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got CSIMResponse
			if err := got.UnmarshalText([]byte(tt.response)); err != nil {
				t.Fatalf("CSIMResponse.UnmarshalText failed: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("CSIMResponse.UnmarshalText = % X, want % X", got, tt.want)
			}
		})
	}
}

func TestCSIMResponseUnmarshalTextRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "length mismatch", response: "+CSIM: 2,\"9000\"", want: "CSIM response length mismatch"},
		{name: "invalid hex", response: "not-hex", want: "invalid CSIM response data"},
		{name: "bare short response", response: "90", want: "missing response status word"},
		{name: "unknown bare status word", response: "DEADBEEF", want: "unrecognized APDU status word"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response CSIMResponse
			err := response.UnmarshalText([]byte(tt.response))
			if err == nil {
				t.Fatal("CSIMResponse.UnmarshalText error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CSIMResponse.UnmarshalText error = %q, want containing %q", err.Error(), tt.want)
			}
		})
	}
}
