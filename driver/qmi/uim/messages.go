package uim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/damonto/ts43-go/driver/qmi/protocol"
)

// region Switch Slot Request

type SwitchSlotRequest struct {
	ClientID      uint8
	TransactionID uint16
	LogicalSlot   uint8
	PhysicalSlot  uint32
	Response      *SwitchSlotResponse
}

func (r *SwitchSlotRequest) Request() *protocol.Request {
	r.Response = new(SwitchSlotResponse)
	physicalSlot := make([]byte, 4)
	binary.LittleEndian.PutUint32(physicalSlot, r.PhysicalSlot)
	return &protocol.Request{
		ClientID:      r.ClientID,
		TransactionID: r.TransactionID,
		MessageID:     protocol.QMIUIMSwitchSlot,
		ServiceType:   protocol.QMIServiceUIM,
		Value: protocol.TLVs{
			{Type: 0x01, Len: 1, Value: []byte{r.LogicalSlot}},
			{Type: 0x02, Len: uint16(len(physicalSlot)), Value: physicalSlot},
		},
		Response: r.Response,
	}
}

type SwitchSlotResponse struct{}

func (r *SwitchSlotResponse) UnmarshalResponse(TLVs *protocol.TLVs) error { return nil }

// endregion

// region Get Slot Status Request

type GetSlotStatusRequest struct {
	ClientID      uint8
	TransactionID uint16
	Response      *GetSlotStatusResponse
}

func (r *GetSlotStatusRequest) Request() *protocol.Request {
	r.Response = new(GetSlotStatusResponse)
	return &protocol.Request{
		ClientID:      r.ClientID,
		TransactionID: r.TransactionID,
		MessageID:     protocol.QMIUIMGetSlotStatus,
		ServiceType:   protocol.QMIServiceUIM,
		ReadTimeout:   1 * time.Second,
		Response:      r.Response,
	}
}

type GetSlotStatusResponse struct {
	Slots         []Slot
	ActivatedSlot uint8
}

type Slot struct {
	CardState   PhysicalCardState
	SlotState   SlotState
	LogicalSlot uint8
	ICCID       []byte
}

func (r *GetSlotStatusResponse) UnmarshalResponse(TLVs *protocol.TLVs) error {
	value, ok := TLVs.Find(0x10)
	if !ok {
		return errors.New("could not find slot status in response")
	}

	buf := bytes.NewReader(value.Value)
	slotCount, err := readUint8(buf, "slot count")
	if err != nil {
		return err
	}
	r.Slots = make([]Slot, 0, slotCount)
	for i := range slotCount {
		var slot Slot
		if err := readValue(buf, &slot.CardState, "physical card status"); err != nil {
			return err
		}
		if err := readValue(buf, &slot.SlotState, "physical slot status"); err != nil {
			return err
		}
		slot.LogicalSlot, err = readUint8(buf, "logical slot")
		if err != nil {
			return err
		}
		slot.ICCID, err = readUint8Array(buf, "ICCID")
		if err != nil {
			return err
		}
		if slot.SlotState == SlotStateActive {
			r.ActivatedSlot = uint8(i + 1)
		}
		r.Slots = append(r.Slots, slot)
	}
	return nil
}

// endregion

// region Get Card Status Request

type GetCardStatusRequest struct {
	ClientID      uint8
	TransactionID uint16
	Response      *GetCardStatusResponse
}

func (r *GetCardStatusRequest) Request() *protocol.Request {
	r.Response = new(GetCardStatusResponse)
	return &protocol.Request{
		ClientID:      r.ClientID,
		TransactionID: r.TransactionID,
		MessageID:     protocol.QMIUIMGetCardStatus,
		ServiceType:   protocol.QMIServiceUIM,
		Response:      r.Response,
	}
}

type GetCardStatusResponse struct {
	IndexGWPrimary   uint16
	Index1XPrimary   uint16
	IndexGWSecondary uint16
	Index1XSecondary uint16
	Cards            []Card
}

type Card struct {
	State        CardState
	Applications []Application
}

type Application struct {
	Type  CardApplicationType
	State CardApplicationState
}

func (r *GetCardStatusResponse) UnmarshalResponse(TLVs *protocol.TLVs) error {
	value, ok := TLVs.Find(0x10)
	if !ok {
		return errors.New("could not find card status in response")
	}

	buf := bytes.NewReader(value.Value)
	if err := readValue(buf, &r.IndexGWPrimary, "GW primary index"); err != nil {
		return err
	}
	if err := readValue(buf, &r.Index1XPrimary, "1X primary index"); err != nil {
		return err
	}
	if err := readValue(buf, &r.IndexGWSecondary, "GW secondary index"); err != nil {
		return err
	}
	if err := readValue(buf, &r.Index1XSecondary, "1X secondary index"); err != nil {
		return err
	}

	cardLen, err := readUint8(buf, "card count")
	if err != nil {
		return err
	}
	r.Cards = make([]Card, 0, cardLen)
	for range cardLen {
		var card Card
		if err := readValue(buf, &card.State, "card state"); err != nil {
			return err
		}
		if err := skipBytes(buf, 4, "card PIN/error status"); err != nil {
			return err
		}

		appLen, err := readUint8(buf, "application count")
		if err != nil {
			return err
		}
		card.Applications = make([]Application, 0, appLen)
		for range appLen {
			var app Application
			if err := readValue(buf, &app.Type, "application type"); err != nil {
				return err
			}
			if err := readValue(buf, &app.State, "application state"); err != nil {
				return err
			}
			card.Applications = append(card.Applications, app)

			if err := skipBytes(buf, 4, "application personalization status"); err != nil {
				return err
			}
			if _, err := readUint8Array(buf, "application identifier"); err != nil {
				return err
			}
			if err := skipBytes(buf, 7, "application PIN status"); err != nil {
				return err
			}
		}
		r.Cards = append(r.Cards, card)
	}
	return nil
}

func (r *GetCardStatusResponse) Ready() bool {
	for _, card := range r.Cards {
		if card.State == CardStatePresent {
			for _, app := range card.Applications {
				if app.Type == CardApplicationTypeUSIM && app.State == CardApplicationStateReady {
					return true
				}
			}
		}
	}
	return false
}

// endregion

// region Authenticate Request

type AuthenticateRequest struct {
	ClientID      uint8
	TransactionID uint16
	SessionType   SessionType
	AID           []byte
	Context       AuthenticationContext
	AuthData      []byte
	Response      *AuthenticateResponse
}

func (r *AuthenticateRequest) Request() *protocol.Request {
	session := make([]byte, 0, 2+len(r.AID))
	session = append(session, byte(r.SessionType), byte(len(r.AID)))
	session = append(session, r.AID...)

	data := make([]byte, 0, 3+len(r.AuthData))
	data = append(data, byte(r.Context))
	data = binary.LittleEndian.AppendUint16(data, uint16(len(r.AuthData)))
	data = append(data, r.AuthData...)

	r.Response = new(AuthenticateResponse)
	return &protocol.Request{
		ClientID:      r.ClientID,
		TransactionID: r.TransactionID,
		MessageID:     protocol.QMIUIMAuthenticate,
		ServiceType:   protocol.QMIServiceUIM,
		Value: protocol.TLVs{
			{Type: 0x01, Len: uint16(len(session)), Value: session},
			{Type: 0x02, Len: uint16(len(data)), Value: data},
		},
		Response: r.Response,
	}
}

type AuthenticateResponse struct {
	SW1             byte
	SW2             byte
	SecurityContext []byte
}

func (r *AuthenticateResponse) UnmarshalResponse(TLVs *protocol.TLVs) error {
	if value, ok := TLVs.Find(0x10); ok && len(value.Value) >= 2 {
		r.SW1 = value.Value[0]
		r.SW2 = value.Value[1]
	}
	value, ok := TLVs.Find(0x11)
	if !ok {
		return errors.New("could not find security context in response")
	}
	if len(value.Value) < 2 {
		return errors.New("security context response too short")
	}
	n := int(value.Value[0]) | (int(value.Value[1]) << 8)
	if len(value.Value) < 2+n {
		return errors.New("security context response truncated")
	}
	r.SecurityContext = value.Value[2 : 2+n]
	return nil
}

// endregion

// region Read Transparent Request

type ReadTransparentRequest struct {
	ClientID      uint8
	TransactionID uint16
	SessionType   SessionType
	FileID        uint16
	FilePath      []byte
	Offset        uint16
	Length        uint16
	Response      *ReadTransparentResponse
}

func (r *ReadTransparentRequest) Request() *protocol.Request {
	file := make([]byte, 0, 3+len(r.FilePath))
	file = binary.LittleEndian.AppendUint16(file, r.FileID)
	file = append(file, byte(len(r.FilePath)))
	file = append(file, r.FilePath...)
	readInfo := make([]byte, 0, 4)
	readInfo = binary.LittleEndian.AppendUint16(readInfo, r.Offset)
	readInfo = binary.LittleEndian.AppendUint16(readInfo, r.Length)
	r.Response = new(ReadTransparentResponse)
	return &protocol.Request{
		ClientID:      r.ClientID,
		TransactionID: r.TransactionID,
		MessageID:     protocol.QMIUIMReadTransparent,
		ServiceType:   protocol.QMIServiceUIM,
		Value: protocol.TLVs{
			{Type: 0x01, Len: 2, Value: []byte{byte(r.SessionType), 0x00}},
			{Type: 0x02, Len: uint16(len(file)), Value: file},
			{Type: 0x03, Len: uint16(len(readInfo)), Value: readInfo},
		},
		Response: r.Response,
	}
}

type ReadTransparentResponse struct {
	Data []byte
}

func (r *ReadTransparentResponse) UnmarshalResponse(TLVs *protocol.TLVs) error {
	value, ok := TLVs.Find(0x11)
	if !ok {
		return errors.New("could not find read result in response")
	}
	if len(value.Value) < 2 {
		return errors.New("read result response too short")
	}
	n := int(value.Value[0]) | (int(value.Value[1]) << 8)
	if len(value.Value) < 2+n {
		return errors.New("read result response truncated")
	}
	r.Data = append(r.Data[:0], value.Value[2:2+n]...)
	return nil
}

// endregion

func readValue(r io.Reader, out any, field string) error {
	if err := binary.Read(r, binary.LittleEndian, out); err != nil {
		return fmt.Errorf("read %s: %w", field, err)
	}
	return nil
}

func readUint8(r io.Reader, field string) (uint8, error) {
	var v uint8
	if err := readValue(r, &v, field); err != nil {
		return 0, err
	}
	return v, nil
}

func readUint8Array(r *bytes.Reader, field string) ([]byte, error) {
	n, err := readUint8(r, field+" length")
	if err != nil {
		return nil, err
	}
	value := make([]byte, n)
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, fmt.Errorf("read %s: %w", field, err)
	}
	return value, nil
}

func skipBytes(r *bytes.Reader, n int, field string) error {
	if r.Len() < n {
		return fmt.Errorf("read %s: %w", field, io.ErrUnexpectedEOF)
	}
	if _, err := r.Seek(int64(n), io.SeekCurrent); err != nil {
		return fmt.Errorf("skip %s: %w", field, err)
	}
	return nil
}
