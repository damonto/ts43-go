package sim

import (
	"errors"
	"fmt"
)

type IMSI string

func (i *IMSI) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		return errors.New("reading EF_IMSI: empty payload")
	}
	n := int(data[0])
	if len(data) < n+1 {
		return errors.New("reading EF_IMSI: truncated payload")
	}
	digits := DecodeSwappedBCD(data[1:1+n], true)
	if len(digits) < 6 {
		return errors.New("reading EF_IMSI: decoded payload is too short")
	}
	*i = IMSI(digits)
	return nil
}

func (i IMSI) String() string {
	return string(i)
}

func (i IMSI) MCC() (string, error) {
	if len(i) < 3 {
		return "", errors.New("IMSI is too short for MCC")
	}
	return string(i[:3]), nil
}

func DecodeMNCLength(data []byte) (int, error) {
	if len(data) < 4 {
		return 0, errors.New("parsing EF_AD: truncated payload")
	}
	switch data[3] & 0x0f {
	case 0x02, 0x03:
		return int(data[3] & 0x0f), nil
	case 0x00:
		return 0, errors.New("parsing EF_AD: MNC length is unavailable")
	default:
		return 0, errors.New("parsing EF_AD: invalid MNC length")
	}
}

func FormatMNC(imsi string, n int) (string, error) {
	if len(imsi) < 3+n {
		return "", errors.New("IMSI is too short for MNC length")
	}
	switch n {
	case 2:
		return imsi[3:5], nil
	case 3:
		return imsi[3:6], nil
	default:
		return "", fmt.Errorf("unsupported MNC length %d", n)
	}
}
