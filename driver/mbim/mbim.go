package mbim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/damonto/ts43-go/sim"
)

var (
	slotActivationPollInterval = 500 * time.Millisecond
	slotActivationAttempts     = 10
)

// MBIM exposes source-SIM identity and AKA operations over MBIM.
type MBIM struct {
	mu     sync.Mutex
	device string
	slot   uint8
	conn   net.Conn
	txnID  uint32
	open   bool
}

// New creates a new MBIM proxy connection to the specified device
func New(device string, slot uint8) (*MBIM, error) {
	if slot == 0 {
		return nil, fmt.Errorf("slot must be >= 1")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: "\x00mbim-proxy", Net: "unix"})
	if err != nil {
		return nil, err
	}
	m := &MBIM{
		device: device,
		slot:   slot - 1, // Convert to 0-based
		conn:   conn,
	}
	return m, nil
}

// Connect establishes MBIM session and opens device
func (m *MBIM) Connect() (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	defer func() {
		if err != nil {
			err = errors.Join(err, m.conn.Close())
		}
	}()
	if m.open {
		return nil
	}
	if err := m.configureProxy(); err != nil {
		return fmt.Errorf("configure proxy: %w", err)
	}
	if err := m.openDevice(); err != nil {
		return fmt.Errorf("open device: %w", err)
	}
	if err := m.ensureSlotActivated(); err != nil {
		return fmt.Errorf("ensure slot is activated: %w", err)
	}
	m.open = true
	return nil
}

// ensureSlotActivated checks if the desired slot is activated and activates it if necessary
func (m *MBIM) ensureSlotActivated() error {
	slot, err := m.currentActivatedSlot()
	if err != nil {
		return err
	}
	if slot == m.slot {
		return nil
	}
	if err := m.activateSlot(m.slot); err != nil {
		return err
	}
	return m.waitForSlotActivation()
}

// currentActivatedSlot queries the current active slot mapping
func (m *MBIM) currentActivatedSlot() (uint8, error) {
	request := DeviceSlotMappingsRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
		MapCount:      0, // Query operation
	}
	if err := request.Request().Transmit(m.conn); err != nil {
		return 0, err
	}
	if len(request.Response.SlotMappings) == 0 {
		return 0, errors.New("no slot mappings found")
	}
	return uint8(request.Response.SlotMappings[0].Slot), nil
}

// activateSlot sets the device to use the specified slot
func (m *MBIM) activateSlot(slot uint8) error {
	request := DeviceSlotMappingsRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
		MapCount:      1,
		SlotMappings: []SlotMapping{
			{Slot: uint32(slot)},
		},
	}
	if err := request.Request().Transmit(m.conn); err != nil {
		return err
	}
	return nil
}

// waitForSlotActivation waits for the slot to become active by checking subscriber ready status
func (m *MBIM) waitForSlotActivation() error {
	var err error
	var lastReadyState uint32
	var sawReadyState bool
	ticker := time.NewTicker(slotActivationPollInterval)
	defer ticker.Stop()

	for attempt := range slotActivationAttempts {
		if attempt > 0 {
			<-ticker.C
		}
		request := SubscriberReadyStatusRequest{
			TransactionID: atomic.AddUint32(&m.txnID, 1),
		}
		err = request.Request().Transmit(m.conn)
		if err == nil {
			sawReadyState = true
			lastReadyState = request.Response.ReadyState
			if lastReadyState == MBIMSubscriberReadyStateInitialized || lastReadyState == MBIMSubscriberReadyStateNoEsimProfile {
				return nil
			}
		}
	}
	if err != nil {
		if sawReadyState {
			return fmt.Errorf("sim did not become available after slot %d activation, last ready state %#x: %w", m.slot, lastReadyState, err)
		}
		return fmt.Errorf("sim did not become available after slot %d activation: %w", m.slot, err)
	}
	return fmt.Errorf("sim did not become available after slot %d activation, last ready state %#x", m.slot, lastReadyState)
}

// configureProxy sends a proxy configuration request with the device path.
func (m *MBIM) configureProxy() error {
	request := ProxyConfigRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
		DevicePath:    m.device,
		Timeout:       30,
	}
	err := request.Request().Transmit(m.conn)
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("device %s is not connected", m.device)
	}
	return err
}

// openDevice sends MBIM Open message to establish connection
func (m *MBIM) openDevice() error {
	request := OpenDeviceRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
	}
	return request.Request().Transmit(m.conn)
}

func (m *MBIM) AuthenticateAKA(ctx context.Context, request *sim.AKARequest) (*sim.AKAResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(request.Rand) != 16 {
		return nil, fmt.Errorf("RAND length %d, want 16", len(request.Rand))
	}
	if len(request.Autn) != 16 {
		return nil, fmt.Errorf("AUTN length %d, want 16", len(request.Autn))
	}
	if err := m.Connect(); err != nil {
		return nil, err
	}
	auth, err := m.runAKA(request.Rand, request.Autn)
	if err != nil {
		return nil, err
	}
	resLen := int(auth.ResLen)
	if resLen > len(auth.Res) {
		resLen = len(auth.Res)
	}
	if resLen == 0 && hasNonZero(auth.Auts) {
		return &sim.AKAResponse{Auts: append([]byte(nil), auth.Auts...)}, nil
	}
	return &sim.AKAResponse{
		Res: append([]byte(nil), auth.Res[:resLen]...),
		CK:  append([]byte(nil), auth.CipheringKey...),
		IK:  append([]byte(nil), auth.IntegrityKey...),
	}, nil
}

func (m *MBIM) Identity(ctx context.Context) (*sim.Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.Connect(); err != nil {
		return nil, err
	}
	status, err := m.subscriberReadyStatus()
	if err != nil {
		return nil, err
	}
	ad, err := m.readBinary(sim.USIMAID, []byte{0x6f, 0xad}, 4)
	if err != nil {
		return nil, fmt.Errorf("read EF_AD: %w", err)
	}
	var gid1 string
	if data, err := m.readBinary(sim.USIMAID, []byte{0x6f, 0x3e}, 0); err == nil {
		gid1 = sim.GroupIdentifierString(data)
	}
	return sim.NewIdentity(sim.Identity{ICCID: status.SimICCID, IMSI: status.SubscriberID, GID1: gid1}, ad)
}

func (m *MBIM) subscriberReadyStatus() (*SubscriberReadyStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := SubscriberReadyStatusRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
	}
	if err := request.Request().Transmit(m.conn); err != nil {
		return nil, err
	}
	return request.Response, nil
}

func (m *MBIM) runAKA(rand, autn []byte) (*AuthAKAResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := AuthAKARequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
		Rand:          rand,
		Autn:          autn,
	}
	if err := request.Request().Transmit(m.conn); err != nil {
		return nil, err
	}
	return request.Response, nil
}

func (m *MBIM) readBinary(aid, path []byte, size uint32) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := ReadBinaryRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
		ApplicationID: aid,
		FilePath:      path,
		Size:          size,
	}
	if err := request.Request().Transmit(m.conn); err != nil {
		return nil, err
	}
	if request.Response.StatusWord1 != 0x90 || request.Response.StatusWord2 != 0x00 {
		return nil, fmt.Errorf("read binary status %02X%02X", request.Response.StatusWord1, request.Response.StatusWord2)
	}
	return request.Response.Data, nil
}

// Disconnect closes the MBIM connection and releases resources
func (m *MBIM) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	request := CloseRequest{
		TransactionID: atomic.AddUint32(&m.txnID, 1),
	}
	err := request.Request().Transmit(m.conn)
	m.open = false
	return errors.Join(err, m.conn.Close())
}

func hasNonZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return true
		}
	}
	return false
}
