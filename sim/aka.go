package sim

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	eapCodeRequest  = 0x01
	eapCodeResponse = 0x02
	eapTypeAKA      = 0x17

	akaSubtypeChallenge   = 0x01
	akaSubtypeSyncFailure = 0x04

	attributeRand = 0x01
	attributeAutn = 0x02
	attributeRes  = 0x03
	attributeAuts = 0x04
	attributeMAC  = 0x0b

	securityContextSuccess     = 0xdb
	securityContextSyncFailure = 0xdc
)

type AKARequest struct {
	Rand []byte
	Autn []byte
}

func (r AKARequest) MarshalBinary() ([]byte, error) {
	if len(r.Rand) != 16 {
		return nil, fmt.Errorf("RAND length %d, want 16", len(r.Rand))
	}
	if len(r.Autn) != 16 {
		return nil, fmt.Errorf("AUTN length %d, want 16", len(r.Autn))
	}
	out := make([]byte, 0, 34)
	out = append(out, byte(len(r.Rand)))
	out = append(out, r.Rand...)
	out = append(out, byte(len(r.Autn)))
	out = append(out, r.Autn...)
	return out, nil
}

func (r *AKARequest) UnmarshalBinary(data []byte) error {
	rand, next, err := valueLength(data, 0, "RAND")
	if err != nil {
		return err
	}
	autn, next, err := valueLength(data, next, "AUTN")
	if err != nil {
		return err
	}
	if next != len(data) {
		return fmt.Errorf("AKA request has %d trailing bytes", len(data)-next)
	}
	if len(rand) != 16 {
		return fmt.Errorf("RAND length %d, want 16", len(rand))
	}
	if len(autn) != 16 {
		return fmt.Errorf("AUTN length %d, want 16", len(autn))
	}
	r.Rand = rand
	r.Autn = autn
	return nil
}

type AKAResponse struct {
	Res  []byte
	CK   []byte
	IK   []byte
	Auts []byte
}

type AKAChallenge struct {
	Identifier byte
	Rand       []byte
	Autn       []byte
}

func IMSIEAPIdentity(mccmnc, imsi, realm string) (string, error) {
	if len(mccmnc) < 5 {
		return "", fmt.Errorf("MCCMNC length %d, want at least 5", len(mccmnc))
	}
	if imsi == "" {
		return "", errors.New("IMSI is required")
	}
	if realm == "" {
		return "", errors.New("realm is required")
	}
	mcc := mccmnc[:3]
	mnc := mccmnc[3:]
	if len(mnc) == 2 {
		mnc = "0" + mnc
	}
	return "0" + imsi + "@" + realm + ".mnc" + mnc + ".mcc" + mcc + ".3gppnetwork.org", nil
}

func (c *AKAChallenge) UnmarshalText(text []byte) error {
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("decode EAP-AKA challenge: %w", err)
	}
	return c.UnmarshalBinary(data)
}

func (c AKAChallenge) MarshalText() ([]byte, error) {
	data, err := c.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString(data)), nil
}

func (c *AKAChallenge) UnmarshalBinary(data []byte) error {
	if len(data) < 8 {
		return errors.New("EAP-AKA challenge too short")
	}
	if data[0] != eapCodeRequest {
		return fmt.Errorf("unexpected EAP code %#x", data[0])
	}
	packetLen := int(binary.BigEndian.Uint16(data[2:4]))
	if packetLen != len(data) {
		return fmt.Errorf("EAP length %d does not match packet length %d", packetLen, len(data))
	}
	if data[4] != eapTypeAKA {
		return fmt.Errorf("unexpected EAP type %#x", data[4])
	}
	if data[5] != akaSubtypeChallenge {
		return fmt.Errorf("unexpected EAP-AKA subtype %#x", data[5])
	}
	next := AKAChallenge{Identifier: data[1]}
	for off := 8; off < len(data); {
		if len(data)-off < 2 {
			return errors.New("truncated EAP-AKA attribute")
		}
		typ := data[off]
		attrLen := int(data[off+1]) * 4
		if attrLen < 4 {
			return fmt.Errorf("invalid EAP-AKA attribute length %d", attrLen)
		}
		if attrLen > len(data)-off {
			return errors.New("EAP-AKA attribute exceeds packet length")
		}
		value := data[off+4 : off+attrLen]
		switch typ {
		case attributeRand:
			if len(value) != 16 {
				return fmt.Errorf("AT_RAND length %d, want 16", len(value))
			}
			next.Rand = append([]byte(nil), value...)
		case attributeAutn:
			if len(value) != 16 {
				return fmt.Errorf("AT_AUTN length %d, want 16", len(value))
			}
			next.Autn = append([]byte(nil), value...)
		}
		off += attrLen
	}
	if len(next.Rand) != 16 {
		return errors.New("AT_RAND is missing")
	}
	if len(next.Autn) != 16 {
		return errors.New("AT_AUTN is missing")
	}
	*c = next
	return nil
}

func (c AKAChallenge) MarshalBinary() ([]byte, error) {
	if len(c.Rand) != 16 {
		return nil, fmt.Errorf("AT_RAND length %d, want 16", len(c.Rand))
	}
	if len(c.Autn) != 16 {
		return nil, fmt.Errorf("AT_AUTN length %d, want 16", len(c.Autn))
	}
	msg := make([]byte, 8, 48)
	msg[0] = eapCodeRequest
	msg[1] = c.Identifier
	msg[4] = eapTypeAKA
	msg[5] = akaSubtypeChallenge
	msg = append(msg, attributeRand, 0x05, 0x00, 0x00)
	msg = append(msg, c.Rand...)
	msg = append(msg, attributeAutn, 0x05, 0x00, 0x00)
	msg = append(msg, c.Autn...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)))
	return msg, nil
}

func (c *AKAChallenge) AuthenticationRequest() *AKARequest {
	return &AKARequest{
		Rand: append([]byte(nil), c.Rand...),
		Autn: append([]byte(nil), c.Autn...),
	}
}

func (r *AKAResponse) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		return errors.New("security context is empty")
	}
	switch data[0] {
	case securityContextSuccess:
		res, next, err := valueLength(data, 1, "RES")
		if err != nil {
			return err
		}
		ck, next, err := valueLength(data, next, "CK")
		if err != nil {
			return err
		}
		ik, _, err := valueLength(data, next, "IK")
		if err != nil {
			return err
		}
		r.Res = res
		r.CK = ck
		r.IK = ik
		r.Auts = nil
		return nil
	case securityContextSyncFailure:
		auts, _, err := valueLength(data, 1, "AUTS")
		if err != nil {
			return err
		}
		r.Res = nil
		r.CK = nil
		r.IK = nil
		r.Auts = auts
		return nil
	default:
		return fmt.Errorf("unknown security context tag %#x", data[0])
	}
}

func (r AKAResponse) MarshalBinary() ([]byte, error) {
	if len(r.Auts) > 0 {
		if len(r.Auts) != 14 {
			return nil, fmt.Errorf("AUTS length %d, want 14", len(r.Auts))
		}
		out := []byte{securityContextSyncFailure, byte(len(r.Auts))}
		return append(out, r.Auts...), nil
	}
	if len(r.Res) == 0 {
		return nil, errors.New("RES is required")
	}
	if len(r.CK) == 0 {
		return nil, errors.New("CK is required")
	}
	if len(r.IK) == 0 {
		return nil, errors.New("IK is required")
	}
	out := []byte{securityContextSuccess, byte(len(r.Res))}
	out = append(out, r.Res...)
	out = append(out, byte(len(r.CK)))
	out = append(out, r.CK...)
	out = append(out, byte(len(r.IK)))
	out = append(out, r.IK...)
	return out, nil
}

func valueLength(data []byte, off int, field string) ([]byte, int, error) {
	if off >= len(data) {
		return nil, off, fmt.Errorf("%s length is missing", field)
	}
	n := int(data[off])
	off++
	if n > len(data)-off {
		return nil, off, fmt.Errorf("%s length %d exceeds remaining data %d", field, n, len(data)-off)
	}
	return append([]byte(nil), data[off:off+n]...), off + n, nil
}

func RespondToChallengeBase64(challenge *AKAChallenge, response *AKAResponse, identity string) (string, error) {
	data, err := RespondToChallenge(challenge, response, identity)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func RespondToChallenge(challenge *AKAChallenge, response *AKAResponse, identity string) ([]byte, error) {
	if len(response.Auts) > 0 {
		if len(response.Auts) != 14 {
			return nil, fmt.Errorf("AUTS length %d, want 14", len(response.Auts))
		}
		return syncFailureResponse(challenge.Identifier, response.Auts), nil
	}
	if len(response.Res) == 0 {
		return nil, errors.New("RES is required")
	}
	if len(response.CK) == 0 {
		return nil, errors.New("CK is required")
	}
	if len(response.IK) == 0 {
		return nil, errors.New("IK is required")
	}
	kAut, err := deriveKAut(identity, response.IK, response.CK)
	if err != nil {
		return nil, err
	}
	return challengeResponse(challenge.Identifier, response.Res, kAut), nil
}

func challengeResponse(identifier byte, res, kAut []byte) []byte {
	resLen := paddedLen(len(res))
	atResLen := 4 + resLen
	msg := make([]byte, 8+atResLen+20)
	msg[0] = eapCodeResponse
	msg[1] = identifier
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)))
	msg[4] = eapTypeAKA
	msg[5] = akaSubtypeChallenge

	off := 8
	msg[off] = attributeRes
	msg[off+1] = byte(atResLen / 4)
	binary.BigEndian.PutUint16(msg[off+2:off+4], uint16(len(res)*8))
	copy(msg[off+4:off+4+len(res)], res)
	off += atResLen

	msg[off] = attributeMAC
	msg[off+1] = 0x05
	mac := hmac.New(sha1.New, kAut)
	mac.Write(msg)
	copy(msg[off+4:off+20], mac.Sum(nil)[:16])
	return msg
}

func syncFailureResponse(identifier byte, auts []byte) []byte {
	msg := make([]byte, 10+len(auts))
	msg[0] = eapCodeResponse
	msg[1] = identifier
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)))
	msg[4] = eapTypeAKA
	msg[5] = akaSubtypeSyncFailure
	msg[8] = attributeAuts
	msg[9] = 0x04
	copy(msg[10:], auts)
	return msg
}

func deriveKAut(identity string, ik, ck []byte) ([]byte, error) {
	if identity == "" {
		return nil, errors.New("identity is required")
	}
	h := sha1.New()
	h.Write([]byte(identity))
	h.Write(ik)
	h.Write(ck)
	key := fips1862PRF(h.Sum(nil), 160)
	return key[16:32], nil
}

func paddedLen(n int) int {
	if n%4 == 0 {
		return n
	}
	return n + 4 - n%4
}

func fips1862PRF(seed []byte, length int) []byte {
	xkey := append([]byte(nil), seed...)
	out := make([]byte, 0, length)
	state := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	for len(out) < length {
		for range 2 {
			w := sha1G(state, xkey)
			out = append(out, w...)
			addMod160(xkey, w)
		}
	}
	return out[:length]
}

func addMod160(xkey, w []byte) {
	carry := 1
	for i := 19; i >= 0; i-- {
		carry += int(xkey[i]) + int(w[i])
		xkey[i] = byte(carry)
		carry >>= 8
	}
}

func sha1G(state [5]uint32, xkey []byte) []byte {
	var block [64]byte
	copy(block[:20], xkey)
	w := sha1Block(state, block)
	out := make([]byte, 20)
	for i, v := range w {
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
	return out
}

func sha1Block(h [5]uint32, block [64]byte) [5]uint32 {
	var w [80]uint32
	for i := range 16 {
		w[i] = binary.BigEndian.Uint32(block[i*4:])
	}
	for i := 16; i < 80; i++ {
		v := w[i-3] ^ w[i-8] ^ w[i-14] ^ w[i-16]
		w[i] = v<<1 | v>>31
	}
	a, b, c, d, e := h[0], h[1], h[2], h[3], h[4]
	for i := range 80 {
		var f, k uint32
		switch {
		case i < 20:
			f = (b & c) | (^b & d)
			k = 0x5a827999
		case i < 40:
			f = b ^ c ^ d
			k = 0x6ed9eba1
		case i < 60:
			f = (b & c) | (b & d) | (c & d)
			k = 0x8f1bbcdc
		default:
			f = b ^ c ^ d
			k = 0xca62c1d6
		}
		temp := (a<<5 | a>>27) + f + e + k + w[i]
		e, d, c, b, a = d, c, b<<30|b>>2, a, temp
	}
	return [5]uint32{h[0] + a, h[1] + b, h[2] + c, h[3] + d, h[4] + e}
}
