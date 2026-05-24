package sim

var USIMAID = []byte{0xa0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}

type SelectID struct {
	ID uint16
}

func (c SelectID) APDU() []byte {
	return []byte{0x00, 0xa4, 0x00, 0x04, 0x02, byte(c.ID >> 8), byte(c.ID)}
}

type SelectName struct {
	Name []byte
}

func (c SelectName) APDU() []byte {
	out := make([]byte, 0, 5+len(c.Name))
	out = append(out, 0x00, 0xa4, 0x04, 0x04, byte(len(c.Name)))
	out = append(out, c.Name...)
	return out
}

type ReadRecord struct {
	Record byte
	Length byte
}

func (c ReadRecord) APDU() []byte {
	return []byte{0x00, 0xb2, c.Record, 0x04, c.Length}
}

type ReadBinary struct {
	Length byte
}

func (c ReadBinary) APDU() []byte {
	return []byte{0x00, 0xb0, 0x00, 0x00, c.Length}
}

type Authenticate3G struct {
	Request *AKARequest
}

func (c Authenticate3G) APDU() ([]byte, error) {
	data, err := c.Request.MarshalBinary()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 5+len(data))
	out = append(out, 0x00, 0x88, 0x00, 0x81, byte(len(data)))
	out = append(out, data...)
	return out, nil
}
