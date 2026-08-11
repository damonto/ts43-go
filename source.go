package ts43

import (
	"context"
	"errors"

	"github.com/damonto/wwan-go/sim"
)

// ErrAuthenticationRejected reports a USIM AKA authentication reject.
var ErrAuthenticationRejected = errors.New("source SIM authentication rejected")

type Source struct {
	card *sim.Card
}

func NewSource(card *sim.Card) (*Source, error) {
	if card == nil {
		return nil, errors.New("creating source channel: USIM card is nil")
	}
	return &Source{card: card}, nil
}

func (s *Source) Identity(ctx context.Context) (*Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Identity{
		ICCID: s.card.ICCID(),
		IMSI:  s.card.IMSI(),
		MCC:   s.card.MCC(),
		MNC:   s.card.MNC(),
		GID1:  s.card.GID1(),
		GID2:  s.card.GID2(),
		SPN:   s.card.SPN(),
	}, nil
}

func (s *Source) AuthenticateAKA(ctx context.Context, req *AKARequest) (*AKAResponse, error) {
	if req == nil {
		return nil, errors.New("authenticating source SIM: request is nil")
	}
	result, err := s.card.AKA(ctx, req.Rand, req.Autn)
	if err != nil {
		return nil, err
	}
	if result.Reject {
		return nil, ErrAuthenticationRejected
	}
	return &AKAResponse{
		Res:  append([]byte(nil), result.RES...),
		CK:   append([]byte(nil), result.CK...),
		IK:   append([]byte(nil), result.IK...),
		Auts: append([]byte(nil), result.AUTS...),
	}, nil
}

func (s *Source) Close() error {
	return s.card.Close()
}
