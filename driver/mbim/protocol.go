package mbim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unicode/utf16"
)

// region Proxy Configuration

type ProxyConfigRequest struct {
	TransactionID uint32
	DevicePath    string
	Timeout       uint32
	Response      *ProxyConfigResponse
}

func (r *ProxyConfigRequest) Request() *Request {
	utf16s := utf16.Encode([]rune(r.DevicePath))
	utf16s = append(utf16s, 0) // null terminator
	pb := new(bytes.Buffer)
	_ = binary.Write(pb, binary.LittleEndian, utf16s)
	devicePathUTF16 := pb.Bytes()

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint32(12))
	binary.Write(buf, binary.LittleEndian, uint32(len(devicePathUTF16)))
	binary.Write(buf, binary.LittleEndian, r.Timeout)
	buf.Write(devicePathUTF16)

	r.Response = new(ProxyConfigResponse)
	return &Request{
		MessageType:   MessageTypeCommand,
		TransactionID: r.TransactionID,
		Command: &Command{
			FragmentTotal:   1,
			FragmentCurrent: 0,
			ServiceID:       ServiceMbimProxyControl,
			CommandID:       CIDProxyControlConfiguration,
			CommandType:     CommandTypeSet,
			Data:            buf.Bytes(),
		},
		Response: r.Response,
	}
}

// ProxyConfigResponse is empty for now
type ProxyConfigResponse struct{}

func (r *ProxyConfigResponse) UnmarshalBinary(data []byte) error { return nil }

// endregion

// region Open Device Request

type OpenDeviceRequest struct {
	TransactionID uint32
	Response      *OpenDeviceResponse
}

func (r *OpenDeviceRequest) Request() *Request {
	r.Response = new(OpenDeviceResponse)
	return &Request{
		MessageType:   MessageTypeOpen,
		TransactionID: r.TransactionID,
		Command:       r,
		Response:      r.Response,
	}
}

func (r *OpenDeviceRequest) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, maxControlTransfer)
	return buf, nil
}

type OpenDeviceResponse struct{}

func (p *OpenDeviceResponse) UnmarshalBinary(data []byte) error { return nil }

// endregion

// region Close Request

type CloseRequest struct {
	TransactionID uint32
	Response      *CloseResponse
}

func (r *CloseRequest) Request() *Request {
	r.Response = new(CloseResponse)
	return &Request{
		MessageType:   MessageTypeClose,
		TransactionID: r.TransactionID,
		ReadTimeout:   2 * time.Second,
		Command:       r,
		Response:      r.Response,
	}
}

func (r *CloseRequest) MarshalBinary() ([]byte, error) {
	return nil, nil
}

type CloseResponse struct{}

func (r *CloseResponse) UnmarshalBinary(data []byte) error { return nil }

// endregion

// region Device Slot Mappings

type DeviceSlotMappingsRequest struct {
	TransactionID uint32
	MapCount      uint32
	SlotMappings  []SlotMapping
	Response      *DeviceSlotMappingsResponse
}

type SlotMapping struct {
	Slot uint32
}

func (r *DeviceSlotMappingsRequest) Request() *Request {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, r.MapCount)

	if r.MapCount > 0 {
		dataOffset := 4 + (r.MapCount * 8) // 4 bytes for MapCount + offset table
		for i := uint32(0); i < r.MapCount; i++ {
			binary.Write(buf, binary.LittleEndian, dataOffset+i*4) // offset to slot data
			binary.Write(buf, binary.LittleEndian, uint32(4))      // size of slot data
		}
		for _, mapping := range r.SlotMappings {
			binary.Write(buf, binary.LittleEndian, mapping.Slot)
		}
	}

	r.Response = new(DeviceSlotMappingsResponse)
	commandType := CommandTypeQuery
	if r.MapCount > 0 {
		commandType = CommandTypeSet
	}
	return &Request{
		MessageType:   MessageTypeCommand,
		TransactionID: r.TransactionID,
		Command: &Command{
			FragmentTotal:   1,
			FragmentCurrent: 0,
			ServiceID:       ServiceMsBasicConnectExtensions,
			CommandID:       CIDDeviceSlotMappings,
			CommandType:     uint32(commandType),
			Data:            buf.Bytes(),
		},
		Response: r.Response,
	}
}

type DeviceSlotMappingsResponse struct {
	MapCount     uint32
	SlotMappings []SlotMapping
}

func (r *DeviceSlotMappingsResponse) UnmarshalBinary(data []byte) error {
	if len(data) < 4 {
		return errors.New("device slot mappings response data too short")
	}
	r.MapCount = binary.LittleEndian.Uint32(data[0:4])
	if r.MapCount == 0 {
		r.SlotMappings = nil
		return nil
	}
	if r.MapCount > uint32((len(data)-4)/8) {
		return errors.New("device slot mappings response buffer too short")
	}
	r.SlotMappings = make([]SlotMapping, r.MapCount)
	tableEnd := 4 + r.MapCount*8
	if len(data) < int(tableEnd) {
		return errors.New("device slot mappings response buffer too short")
	}
	for i := uint32(0); i < r.MapCount; i++ {
		entryOffset := 4 + i*8
		slotDataOffset := binary.LittleEndian.Uint32(data[entryOffset : entryOffset+4])
		slotDataSize := binary.LittleEndian.Uint32(data[entryOffset+4 : entryOffset+8])
		if slotDataSize != 4 {
			return errors.New("device slot mappings response slot data has invalid size")
		}
		if slotDataOffset > uint32(len(data)) || slotDataSize > uint32(len(data))-slotDataOffset {
			return errors.New("device slot mappings response slot data too short")
		}
		r.SlotMappings[i].Slot = binary.LittleEndian.Uint32(data[slotDataOffset : slotDataOffset+4])
	}
	return nil
}

// endregion

// region Subscriber Ready Status

type SubscriberReadyStatusRequest struct {
	TransactionID uint32
	Response      *SubscriberReadyStatusResponse
}

func (r *SubscriberReadyStatusRequest) Request() *Request {
	r.Response = new(SubscriberReadyStatusResponse)
	return &Request{
		MessageType:   MessageTypeCommand,
		TransactionID: r.TransactionID,
		ReadTimeout:   1 * time.Second,
		Command: &Command{
			FragmentTotal:   1,
			FragmentCurrent: 0,
			ServiceID:       ServiceBasicConnect,
			CommandID:       CIDSubscriberReadyStatus,
			CommandType:     CommandTypeQuery,
			Data:            []byte{},
		},
		Response: r.Response,
	}
}

type SubscriberReadyStatusResponse struct {
	ReadyState            uint32
	SubscriberID          string
	SIMICCID              string
	ReadyInfo             uint32
	TelephoneNumbersCount uint32
	TelephoneNumbers      []string
}

func (r *SubscriberReadyStatusResponse) UnmarshalBinary(data []byte) error {
	if len(data) < 28 {
		return errors.New("subscriber ready status response data too short")
	}
	r.ReadyState = binary.LittleEndian.Uint32(data[0:4])
	subscriberOffset := binary.LittleEndian.Uint32(data[4:8])
	subscriberSize := binary.LittleEndian.Uint32(data[8:12])
	iccidOffset := binary.LittleEndian.Uint32(data[12:16])
	iccidSize := binary.LittleEndian.Uint32(data[16:20])
	r.ReadyInfo = binary.LittleEndian.Uint32(data[20:24])
	r.TelephoneNumbersCount = binary.LittleEndian.Uint32(data[24:28])
	var err error
	r.SubscriberID, err = utf16StringAt(data, subscriberOffset, subscriberSize)
	if err != nil {
		return fmt.Errorf("subscriber ID: %w", err)
	}
	r.SIMICCID, err = utf16StringAt(data, iccidOffset, iccidSize)
	if err != nil {
		return fmt.Errorf("SIM ICCID: %w", err)
	}
	if r.TelephoneNumbersCount == 0 {
		r.TelephoneNumbers = nil
		return nil
	}
	if r.TelephoneNumbersCount > uint32((len(data)-28)/8) {
		return errors.New("subscriber ready status response telephone numbers table too short")
	}
	r.TelephoneNumbers = make([]string, r.TelephoneNumbersCount)
	for i := range r.TelephoneNumbersCount {
		entryOffset := 28 + i*8
		numberOffset := binary.LittleEndian.Uint32(data[entryOffset : entryOffset+4])
		numberSize := binary.LittleEndian.Uint32(data[entryOffset+4 : entryOffset+8])
		r.TelephoneNumbers[i], err = utf16StringAt(data, numberOffset, numberSize)
		if err != nil {
			return fmt.Errorf("telephone number %d: %w", i, err)
		}
	}
	return nil
}

// endregion

// region UICC Read Binary

type ReadBinaryRequest struct {
	TransactionID uint32
	ApplicationID []byte
	FilePath      []byte
	Offset        uint32
	Size          uint32
	Response      *ReadBinaryResponse
}

func (r *ReadBinaryRequest) Request() *Request {
	buf := new(bytes.Buffer)
	dataOffset := 44
	filePathOffset := align4(dataOffset + len(r.ApplicationID))
	binary.Write(buf, binary.LittleEndian, uint32(1))
	writeByteArrayRef(buf, dataOffset, r.ApplicationID)
	writeByteArrayRef(buf, filePathOffset, r.FilePath)
	binary.Write(buf, binary.LittleEndian, r.Offset)
	binary.Write(buf, binary.LittleEndian, r.Size)
	binary.Write(buf, binary.LittleEndian, uint32(0))
	binary.Write(buf, binary.LittleEndian, uint32(0))
	binary.Write(buf, binary.LittleEndian, uint32(0))
	binary.Write(buf, binary.LittleEndian, uint32(0))
	buf.Write(r.ApplicationID)
	writePadding(buf)
	buf.Write(r.FilePath)
	writePadding(buf)
	r.Response = new(ReadBinaryResponse)
	return &Request{
		MessageType:   MessageTypeCommand,
		TransactionID: r.TransactionID,
		Command: &Command{
			FragmentTotal:   1,
			FragmentCurrent: 0,
			ServiceID:       ServiceMsUiccLowLevelAccess,
			CommandID:       CIDUiccReadBinary,
			CommandType:     CommandTypeQuery,
			Data:            buf.Bytes(),
		},
		Response: r.Response,
	}
}

type ReadBinaryResponse struct {
	Version     uint32
	StatusWord1 uint32
	StatusWord2 uint32
	Data        []byte
}

func (r *ReadBinaryResponse) UnmarshalBinary(data []byte) error {
	if len(data) < 20 {
		return errors.New("read binary response data too short")
	}
	r.Version = binary.LittleEndian.Uint32(data[0:4])
	r.StatusWord1 = binary.LittleEndian.Uint32(data[4:8])
	r.StatusWord2 = binary.LittleEndian.Uint32(data[8:12])
	offset := binary.LittleEndian.Uint32(data[12:16])
	size := binary.LittleEndian.Uint32(data[16:20])
	if offset > uint32(len(data)) || size > uint32(len(data))-offset {
		return errors.New("read binary response buffer too short")
	}
	r.Data = append(r.Data[:0], data[offset:offset+size]...)
	return nil
}

// endregion

// region Auth AKA

type AuthAKARequest struct {
	TransactionID uint32
	Rand          []byte
	Autn          []byte
	Response      *AuthAKAResponse
}

func (r *AuthAKARequest) Request() *Request {
	buf := new(bytes.Buffer)
	buf.Write(r.Rand)
	buf.Write(r.Autn)
	r.Response = new(AuthAKAResponse)
	return &Request{
		MessageType:   MessageTypeCommand,
		TransactionID: r.TransactionID,
		Command: &Command{
			FragmentTotal:   1,
			FragmentCurrent: 0,
			ServiceID:       ServiceAuth,
			CommandID:       CIDAuthAKA,
			CommandType:     CommandTypeQuery,
			Data:            buf.Bytes(),
		},
		Response: r.Response,
	}
}

type AuthAKAResponse struct {
	Res          []byte
	ResLen       uint32
	IntegrityKey []byte
	CipheringKey []byte
	Auts         []byte
}

func (r *AuthAKAResponse) UnmarshalBinary(data []byte) error {
	if len(data) < 66 {
		return errors.New("auth AKA response data too short")
	}
	r.Res = append(r.Res[:0], data[0:16]...)
	r.ResLen = binary.LittleEndian.Uint32(data[16:20])
	r.IntegrityKey = append(r.IntegrityKey[:0], data[20:36]...)
	r.CipheringKey = append(r.CipheringKey[:0], data[36:52]...)
	r.Auts = append(r.Auts[:0], data[52:66]...)
	return nil
}

func utf16StringAt(data []byte, offset, size uint32) (string, error) {
	if size == 0 {
		return "", nil
	}
	if offset > uint32(len(data)) || size > uint32(len(data))-offset {
		return "", errors.New("string buffer too short")
	}
	raw := data[offset : offset+size]
	if len(raw)%2 != 0 {
		return "", errors.New("UTF-16 string has odd byte length")
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return string(utf16.Decode(u)), nil
}

func writeByteArrayRef(buf *bytes.Buffer, offset int, value []byte) {
	if len(value) == 0 {
		binary.Write(buf, binary.LittleEndian, uint32(0))
		binary.Write(buf, binary.LittleEndian, uint32(0))
		return
	}
	binary.Write(buf, binary.LittleEndian, uint32(offset))
	binary.Write(buf, binary.LittleEndian, uint32(len(value)))
}

func align4(n int) int {
	return (n + 3) &^ 3
}

func writePadding(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}

// endregion
