package sim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/damonto/ts43-go/apdu"
)

const (
	masterFileID = 0x3f00
	efDirID      = 0x2f00

	structureLinearFixed = 0x42
)

var errUSIMApplicationNotFound = errors.New("USIM application not found")

var (
	tagFCPTemplate     = tag{0x62}
	tagFileDescriptor  = tag{0x82}
	tagFileSize        = tag{0x80}
	tagApplication     = tag{0x61}
	tagApplicationAID  = tag{0x4f}
	tagApplicationName = tag{0x50}
)

type tag []byte

func (t tag) equal(other tag) bool {
	return bytes.Equal(t, other)
}

type tlv struct {
	Tag      tag
	Value    []byte
	Children []tlv
}

func (t *tlv) unmarshalBinary(data []byte) error {
	r := bytes.NewReader(data)
	if _, err := t.ReadFrom(r); err != nil {
		return err
	}
	if r.Len() != 0 {
		return fmt.Errorf("TLV has %d trailing bytes", r.Len())
	}
	return nil
}

func (t *tlv) ReadFrom(r io.Reader) (int64, error) {
	first, n, err := readTLVByte(r)
	if err != nil {
		return n, err
	}
	read := n
	next := tlv{Tag: tag{first}}

	if first&0x1f == 0x1f {
		for {
			b, n, err := readTLVByte(r)
			read += n
			if err != nil {
				return read, errors.New("TLV tag is truncated")
			}
			next.Tag = append(next.Tag, b)
			if b&0x80 == 0 {
				break
			}
		}
	}

	length, n, err := readTLVLength(r)
	read += n
	if err != nil {
		return read, err
	}
	next.Value = make([]byte, length)
	nn, err := io.ReadFull(r, next.Value)
	read += int64(nn)
	if err != nil {
		return read, errors.New("TLV value is truncated")
	}

	if first&0x20 != 0 {
		children := bytes.NewReader(next.Value)
		for children.Len() > 0 {
			var child tlv
			if _, err := child.ReadFrom(children); err != nil {
				return read, err
			}
			next.Children = append(next.Children, child)
		}
	}

	*t = next
	return read, nil
}

func (t tlv) WriteTo(w io.Writer) (int64, error) {
	if len(t.Tag) == 0 {
		return 0, errors.New("TLV tag is missing")
	}
	value := t.Value
	if len(t.Children) > 0 {
		var buf bytes.Buffer
		for _, child := range t.Children {
			if _, err := child.WriteTo(&buf); err != nil {
				return 0, err
			}
		}
		value = buf.Bytes()
	}

	var written int64
	n, err := w.Write(t.Tag)
	written += int64(n)
	if err != nil {
		return written, err
	}
	length := encodeTLVLength(len(value))
	n, err = w.Write(length)
	written += int64(n)
	if err != nil {
		return written, err
	}
	n, err = w.Write(value)
	written += int64(n)
	if err != nil {
		return written, err
	}
	return written, nil
}

func readTLVByte(r io.Reader) (byte, int64, error) {
	var buf [1]byte
	n, err := io.ReadFull(r, buf[:])
	return buf[0], int64(n), err
}

func readTLVLength(r io.Reader) (int, int64, error) {
	first, n, err := readTLVByte(r)
	if err != nil {
		return 0, n, errors.New("TLV length is missing")
	}
	if first&0x80 == 0 {
		return int(first), n, nil
	}
	count := int(first & 0x7f)
	if count == 0 {
		return 0, n, errors.New("indefinite TLV length is unsupported")
	}
	if count > 4 {
		return 0, n, fmt.Errorf("TLV length uses %d bytes, want at most 4", count)
	}
	buf := make([]byte, count)
	nn, err := io.ReadFull(r, buf)
	read := n + int64(nn)
	if err != nil {
		return 0, read, errors.New("TLV length is truncated")
	}
	length := 0
	for _, b := range buf {
		length = length<<8 | int(b)
	}
	return length, read, nil
}

func encodeTLVLength(length int) []byte {
	switch {
	case length < 0x80:
		return []byte{byte(length)}
	case length <= 0xff:
		return []byte{0x81, byte(length)}
	case length <= 0xffff:
		return []byte{0x82, byte(length >> 8), byte(length)}
	case length <= 0xffffff:
		return []byte{0x83, byte(length >> 16), byte(length >> 8), byte(length)}
	default:
		return []byte{0x84, byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	}
}

type Application struct {
	AID   []byte
	Label string
}

type FileInfo struct {
	FileStructure byte
	RecordSize    uint16
	RecordCount   uint16
	FileSize      uint16
}

func DiscoverUSIMAID(transmit func([]byte) (apdu.Response, error)) ([]byte, error) {
	apps, err := ListApplications(transmit)
	if err != nil {
		return nil, err
	}
	for _, app := range apps {
		if len(app.AID) == 0 {
			continue
		}
		if strings.EqualFold(app.Label, "USIM") || bytes.HasPrefix(app.AID, USIMAID) {
			return slices.Clone(app.AID), nil
		}
	}
	return nil, errUSIMApplicationNotFound
}

func ListApplications(transmit func([]byte) (apdu.Response, error)) ([]Application, error) {
	if transmit == nil {
		return nil, errors.New("transmit is required")
	}
	if _, err := transmit((SelectID{ID: masterFileID}).APDU()); err != nil {
		return nil, fmt.Errorf("select MF: %w", err)
	}
	resp, err := transmit((SelectID{ID: efDirID}).APDU())
	if err != nil {
		return nil, fmt.Errorf("select EF_DIR: %w", err)
	}
	var info FileInfo
	if err := info.UnmarshalBinary(resp.Data()); err != nil {
		return nil, fmt.Errorf("parse EF_DIR FCP: %w", err)
	}
	if info.FileStructure != structureLinearFixed {
		return nil, errors.New("EF_DIR is not linear fixed")
	}
	if info.RecordSize > 0xff {
		return nil, fmt.Errorf("EF_DIR record size %d exceeds APDU limit", info.RecordSize)
	}

	apps := make([]Application, 0, info.RecordCount)
	for record := uint16(1); record <= info.RecordCount; record++ {
		resp, err := transmit((ReadRecord{Record: byte(record), Length: byte(info.RecordSize)}).APDU())
		if err != nil {
			return nil, fmt.Errorf("read EF_DIR record %d: %w", record, err)
		}
		var app Application
		if err := app.UnmarshalBinary(resp.Data()); err != nil {
			return nil, fmt.Errorf("parse EF_DIR record %d: %w", record, err)
		}
		if len(app.AID) == 0 {
			continue
		}
		apps = append(apps, app)
	}
	return apps, nil
}

func (info *FileInfo) UnmarshalBinary(data []byte) error {
	var fcp tlv
	if err := fcp.unmarshalBinary(data); err != nil {
		return err
	}
	if !fcp.Tag.equal(tagFCPTemplate) {
		return errors.New("missing FCP template")
	}

	parsed := FileInfo{}
	for _, item := range fcp.Children {
		switch {
		case item.Tag.equal(tagFileDescriptor):
			if len(item.Value) != 2 && len(item.Value) != 5 {
				return errors.New("unexpected file descriptor length")
			}
			parsed.FileStructure = item.Value[0]
			if len(item.Value) == 5 {
				parsed.RecordSize = binary.BigEndian.Uint16(item.Value[2:4])
				parsed.RecordCount = uint16(item.Value[4])
			}
		case item.Tag.equal(tagFileSize):
			if len(item.Value) != 2 {
				return errors.New("unexpected file size length")
			}
			parsed.FileSize = binary.BigEndian.Uint16(item.Value)
		}
	}
	if parsed.FileStructure == 0 {
		return errors.New("missing file descriptor")
	}
	*info = parsed
	return nil
}

func (app *Application) UnmarshalBinary(data []byte) error {
	data = trimRecordPadding(data)
	if len(data) == 0 {
		*app = Application{}
		return nil
	}
	var record tlv
	if err := record.unmarshalBinary(data); err != nil {
		return err
	}
	if !record.Tag.equal(tagApplication) {
		return errors.New("missing application template")
	}

	parsed := Application{}
	for _, item := range record.Children {
		switch {
		case item.Tag.equal(tagApplicationAID):
			parsed.AID = slices.Clone(item.Value)
		case item.Tag.equal(tagApplicationName):
			parsed.Label = string(item.Value)
		}
	}
	if len(parsed.AID) == 0 {
		return errors.New("missing application AID")
	}
	*app = parsed
	return nil
}

func trimRecordPadding(data []byte) []byte {
	for len(data) > 0 && data[len(data)-1] == 0xff {
		data = data[:len(data)-1]
	}
	return data
}
