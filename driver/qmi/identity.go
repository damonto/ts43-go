package qmi

import (
	"context"
	"fmt"

	"github.com/damonto/ts43-go/driver/qmi/uim"
	"github.com/damonto/ts43-go/sim"
)

var (
	mfPath   = []byte{0x00, 0x3f}
	usimPath = []byte{0x00, 0x3f, 0xff, 0x7f}
)

func (q *QMI) Identity(ctx context.Context) (*sim.Identity, error) {
	return identity(ctx, &q.Client)
}

func (c *QRTR) Identity(ctx context.Context) (*sim.Identity, error) {
	return identity(ctx, &c.Client)
}

func identity(ctx context.Context, client *uim.Client) (*sim.Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}
	iccidData, err := client.ReadTransparent(0x2fe2, mfPath, 10)
	if err != nil {
		return nil, fmt.Errorf("read EF_ICCID: %w", err)
	}
	imsiData, err := client.ReadTransparent(0x6f07, usimPath, 9)
	if err != nil {
		return nil, fmt.Errorf("read EF_IMSI: %w", err)
	}
	ad, err := client.ReadTransparent(0x6fad, usimPath, 4)
	if err != nil {
		return nil, fmt.Errorf("read EF_AD: %w", err)
	}
	var imsi sim.IMSI
	if err := imsi.UnmarshalBinary(imsiData); err != nil {
		return nil, err
	}
	var gid1 string
	if data, err := client.ReadTransparent(0x6f3e, usimPath, 0); err == nil {
		gid1 = sim.GroupIdentifierString(data)
	}
	return sim.NewIdentity(sim.Identity{
		ICCID: sim.DecodeSwappedBCD(iccidData, true),
		IMSI:  imsi.String(),
		GID1:  gid1,
	}, ad)
}
