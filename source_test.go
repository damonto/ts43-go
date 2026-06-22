package ts43

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/damonto/uicc-go/usim"
	usimcard "github.com/damonto/uicc-go/usim/card"
	"github.com/damonto/uicc-go/usim/simfile"
)

var usimAID = []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}

type fakeReader struct {
	authPayload []byte
	adPayload   []byte
	closed      bool
}

func newTestChannel(t *testing.T, authPayload []byte) *Source {
	return newTestChannelWithAD(t, authPayload, []byte{0x00, 0x00, 0x00, 0x03})
}

func newTestChannelWithAD(t *testing.T, authPayload, adPayload []byte) *Source {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	card, err := usim.New(context.Background(), &fakeReader{authPayload: authPayload, adPayload: adPayload}, logger)
	if err != nil {
		t.Fatalf("usim.New() error = %v", err)
	}
	channel, err := NewSource(card)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return channel
}

func (r *fakeReader) ListApplications(context.Context) ([]usimcard.Application, error) {
	return []usimcard.Application{{AID: slices.Clone(usimAID), Label: "USIM"}}, nil
}

func (r *fakeReader) FileAttributes(_ context.Context, file usimcard.FileRef) (usimcard.FileAttributes, error) {
	switch string(file.Path) {
	case string([]byte{0x2F, 0xE2}):
		return transparentAttrs(10), nil
	case string([]byte{0x6F, 0x07}):
		return transparentAttrs(9), nil
	case string([]byte{0x6F, 0xAD}):
		return transparentAttrs(4), nil
	case string([]byte{0x6F, 0x3E}):
		return transparentAttrs(2), nil
	default:
		return usimcard.FileAttributes{}, errors.New("file not found")
	}
}

func (r *fakeReader) ReadTransparent(_ context.Context, req usimcard.TransparentRead) ([]byte, error) {
	switch string(req.File.Path) {
	case string([]byte{0x2F, 0xE2}):
		return []byte{0x98, 0x68, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF0}, nil
	case string([]byte{0x6F, 0x07}):
		return []byte{0x08, 0x09, 0x10, 0x10, 0x10, 0x32, 0x54, 0x76, 0x98}, nil
	case string([]byte{0x6F, 0xAD}):
		return slices.Clone(r.adPayload), nil
	case string([]byte{0x6F, 0x3E}):
		return []byte{0xAA, 0xBB}, nil
	default:
		return nil, errors.New("file not found")
	}
}

func (r *fakeReader) ReadRecord(context.Context, usimcard.RecordRead) ([]byte, error) {
	return nil, errors.New("file not found")
}

func (r *fakeReader) Authenticate3G(context.Context, usimcard.AuthenticateRequest) ([]byte, error) {
	return slices.Clone(r.authPayload), nil
}

func (r *fakeReader) SMSPPDownload(context.Context, usimcard.SMSPPDownloadRequest) (usimcard.SMSPPDownloadResponse, error) {
	return usimcard.SMSPPDownloadResponse{}, errors.New("not implemented")
}

func (r *fakeReader) Close() error {
	r.closed = true
	return nil
}

func TestChannelIdentity(t *testing.T) {
	tests := []struct {
		name      string
		adPayload []byte
		want      *Identity
	}{
		{
			name:      "loaded USIM identity",
			adPayload: []byte{0x00, 0x00, 0x00, 0x03},
			want: &Identity{
				ICCID: "8986000000000000000",
				IMSI:  "001010123456789",
				MCC:   "001",
				MNC:   "010",
				GID1:  "AABB",
			},
		},
		{
			name:      "two-digit source MNC is passed through",
			adPayload: []byte{0x00, 0x00, 0x00, 0x02},
			want: &Identity{
				ICCID: "8986000000000000000",
				IMSI:  "001010123456789",
				MCC:   "001",
				MNC:   "01",
				GID1:  "AABB",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := newTestChannelWithAD(t, []byte{0xDB, 0x01, 0x01, 0x01, 0x02, 0x01, 0x03}, tt.adPayload)
			got, err := channel.Identity(context.Background())
			if err != nil {
				t.Fatalf("Identity() error = %v", err)
			}
			if *got != *tt.want {
				t.Fatalf("Identity() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestChannelAuthenticateAKA(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    *AKAResponse
		wantErr error
	}{
		{
			name:    "success",
			payload: []byte{0xDB, 0x02, 0x11, 0x22, 0x02, 0x33, 0x44, 0x02, 0x55, 0x66},
			want:    &AKAResponse{Res: []byte{0x11, 0x22}, CK: []byte{0x33, 0x44}, IK: []byte{0x55, 0x66}},
		},
		{
			name:    "sync failure",
			payload: []byte{0xDC, 0x0E, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D},
			want:    &AKAResponse{Auts: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D}},
		},
		{
			name:    "reject",
			payload: []byte{0xDC},
			wantErr: ErrAuthenticationRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := newTestChannel(t, tt.payload)
			got, err := channel.AuthenticateAKA(context.Background(), &AKARequest{
				Rand: bytes.Repeat([]byte{0x01}, 16),
				Autn: bytes.Repeat([]byte{0x02}, 16),
			})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AuthenticateAKA() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthenticateAKA() error = %v", err)
			}
			if !slices.Equal(got.Res, tt.want.Res) || !slices.Equal(got.CK, tt.want.CK) || !slices.Equal(got.IK, tt.want.IK) || !slices.Equal(got.Auts, tt.want.Auts) {
				t.Fatalf("AuthenticateAKA() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNewRejectsNilCard(t *testing.T) {
	tests := []struct {
		name string
		card *usim.Card
	}{
		{name: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewSource(tt.card); err == nil {
				t.Fatal("NewSource() error = nil, want non-nil")
			}
		})
	}
}

func transparentAttrs(size uint16) usimcard.FileAttributes {
	return usimcard.FileAttributes{
		FileStructure: simfile.StructureTransparent,
		FileSize:      size,
	}
}
