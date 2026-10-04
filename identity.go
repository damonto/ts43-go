package ts43

import "strings"

type Identity struct {
	ICCID        string
	IMSI         string
	MCC          string
	MNC          string
	GID1         string
	GID2         string
	SPN          string
	PreferredAPN string
}

func mccMNC(i Identity) string {
	return i.MCC + i.MNC
}

type carrierMatch struct {
	MCCMNC             string
	PreferredAPN       *string
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

// googleScore follows Android CarrierResolver's carrier-ID rules. WFC carrier
// settings and Samsung nwInfo retain their own exact-match semantics.
func (m carrierMatch) googleScore(identity Identity) (int, bool) {
	if m.MCCMNC != mccMNC(identity) {
		return 0, false
	}
	score := 0
	for _, field := range []struct {
		want   *string
		got    string
		weight int
		prefix bool
	}{
		{m.GID1, identity.GID1, 32, true},
		{m.GID2, identity.GID2, 16, true},
		{m.SPN, identity.SPN, 2, false},
		{m.PreferredAPN, identity.PreferredAPN, 1, false},
	} {
		if field.want == nil {
			continue
		}
		got := field.got
		if field.prefix {
			if len(got) < len(*field.want) {
				return 0, false
			}
			got = got[:len(*field.want)]
		}
		if !strings.EqualFold(*field.want, got) {
			return 0, false
		}
		score += field.weight
	}
	if m.IMSIPrefixXPattern != nil {
		if !matchIMSIPrefixXPattern(*m.IMSIPrefixXPattern, identity.IMSI) {
			return 0, false
		}
		score += 128
	}
	return score, true
}
