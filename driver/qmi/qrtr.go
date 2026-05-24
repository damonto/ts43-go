package qmi

import (
	"errors"

	"github.com/damonto/ts43-go/driver/qmi/protocol"
	"github.com/damonto/ts43-go/driver/qmi/qrtr"
	transport "github.com/damonto/ts43-go/driver/qmi/transport/qrtr"
	"github.com/damonto/ts43-go/driver/qmi/uim"
)

// QRTR exposes source-SIM identity and AKA operations over QRTR.
type QRTR struct {
	conn *qrtr.Conn
	uim.Client
}

// NewQRTR creates a new QRTR connection to the UIM service.
func NewQRTR(slot uint8) (*QRTR, error) {
	if slot == 0 {
		return nil, errors.New("slot must be >= 1")
	}

	conn, err := qrtr.Open(protocol.QMIServiceUIM)
	if err != nil {
		return nil, err
	}
	q := &QRTR{
		conn: conn,
		Client: uim.Client{
			Transport: transport.New(conn),
			Slot:      slot,
		},
	}
	return q, nil
}

func (c *QRTR) Disconnect() error {
	return c.conn.Close()
}
