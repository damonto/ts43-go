package uim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/damonto/ts43-go/driver/qmi/protocol"
	"github.com/damonto/ts43-go/sim"
)

// Client implements UIM smart card operations over a QMI transport.
type Client struct {
	mu        sync.Mutex
	Transport protocol.Transport
	Slot      uint8
	ClientID  uint8
	TxnID     uint32
}

// Connect establishes QMI session and allocates UIM client ID
func (q *Client) Connect() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if err := q.ensureSlotActivated(); err != nil {
		return err
	}
	// In QMI mode, we need to keep the SIM slot set to 1, because once the
	// configured slot becomes active, it will be assigned as slot 1.
	q.Slot = 1
	return nil
}

// ensureSlotActivated checks if the desired slot is activated and activates it if necessary
func (q *Client) ensureSlotActivated() error {
	slot, err := q.currentActivatedSlot()
	if err != nil {
		// Some older devices do not support the GetSlotStatusRequest QMI command
		if errors.Is(err, protocol.QMIErrorNotSupported) {
			return nil
		}
		return err
	}
	if slot == q.Slot {
		return nil
	}
	if err := q.switchSlot(); err != nil {
		return err
	}
	return q.waitForSlotActivation()
}

// waitForSlotActivation waits for the specified slot to be activated
func (q *Client) waitForSlotActivation() error {
	var err error
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for attempt := range 10 {
		if attempt > 0 {
			<-ticker.C
		}
		request := GetCardStatusRequest{
			ClientID:      q.ClientID,
			TransactionID: uint16(atomic.AddUint32(&q.TxnID, 1)),
		}
		err = q.Transport.Transmit(request.Request())
		if err == nil && request.Response.Ready() {
			return nil
		}
	}
	return fmt.Errorf("sim did not become available after slot %d activation err: %w", q.Slot, err)
}

// currentActivatedSlot returns the currently active logical slot
func (q *Client) currentActivatedSlot() (uint8, error) {
	request := GetSlotStatusRequest{
		ClientID:      q.ClientID,
		TransactionID: uint16(atomic.AddUint32(&q.TxnID, 1)),
	}
	if err := q.Transport.Transmit(request.Request()); err != nil {
		return 0, err
	}
	return request.Response.ActivatedSlot, nil
}

// switchSlot switches to the specified logical and physical slot
func (q *Client) switchSlot() error {
	request := SwitchSlotRequest{
		ClientID:      q.ClientID,
		TransactionID: uint16(atomic.AddUint32(&q.TxnID, 1)),
		LogicalSlot:   1,
		PhysicalSlot:  uint32(q.Slot),
	}
	return q.Transport.Transmit(request.Request())
}

func (q *Client) ReadTransparent(fileID uint16, path []byte, length uint16) ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	request := ReadTransparentRequest{
		ClientID:      q.ClientID,
		TransactionID: uint16(atomic.AddUint32(&q.TxnID, 1)),
		SessionType:   SessionTypePrimaryGWProvisioning,
		FileID:        fileID,
		FilePath:      path,
		Length:        length,
	}
	if err := q.Transport.Transmit(request.Request()); err != nil {
		return nil, err
	}
	return request.Response.Data, nil
}

func (q *Client) AuthenticateAKA(ctx context.Context, request *sim.AKARequest) (*sim.AKAResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(request.Rand) != 16 {
		return nil, fmt.Errorf("RAND length %d, want 16", len(request.Rand))
	}
	if len(request.Autn) != 16 {
		return nil, fmt.Errorf("AUTN length %d, want 16", len(request.Autn))
	}
	authData, err := request.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := q.Connect(); err != nil {
		return nil, err
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	auth := AuthenticateRequest{
		ClientID:      q.ClientID,
		TransactionID: uint16(atomic.AddUint32(&q.TxnID, 1)),
		SessionType:   SessionTypePrimaryGWProvisioning,
		Context:       AuthenticationContext3G,
		AuthData:      authData,
	}
	if err := q.Transport.Transmit(auth.Request()); err != nil {
		return nil, err
	}
	if auth.Response.SW1 != 0x90 || auth.Response.SW2 != 0x00 {
		return nil, fmt.Errorf("AKA authentication status %02X%02X", auth.Response.SW1, auth.Response.SW2)
	}
	var response sim.AKAResponse
	if err := response.UnmarshalBinary(auth.Response.SecurityContext); err != nil {
		return nil, err
	}
	return &response, nil
}
