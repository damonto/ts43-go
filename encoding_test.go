package ts43

import (
	"bytes"
	"encoding"
	"testing"
)

type binaryEncoder interface {
	encoding.BinaryMarshaler
	encoding.BinaryAppender
}

type binaryCodec interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}

type textEncoder interface {
	encoding.TextMarshaler
	encoding.TextAppender
}

type textCodec interface {
	encoding.TextMarshaler
	encoding.TextUnmarshaler
}

var (
	_ encoding.BinaryMarshaler   = AKARequest{}
	_ encoding.BinaryAppender    = AKARequest{}
	_ encoding.BinaryUnmarshaler = (*AKARequest)(nil)
	_ encoding.BinaryMarshaler   = AKAChallenge{}
	_ encoding.BinaryAppender    = AKAChallenge{}
	_ encoding.BinaryUnmarshaler = (*AKAChallenge)(nil)
	_ encoding.BinaryMarshaler   = AKAResponse{}
	_ encoding.BinaryAppender    = AKAResponse{}
	_ encoding.BinaryUnmarshaler = (*AKAResponse)(nil)
	_ encoding.BinaryUnmarshaler = (*ODSAResponse)(nil)
	_ encoding.BinaryUnmarshaler = (*WFCStatus)(nil)

	_ encoding.TextMarshaler   = Operation("")
	_ encoding.TextAppender    = Operation("")
	_ encoding.TextUnmarshaler = (*Operation)(nil)
	_ encoding.TextMarshaler   = OperationType(0)
	_ encoding.TextAppender    = OperationType(0)
	_ encoding.TextUnmarshaler = (*OperationType)(nil)
	_ encoding.TextMarshaler   = TransferType(0)
	_ encoding.TextAppender    = TransferType(0)
	_ encoding.TextUnmarshaler = (*TransferType)(nil)
	_ encoding.TextMarshaler   = OperationResult(0)
	_ encoding.TextAppender    = OperationResult(0)
	_ encoding.TextUnmarshaler = (*OperationResult)(nil)
	_ encoding.TextMarshaler   = AppEligibility(0)
	_ encoding.TextAppender    = AppEligibility(0)
	_ encoding.TextUnmarshaler = (*AppEligibility)(nil)
	_ encoding.TextMarshaler   = SubscriptionResult(0)
	_ encoding.TextAppender    = SubscriptionResult(0)
	_ encoding.TextUnmarshaler = (*SubscriptionResult)(nil)
	_ encoding.TextMarshaler   = ServiceStatus(0)
	_ encoding.TextAppender    = ServiceStatus(0)
	_ encoding.TextUnmarshaler = (*ServiceStatus)(nil)
	_ encoding.TextMarshaler   = MessageButton(0)
	_ encoding.TextAppender    = MessageButton(0)
	_ encoding.TextUnmarshaler = (*MessageButton)(nil)
	_ encoding.TextMarshaler   = WFCEntitlementStatus(0)
	_ encoding.TextAppender    = WFCEntitlementStatus(0)
	_ encoding.TextUnmarshaler = (*WFCEntitlementStatus)(nil)
	_ encoding.TextMarshaler   = WFCTCStatus(0)
	_ encoding.TextAppender    = WFCTCStatus(0)
	_ encoding.TextUnmarshaler = (*WFCTCStatus)(nil)
	_ encoding.TextMarshaler   = WFCAddressStatus(0)
	_ encoding.TextAppender    = WFCAddressStatus(0)
	_ encoding.TextUnmarshaler = (*WFCAddressStatus)(nil)
	_ encoding.TextMarshaler   = WFCProvisionStatus(0)
	_ encoding.TextAppender    = WFCProvisionStatus(0)
	_ encoding.TextUnmarshaler = (*WFCProvisionStatus)(nil)
	_ encoding.TextMarshaler   = AKAChallenge{}
	_ encoding.TextAppender    = AKAChallenge{}
	_ encoding.TextUnmarshaler = (*AKAChallenge)(nil)
	_ encoding.TextMarshaler   = optionalInt{}
	_ encoding.TextAppender    = optionalInt{}
	_ encoding.TextUnmarshaler = (*optionalInt)(nil)
	_ encoding.TextUnmarshaler = (*WebsheetResult)(nil)
)

func TestBinaryAppenderMatchesMarshalBinary(t *testing.T) {
	bytes16 := []byte("0123456789abcdef")
	tests := []struct {
		name  string
		value binaryEncoder
	}{
		{name: "AKA request", value: AKARequest{Rand: bytes16, Autn: bytes16}},
		{name: "AKA challenge", value: AKAChallenge{Identifier: 7, Rand: bytes16, Autn: bytes16}},
		{name: "AKA response", value: AKAResponse{Res: []byte("response"), CK: bytes16, IK: bytes16}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := tt.value.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}
			prefix := []byte("prefix:")
			got, err := tt.value.AppendBinary(bytes.Clone(prefix))
			if err != nil {
				t.Fatalf("AppendBinary() error = %v", err)
			}
			if want := append(prefix, want...); !bytes.Equal(got, want) {
				t.Errorf("AppendBinary() = %x, want %x", got, want)
			}
		})
	}
}

func TestBinaryCodecRoundTrip(t *testing.T) {
	bytes16 := []byte("0123456789abcdef")
	tests := []struct {
		name     string
		value    binaryEncoder
		newValue func() binaryCodec
	}{
		{
			name:  "AKA request",
			value: AKARequest{Rand: bytes16, Autn: bytes16},
			newValue: func() binaryCodec {
				return new(AKARequest)
			},
		},
		{
			name:  "AKA challenge",
			value: AKAChallenge{Identifier: 7, Rand: bytes16, Autn: bytes16},
			newValue: func() binaryCodec {
				return new(AKAChallenge)
			},
		},
		{
			name:  "AKA response",
			value: AKAResponse{Res: []byte("response"), CK: bytes16, IK: bytes16},
			newValue: func() binaryCodec {
				return new(AKAResponse)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := tt.value.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}
			gotValue := tt.newValue()
			if err := gotValue.UnmarshalBinary(want); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}
			got, err := gotValue.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() after round trip error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("binary round trip = %x, want %x", got, want)
			}
		})
	}
}

func TestAKAResponseMarshalBinaryRejectsOversizedFields(t *testing.T) {
	bytes16 := []byte("0123456789abcdef")
	oversized := make([]byte, maxAKAValueLength+1)
	tests := []struct {
		name  string
		value AKAResponse
	}{
		{name: "RES", value: AKAResponse{Res: oversized, CK: bytes16, IK: bytes16}},
		{name: "CK", value: AKAResponse{Res: []byte("response"), CK: oversized, IK: bytes16}},
		{name: "IK", value: AKAResponse{Res: []byte("response"), CK: bytes16, IK: oversized}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := []byte("prefix:")
			got, err := tt.value.AppendBinary(bytes.Clone(prefix))
			if err == nil {
				t.Fatal("AppendBinary() error = nil, want oversized field error")
			}
			if !bytes.Equal(got, prefix) {
				t.Errorf("AppendBinary() buffer = %q, want unchanged prefix %q", got, prefix)
			}
		})
	}
}

func TestTextAppenderMatchesMarshalText(t *testing.T) {
	bytes16 := []byte("0123456789abcdef")
	tests := []struct {
		name  string
		value textEncoder
	}{
		{name: "operation", value: OperationManage},
		{name: "operation type", value: OperationTypeTransferSubscription},
		{name: "transfer type", value: TransferTypeTemporaryToken},
		{name: "operation result", value: OperationResultSuccess},
		{name: "app eligibility", value: AppEligibilityEnabled},
		{name: "subscription result", value: SubscriptionResultDone},
		{name: "service status", value: ServiceStatusActivated},
		{name: "message button", value: MessageButtonAccepted},
		{name: "WFC entitlement status", value: WFCEntitlementEnabled},
		{name: "WFC terms status", value: WFCTCAvailable},
		{name: "WFC address status", value: WFCAddressAvailable},
		{name: "WFC provision status", value: WFCProvisionProvisioned},
		{name: "AKA challenge", value: AKAChallenge{Identifier: 7, Rand: bytes16, Autn: bytes16}},
		{name: "optional integer unset", value: optionalInt{}},
		{name: "optional integer zero", value: optionalInt{set: true}},
		{name: "optional integer positive", value: optionalInt{value: 42, set: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := tt.value.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText() error = %v", err)
			}
			prefix := []byte("prefix:")
			got, err := tt.value.AppendText(bytes.Clone(prefix))
			if err != nil {
				t.Fatalf("AppendText() error = %v", err)
			}
			if want := append(prefix, want...); !bytes.Equal(got, want) {
				t.Errorf("AppendText() = %q, want %q", got, want)
			}
		})
	}
}

func TestTextCodecRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		value func() textCodec
	}{
		{name: "empty operation", text: "", value: func() textCodec { return new(Operation) }},
		{name: "operation", text: "ManageSubscription", value: func() textCodec { return new(Operation) }},
		{name: "omitted operation type", text: "-1", value: func() textCodec { return new(OperationType) }},
		{name: "operation type", text: "3", value: func() textCodec { return new(OperationType) }},
		{name: "disabled transfer type", text: "", value: func() textCodec { return new(TransferType) }},
		{name: "transfer type", text: "9", value: func() textCodec { return new(TransferType) }},
		{name: "unknown operation result", text: "", value: func() textCodec { return new(OperationResult) }},
		{name: "operation result", text: "1", value: func() textCodec { return new(OperationResult) }},
		{name: "unknown app eligibility", text: "", value: func() textCodec { return new(AppEligibility) }},
		{name: "app eligibility", text: "1", value: func() textCodec { return new(AppEligibility) }},
		{name: "unknown subscription result", text: "", value: func() textCodec { return new(SubscriptionResult) }},
		{name: "subscription result", text: "3", value: func() textCodec { return new(SubscriptionResult) }},
		{name: "unknown service status", text: "", value: func() textCodec { return new(ServiceStatus) }},
		{name: "service status", text: "1", value: func() textCodec { return new(ServiceStatus) }},
		{name: "unknown message button", text: "", value: func() textCodec { return new(MessageButton) }},
		{name: "message button", text: "0", value: func() textCodec { return new(MessageButton) }},
		{name: "WFC entitlement status", text: "1", value: func() textCodec { return new(WFCEntitlementStatus) }},
		{name: "WFC terms status", text: "2", value: func() textCodec { return new(WFCTCStatus) }},
		{name: "WFC address status", text: "3", value: func() textCodec { return new(WFCAddressStatus) }},
		{name: "WFC provision status", text: "1", value: func() textCodec { return new(WFCProvisionStatus) }},
		{name: "optional integer unset", text: "", value: func() textCodec { return new(optionalInt) }},
		{name: "optional integer zero", text: "0", value: func() textCodec { return new(optionalInt) }},
		{name: "optional integer positive", text: "42", value: func() textCodec { return new(optionalInt) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := tt.value()
			if err := value.UnmarshalText([]byte(tt.text)); err != nil {
				t.Fatalf("UnmarshalText(%q) error = %v", tt.text, err)
			}
			got, err := value.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText() error = %v", err)
			}
			if string(got) != tt.text {
				t.Errorf("MarshalText() = %q, want %q", got, tt.text)
			}
		})
	}
}

func TestTextUnmarshalRejectsInvalidIntegers(t *testing.T) {
	tests := []struct {
		name  string
		value encoding.TextUnmarshaler
	}{
		{name: "operation type", value: new(OperationType)},
		{name: "transfer type", value: new(TransferType)},
		{name: "message button", value: new(MessageButton)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.value.UnmarshalText([]byte("invalid")); err == nil {
				t.Error("UnmarshalText() error = nil, want error")
			}
		})
	}
}

func TestOperationTextRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "marshal",
			run: func() error {
				_, err := Operation(invalid).MarshalText()
				return err
			},
		},
		{
			name: "unmarshal",
			run: func() error {
				var operation Operation
				return operation.UnmarshalText([]byte(invalid))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Error("text conversion error = nil, want invalid UTF-8 error")
			}
		})
	}
}
