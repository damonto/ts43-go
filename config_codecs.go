package ts43

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
)

// EntitlementConfigs holds application-supplied transfer discovery records.
// Decode before sharing a value with clients. Both encodings use UTF-8 JSON.
type EntitlementConfigs struct{ records []entitlementRecord }

// WFCConfigs holds application-supplied Wi-Fi Calling discovery records.
// Decode before sharing a value with clients. Both encodings use UTF-8 JSON.
type WFCConfigs struct{ records []wfcRecord }

var (
	_ json.Marshaler             = EntitlementConfigs{}
	_ json.Unmarshaler           = (*EntitlementConfigs)(nil)
	_ encoding.BinaryMarshaler   = EntitlementConfigs{}
	_ encoding.BinaryUnmarshaler = (*EntitlementConfigs)(nil)
	_ json.Marshaler             = WFCConfigs{}
	_ json.Unmarshaler           = (*WFCConfigs)(nil)
	_ encoding.BinaryMarshaler   = WFCConfigs{}
	_ encoding.BinaryUnmarshaler = (*WFCConfigs)(nil)
)

// MarshalJSON encodes the entitlement records as an array, including for zero values.
func (c EntitlementConfigs) MarshalJSON() ([]byte, error) {
	if len(c.records) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(c.records)
}

// UnmarshalJSON replaces the entitlement records only after successful decoding.
func (c *EntitlementConfigs) UnmarshalJSON(data []byte) error {
	var records []entitlementRecord
	if err := decodeConfigArray(data, &records); err != nil {
		return fmt.Errorf("decode entitlement configs: %w", err)
	}
	c.records = records
	return nil
}

// MarshalBinary encodes entitlement records using the same JSON representation.
func (c EntitlementConfigs) MarshalBinary() ([]byte, error) { return c.MarshalJSON() }

// UnmarshalBinary decodes JSON entitlement records.
func (c *EntitlementConfigs) UnmarshalBinary(data []byte) error { return c.UnmarshalJSON(data) }

// MarshalJSON encodes WFC records as an array, including for zero values.
func (c WFCConfigs) MarshalJSON() ([]byte, error) {
	if len(c.records) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(c.records)
}

// UnmarshalJSON replaces WFC records only after successful decoding.
func (c *WFCConfigs) UnmarshalJSON(data []byte) error {
	var records []wfcRecord
	if err := decodeConfigArray(data, &records); err != nil {
		return fmt.Errorf("decode WFC configs: %w", err)
	}
	c.records = records
	return nil
}

// MarshalBinary encodes WFC records using the same JSON representation.
func (c WFCConfigs) MarshalBinary() ([]byte, error) { return c.MarshalJSON() }

// UnmarshalBinary decodes JSON WFC records.
func (c *WFCConfigs) UnmarshalBinary(data []byte) error { return c.UnmarshalJSON(data) }

func decodeConfigArray(data []byte, target any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		return errors.New("expected config array")
	}
	return json.Unmarshal(data, target)
}
