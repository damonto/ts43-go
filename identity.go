package ts43

import "strings"

type Identity struct {
	ICCID string
	IMSI  string
	MCC   string
	MNC   string
	GID1  string
	GID2  string
	SPN   string
}

func mccMNC(i Identity) string {
	return i.MCC + i.MNC
}

type carrierMatch struct {
	MCCMNC             string
	GID1               *string
	GID2               *string
	SPN                *string
	IMSIPrefixXPattern *string
}

func (m carrierMatch) score(identity Identity) (int, bool) {
	if m.MCCMNC != mccMNC(identity) {
		return 0, false
	}

	score := 0
	if m.GID1 != nil {
		if !strings.EqualFold(*m.GID1, identity.GID1) {
			return 0, false
		}
		score++
	}
	if m.GID2 != nil {
		if !strings.EqualFold(*m.GID2, identity.GID2) {
			return 0, false
		}
		score++
	}
	if m.SPN != nil {
		if *m.SPN != identity.SPN {
			return 0, false
		}
		score++
	}
	if m.IMSIPrefixXPattern != nil {
		if !matchIMSIPrefixXPattern(*m.IMSIPrefixXPattern, identity.IMSI) {
			return 0, false
		}
		score++
	}
	return score, true
}

func matchIMSIPrefixXPattern(pattern, imsi string) bool {
	if pattern == "" {
		return imsi == ""
	}
	if len(pattern) > len(imsi) {
		return false
	}
	for i := range pattern {
		if pattern[i] == 'x' || pattern[i] == 'X' {
			continue
		}
		if pattern[i] != imsi[i] {
			return false
		}
	}
	return true
}
