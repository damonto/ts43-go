package ccid

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type scriptedTransmitter struct {
	responses [][]byte
	writes    [][]byte
}

func (s *scriptedTransmitter) transmit(command []byte) ([]byte, error) {
	s.writes = append(s.writes, append([]byte(nil), command...))
	if len(s.responses) == 0 {
		return nil, errors.New("unexpected transmit")
	}
	response := append([]byte(nil), s.responses[0]...)
	s.responses = s.responses[1:]
	return response, nil
}

func TestCCIDReaderTransmitStateErrors(t *testing.T) {
	tests := []struct {
		name   string
		reader CCIDReader
		want   string
	}{
		{
			name:   "closed",
			reader: CCIDReader{closed: true},
			want:   "ccid reader is closed",
		},
		{
			name:   "not connected",
			reader: CCIDReader{},
			want:   "ccid reader is not connected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.reader.transmit([]byte{0x00})
			if err == nil {
				t.Fatal("transmit error = nil, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("transmit error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestTransmitUSIM(t *testing.T) {
	tests := []struct {
		name       string
		responses  [][]byte
		want       []byte
		wantErr    string
		wantWrites [][]byte
	}{
		{
			name:      "short response",
			responses: [][]byte{{0x90}},
			wantErr:   "short APDU response",
		},
		{
			name:      "get response",
			responses: [][]byte{{0xAA, 0x61, 0x02}, {0xBB, 0x90, 0x00}},
			want:      []byte{0xAA, 0xBB, 0x90, 0x00},
			wantWrites: [][]byte{
				{0x00},
				{0x00, 0xC0, 0x00, 0x00, 0x02},
			},
		},
		{
			name:      "non OK status",
			responses: [][]byte{{0x6A, 0x82}},
			wantErr:   "APDU status 6A82",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transmitter := &scriptedTransmitter{responses: tt.responses}
			got, err := transmitUSIM(transmitter.transmit, []byte{0x00})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("transmitUSIM error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("transmitUSIM error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("transmitUSIM failed: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("transmitUSIM = % X, want % X", got, tt.want)
			}
			if len(transmitter.writes) != len(tt.wantWrites) {
				t.Fatalf("writes = %d, want %d", len(transmitter.writes), len(tt.wantWrites))
			}
			for i, want := range tt.wantWrites {
				if !bytes.Equal(transmitter.writes[i], want) {
					t.Fatalf("write %d = % X, want % X", i, transmitter.writes[i], want)
				}
			}
		})
	}
}

func TestStatusWords(t *testing.T) {
	tests := []struct {
		name     string
		resp     []byte
		wantOK   bool
		wantMore bool
	}{
		{name: "ok", resp: []byte{0xDE, 0xAD, 0x90, 0x00}, wantOK: true},
		{name: "short", resp: []byte{0x90}},
		{name: "more", resp: []byte{0x61, 0x10}, wantMore: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusOK(tt.resp); got != tt.wantOK {
				t.Fatalf("statusOK() = %t, want %t", got, tt.wantOK)
			}
			if got := statusHasMore(tt.resp); got != tt.wantMore {
				t.Fatalf("statusHasMore() = %t, want %t", got, tt.wantMore)
			}
		})
	}
}
