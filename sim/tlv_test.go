package sim

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	"github.com/damonto/ts43-go/apdu"
)

func TestTLVReadFromWriteTo(t *testing.T) {
	data := []byte{
		0x62, 0x0b,
		0x82, 0x05, 0x42, 0x00, 0x00, 0x00, 0x02,
		0x80, 0x02, 0x00, 0x22,
	}
	var item tlv
	n, err := item.ReadFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFrom error = %v", err)
	}
	if n != int64(len(data)) {
		t.Fatalf("ReadFrom bytes = %d, want %d", n, len(data))
	}
	if !item.Tag.equal(tagFCPTemplate) {
		t.Fatalf("tag = %x, want %x", item.Tag, tagFCPTemplate)
	}
	if len(item.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(item.Children))
	}

	var out bytes.Buffer
	n, err = item.WriteTo(&out)
	if err != nil {
		t.Fatalf("WriteTo error = %v", err)
	}
	if n != int64(len(data)) {
		t.Fatalf("WriteTo bytes = %d, want %d", n, len(data))
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("WriteTo = %x, want %x", out.Bytes(), data)
	}
}

func TestDiscoverUSIMAID(t *testing.T) {
	fcp := []byte{
		0x62, 0x0b,
		0x82, 0x05, 0x42, 0x00, 0x00, 0x11, 0x02,
		0x80, 0x02, 0x00, 0x22,
	}
	usimRecord := []byte{
		0x61, 0x0f,
		0x4f, 0x07, 0xa0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02,
		0x50, 0x04, 'U', 'S', 'I', 'M',
	}
	emptyRecord := bytes.Repeat([]byte{0xff}, len(usimRecord))
	responses := map[string]apdu.Response{
		string((SelectID{ID: masterFileID}).APDU()):                           response(nil),
		string((SelectID{ID: efDirID}).APDU()):                                response(fcp),
		string((ReadRecord{Record: 1, Length: byte(len(usimRecord))}).APDU()): response(emptyRecord),
		string((ReadRecord{Record: 2, Length: byte(len(usimRecord))}).APDU()): response(usimRecord),
	}

	got, err := DiscoverUSIMAID(func(cmd []byte) (apdu.Response, error) {
		resp, ok := responses[string(cmd)]
		if !ok {
			return nil, fmt.Errorf("unexpected APDU %x", cmd)
		}
		return resp, nil
	})
	if err != nil {
		t.Fatalf("DiscoverUSIMAID error = %v", err)
	}
	if !slices.Equal(got, USIMAID) {
		t.Fatalf("DiscoverUSIMAID = %x, want %x", got, USIMAID)
	}
}

func response(data []byte) apdu.Response {
	out := append([]byte(nil), data...)
	return append(out, 0x90, 0x00)
}
