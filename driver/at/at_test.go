package at

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

type scriptedSerial struct {
	readBuf strings.Reader
	writes  []string
}

func newScriptedSerial(script string) *scriptedSerial {
	return &scriptedSerial{readBuf: *strings.NewReader(script)}
}

func (s *scriptedSerial) Read(p []byte) (int, error) {
	return s.readBuf.Read(p)
}

func (s *scriptedSerial) Write(p []byte) (int, error) {
	s.writes = append(s.writes, string(p))
	return len(p), nil
}

func (s *scriptedSerial) Close() error { return nil }

type partialWriteSerial struct {
	readBuf strings.Reader
	writes  bytes.Buffer
	max     int
}

func newPartialWriteSerial(script string, max int) *partialWriteSerial {
	return &partialWriteSerial{readBuf: *strings.NewReader(script), max: max}
}

func (s *partialWriteSerial) Read(p []byte) (int, error) {
	return s.readBuf.Read(p)
}

func (s *partialWriteSerial) Write(p []byte) (int, error) {
	if len(p) > s.max {
		p = p[:s.max]
	}
	s.writes.Write(p)
	return len(p), nil
}

func (s *partialWriteSerial) Close() error { return nil }

func TestSendCommandKeepsBufferedDataAcrossCalls(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "two CSIM responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serial := newScriptedSerial("\r\nAT+CSIM=?\r\n\r\nOK\r\nAT+CSIM=4,\"00\"\r\n+CSIM: 4,\"9000\"\r\nOK\r\n")
			at := &AT{
				s:      serial,
				reader: bufio.NewReader(serial),
			}

			got, err := at.sendCommand("AT+CSIM=?")
			if err != nil {
				t.Fatalf("first sendCommand failed: %v", err)
			}
			if got != "" {
				t.Fatalf("first sendCommand = %q, want empty response", got)
			}

			got, err = at.sendCommand(`AT+CSIM=4,"00"`)
			if err != nil {
				t.Fatalf("second sendCommand failed: %v", err)
			}
			if got != `+CSIM: 4,"9000"` {
				t.Fatalf("second sendCommand = %q, want %q", got, `+CSIM: 4,"9000"`)
			}

			if len(serial.writes) != 2 {
				t.Fatalf("writes = %d, want 2", len(serial.writes))
			}
			if serial.writes[0] != "AT+CSIM=?\r\n" {
				t.Fatalf("first write = %q", serial.writes[0])
			}
			if serial.writes[1] != "AT+CSIM=4,\"00\"\r\n" {
				t.Fatalf("second write = %q", serial.writes[1])
			}
		})
	}
}

func TestSendCommandWritesCompleteCommand(t *testing.T) {
	tests := []struct {
		name string
		max  int
	}{
		{name: "partial writes", max: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serial := newPartialWriteSerial("AT+TEST\r\nOK\r\n", tt.max)
			at := &AT{
				s:      serial,
				reader: bufio.NewReader(serial),
			}

			if _, err := at.sendCommand("AT+TEST"); err != nil {
				t.Fatalf("sendCommand failed: %v", err)
			}
			if got := serial.writes.String(); got != "AT+TEST\r\n" {
				t.Fatalf("write = %q, want %q", got, "AT+TEST\r\n")
			}
		})
	}
}

func TestSendCommandMatchesCompleteResultCodes(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "info line contains OK substring"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serial := newScriptedSerial("AT+TEST\r\n+INFO: LOOKUP\r\nOK\r\n")
			at := &AT{
				s:      serial,
				reader: bufio.NewReader(serial),
			}

			got, err := at.sendCommand("AT+TEST")
			if err != nil {
				t.Fatalf("sendCommand failed: %v", err)
			}
			if got != "+INFO: LOOKUP" {
				t.Fatalf("sendCommand = %q, want %q", got, "+INFO: LOOKUP")
			}
		})
	}
}

func TestSendCommandReturnsStructuredATErrors(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "CME", line: "+CME ERROR: 42", want: "+CME ERROR: 42"},
		{name: "CMS", line: "+CMS ERROR: 500", want: "+CMS ERROR: 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serial := newScriptedSerial("AT+TEST\r\n" + tt.line + "\r\n")
			at := &AT{
				s:      serial,
				reader: bufio.NewReader(serial),
			}

			_, err := at.sendCommand("AT+TEST")
			if err == nil {
				t.Fatal("sendCommand error = nil, want error")
			}
			if err.Error() != tt.want {
				t.Fatalf("sendCommand error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestTransmitAPDUReturnsNonOKStatusWord(t *testing.T) {
	tests := []struct {
		name string
		want []byte
	}{
		{name: "file not found", want: []byte{0x6A, 0x82}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serial := newScriptedSerial("AT+CSIM=2,\"00\"\r\n+CSIM: 4,\"6A82\"\r\nOK\r\n")
			at := &AT{
				s:      serial,
				reader: bufio.NewReader(serial),
			}

			got, err := at.transmitAPDU([]byte{0x00})
			if err != nil {
				t.Fatalf("transmitAPDU failed: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("transmitAPDU = % X, want % X", got, tt.want)
			}
		})
	}
}

var _ io.ReadWriteCloser = (*scriptedSerial)(nil)
var _ io.ReadWriteCloser = (*partialWriteSerial)(nil)
