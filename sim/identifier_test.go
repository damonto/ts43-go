package sim

import "testing"

func TestICCIDText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "even length", text: "890000000000000000"},
		{name: "odd length", text: "8900000000000000001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id ICCID
			if err := id.UnmarshalText([]byte(tt.text)); err != nil {
				t.Fatalf("UnmarshalText error = %v", err)
			}
			got, err := id.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText error = %v", err)
			}
			if string(got) != tt.text {
				t.Fatalf("MarshalText = %q, want %q", got, tt.text)
			}
		})
	}
}

func TestIMEIText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "even length", text: "12345678901234"},
		{name: "odd length", text: "123456789012345"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id IMEI
			if err := id.UnmarshalText([]byte(tt.text)); err != nil {
				t.Fatalf("UnmarshalText error = %v", err)
			}
			got, err := id.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText error = %v", err)
			}
			if string(got) != tt.text {
				t.Fatalf("MarshalText = %q, want %q", got, tt.text)
			}
		})
	}
}
