package ccid

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ElMostafaIdrassi/goscard"
	"github.com/damonto/ts43-go/apdu"
	"github.com/damonto/ts43-go/sim"
)

var pcsc struct {
	mu   sync.Mutex
	refs int
}

type CCID interface {
	Identity(context.Context) (*sim.Identity, error)
	AuthenticateAKA(context.Context, *sim.AKARequest) (*sim.AKAResponse, error)
	ListReaders() ([]string, error)
	SetReader(reader string) error
	Disconnect() error
}

type CCIDReader struct {
	mu      sync.Mutex
	context goscard.Context
	card    goscard.Card
	ioSend  *goscard.SCardIORequest
	reader  string
	open    bool
	closed  bool
}

func New() (*CCIDReader, error) {
	return NewWithReader("")
}

// NewWithReader creates a CCID channel with reader preselected.
func NewWithReader(reader string) (*CCIDReader, error) {
	if err := acquirePCSC(); err != nil {
		return nil, err
	}
	context, _, err := goscard.NewContext(goscard.SCardScopeSystem, nil, nil)
	if err != nil {
		releasePCSC()
		return nil, err
	}
	ccid := &CCIDReader{context: context, reader: reader}
	return ccid, nil
}

func (c *CCIDReader) ListReaders() ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, errors.New("ccid reader is closed")
	}
	readers, _, err := c.context.ListReaders(nil)
	if err != nil {
		return nil, err
	}
	return readers, nil
}

// SetReader selects the reader used by Connect.
func (c *CCIDReader) SetReader(reader string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return errors.New("ccid reader is closed")
	}
	if c.open {
		return errors.New("ccid reader is connected")
	}
	c.reader = reader
	return nil
}

func (c *CCIDReader) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.disconnect()
}

func (c *CCIDReader) AuthenticateAKA(ctx context.Context, request *sim.AKARequest) (*sim.AKAResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command, err := (sim.Authenticate3G{Request: request}).APDU()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connectCard(); err != nil {
		return nil, err
	}
	if err := c.selectUSIMApplication(); err != nil {
		return nil, err
	}
	resp, err := c.transmitUSIM(command)
	if err != nil {
		return nil, err
	}
	var out sim.AKAResponse
	if err := out.UnmarshalBinary(resp.Data()); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *CCIDReader) Identity(ctx context.Context) (*sim.Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connectCard(); err != nil {
		return nil, err
	}
	iccid, err := c.iccid()
	if err != nil {
		return nil, err
	}
	if err := c.selectUSIMApplication(); err != nil {
		return nil, err
	}
	imsi, err := c.imsi()
	if err != nil {
		return nil, err
	}
	ad, err := c.administrativeData()
	if err != nil {
		return nil, err
	}
	var gid1 string
	if data, err := c.groupIdentifier1(); err == nil {
		gid1 = sim.GroupIdentifierString(data)
	}
	return sim.NewIdentity(sim.Identity{ICCID: iccid, IMSI: imsi, GID1: gid1}, ad)
}

func (c *CCIDReader) connectCard() error {
	if c.closed {
		return errors.New("ccid reader is closed")
	}
	if c.open {
		return nil
	}
	if c.reader == "" {
		return errors.New("ccid reader is required")
	}
	var err error
	c.card, _, err = c.context.Connect(c.reader, goscard.SCardShareShared, goscard.SCardProtocolAny)
	if err != nil {
		return err
	}
	c.ioSend, err = ioRequestForProtocol(c.card.ActiveProtocol())
	if err != nil {
		c.open = true
		return errors.Join(err, c.disconnectCard())
	}
	c.open = true
	return nil
}

func (c *CCIDReader) transmit(command []byte) ([]byte, error) {
	if !c.open {
		if c.closed {
			return nil, errors.New("ccid reader is closed")
		}
		return nil, errors.New("ccid reader is not connected")
	}
	r, _, err := c.card.Transmit(c.ioSend, command, nil)
	return r, err
}

func (c *CCIDReader) iccid() (string, error) {
	if err := c.selectFiles(0x3f00, 0x2fe2); err != nil {
		return "", fmt.Errorf("select EF_ICCID: %w", err)
	}
	resp, err := c.transmitUSIM((sim.ReadBinary{Length: 10}).APDU())
	if err != nil {
		return "", fmt.Errorf("read EF_ICCID: %w", err)
	}
	return sim.DecodeSwappedBCD(resp.Data(), true), nil
}

func (c *CCIDReader) imsi() (string, error) {
	if err := c.selectFiles(0x6f07); err != nil {
		return "", fmt.Errorf("select EF_IMSI: %w", err)
	}
	resp, err := c.transmitUSIM((sim.ReadBinary{Length: 9}).APDU())
	if err != nil {
		return "", fmt.Errorf("read EF_IMSI: %w", err)
	}
	var imsi sim.IMSI
	if err := imsi.UnmarshalBinary(resp.Data()); err != nil {
		return "", err
	}
	return imsi.String(), nil
}

func (c *CCIDReader) administrativeData() ([]byte, error) {
	if err := c.selectFiles(0x6fad); err != nil {
		return nil, fmt.Errorf("select EF_AD: %w", err)
	}
	resp, err := c.transmitUSIM((sim.ReadBinary{Length: 4}).APDU())
	if err != nil {
		return nil, fmt.Errorf("read EF_AD: %w", err)
	}
	return resp.Data(), nil
}

func (c *CCIDReader) groupIdentifier1() ([]byte, error) {
	if err := c.selectFiles(0x6f3e); err != nil {
		return nil, err
	}
	resp, err := c.transmitUSIM((sim.ReadBinary{}).APDU())
	if err != nil {
		return nil, err
	}
	return resp.Data(), nil
}

func (c *CCIDReader) selectUSIMApplication() error {
	aid, err := sim.DiscoverUSIMAID(c.transmitUSIM)
	if err != nil {
		return fmt.Errorf("discover USIM application: %w", err)
	}
	if _, err := c.transmitUSIM((sim.SelectName{Name: aid}).APDU()); err != nil {
		return fmt.Errorf("select USIM application: %w", err)
	}
	return nil
}

func (c *CCIDReader) selectFiles(ids ...uint16) error {
	for _, id := range ids {
		if _, err := c.transmitUSIM((sim.SelectID{ID: id}).APDU()); err != nil {
			return fmt.Errorf("select file %04X: %w", id, err)
		}
	}
	return nil
}

func (c *CCIDReader) transmitUSIM(command []byte) (apdu.Response, error) {
	return transmitUSIM(c.transmit, command)
}

func transmitUSIM(transmit func([]byte) ([]byte, error), command []byte) (apdu.Response, error) {
	raw, err := transmit(command)
	if err != nil {
		return nil, err
	}
	resp := apdu.Response(raw)
	if len(resp) < 2 {
		return nil, errors.New("short APDU response")
	}
	data := append([]byte(nil), resp.Data()...)
	for statusHasMore(resp) {
		resp, err = more(transmit, resp.SW2())
		if err != nil {
			return nil, err
		}
		data = append(data, resp.Data()...)
	}
	if !statusOK(resp) {
		return nil, fmt.Errorf("APDU status %04X", resp.SW())
	}
	return append(data, byte(resp.SW()>>8), byte(resp.SW())), nil
}

func more(transmit func([]byte) ([]byte, error), le byte) (apdu.Response, error) {
	resp, err := transmit((&apdu.Request{CLA: 0x00, INS: 0xc0, Le: &le}).APDU())
	if err != nil {
		return nil, err
	}
	out := apdu.Response(resp)
	if len(out) < 2 {
		return nil, errors.New("short APDU response")
	}
	return out, nil
}

func statusOK(response []byte) bool {
	return len(response) >= 2 && response[len(response)-2] == 0x90 && response[len(response)-1] == 0x00
}

func statusHasMore(response []byte) bool {
	return len(response) >= 2 && response[len(response)-2] == 0x61
}

func (c *CCIDReader) disconnect() error {
	if c.closed {
		return nil
	}
	if c.open {
		if err := c.disconnectCard(); err != nil {
			return err
		}
	}
	if _, err := c.context.Release(); err != nil {
		return err
	}
	c.closed = true
	releasePCSC()
	return nil
}

func (c *CCIDReader) disconnectCard() error {
	if !c.open {
		return nil
	}
	if _, err := c.card.Disconnect(goscard.SCardLeaveCard); err != nil {
		return err
	}
	c.open = false
	c.ioSend = nil
	return nil
}

func acquirePCSC() error {
	pcsc.mu.Lock()
	defer pcsc.mu.Unlock()

	if pcsc.refs == 0 {
		if err := goscard.Initialize(goscard.NewDefaultLogger(goscard.LogLevelNone)); err != nil {
			return err
		}
	}
	pcsc.refs++
	return nil
}

func releasePCSC() {
	pcsc.mu.Lock()
	defer pcsc.mu.Unlock()

	if pcsc.refs == 0 {
		return
	}
	pcsc.refs--
	if pcsc.refs == 0 {
		goscard.Finalize()
	}
}

func ioRequestForProtocol(protocol goscard.SCardProtocol) (*goscard.SCardIORequest, error) {
	switch protocol {
	case goscard.SCardProtocolT0:
		return &goscard.SCardIoRequestT0, nil
	case goscard.SCardProtocolT1:
		return &goscard.SCardIoRequestT1, nil
	default:
		return nil, fmt.Errorf("unsupported active PC/SC protocol: %s", protocol.String())
	}
}
