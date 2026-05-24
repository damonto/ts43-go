package sim

import "errors"

type Identity struct {
	ICCID string
	IMSI  string
	MCC   string
	MNC   string
	GID1  string
}

func (i Identity) MCCMNC() string {
	return i.MCC + i.MNC
}

func NewIdentity(identity Identity, administrativeData []byte) (*Identity, error) {
	if identity.IMSI == "" {
		return nil, errors.New("IMSI is required")
	}
	mcc, err := IMSI(identity.IMSI).MCC()
	if err != nil {
		return nil, err
	}
	mncLen, err := DecodeMNCLength(administrativeData)
	if err != nil {
		return nil, err
	}
	mnc, err := FormatMNC(identity.IMSI, mncLen)
	if err != nil {
		return nil, err
	}
	identity.MCC = mcc
	identity.MNC = mnc
	return &identity, nil
}
