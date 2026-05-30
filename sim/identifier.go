package sim

import (
	"encoding/hex"
	"errors"
	"strings"
)

type ICCID []byte

func NewICCID(value string) (ICCID, error) {
	var id ICCID
	if err := id.UnmarshalText([]byte(value)); err != nil {
		return nil, err
	}
	return id, nil
}

func (id ICCID) String() string {
	return DecodeBCD(id)
}

func (id ICCID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

func (id *ICCID) UnmarshalText(text []byte) error {
	next, err := EncodeBCD[ICCID](string(text))
	if err != nil {
		return err
	}
	*id = next
	return nil
}

type IMEI []byte

func NewIMEI(value string) (IMEI, error) {
	var id IMEI
	if err := id.UnmarshalText([]byte(value)); err != nil {
		return nil, err
	}
	return id, nil
}

func (id IMEI) String() string {
	return DecodeBCD(id)
}

func (id IMEI) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

func (id *IMEI) UnmarshalText(text []byte) error {
	next, err := EncodeBCD[IMEI](string(text))
	if err != nil {
		return err
	}
	*id = next
	return nil
}

// EncodeBCD encodes a decimal string as swapped GSM BCD.
func EncodeBCD[T ~[]byte](value string) (T, error) {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'A' || r > 'F') && (r < 'a' || r > 'f') {
			return nil, errors.New("invalid value")
		}
	}
	if len(value)%2 != 0 {
		value += "F"
	}
	id, _ := hex.DecodeString(value)
	for index := range id {
		id[index] = (id[index]>>4)&0x0F | (id[index]<<4)&0xF0
	}
	return T(id), nil
}

// DecodeBCD decodes swapped GSM BCD bytes to a decimal string.
func DecodeBCD(value []byte) string {
	id := make([]byte, len(value))
	for index := range value {
		id[index] = (value[index]>>4)&0x0F | (value[index]<<4)&0xF0
	}
	out := hex.EncodeToString(id)
	if index := strings.LastIndexByte(out, 'f'); index != -1 {
		out = out[:index]
	}
	return out
}

func DecodeSwappedBCD(data []byte, stripLeadingNine bool) string {
	out := DecodeBCD(data)
	if stripLeadingNine {
		out = strings.TrimPrefix(out, "9")
	}
	return out
}

func GroupIdentifierString(data []byte) string {
	data = trimRightByte(data, 0xff)
	if len(data) == 0 {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(data))
}

func trimRightByte(data []byte, value byte) []byte {
	for len(data) > 0 && data[len(data)-1] == value {
		data = data[:len(data)-1]
	}
	return data
}
