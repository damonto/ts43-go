package at

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/damonto/ts43-go/apdu"
	"github.com/damonto/ts43-go/sim"
)

type AT struct {
	mu     sync.Mutex
	s      io.ReadWriteCloser
	reader *bufio.Reader
}

var (
	errReadTimeout = errors.New("read timeout")
)

func New(device string) (*AT, error) {
	s, err := Open(device)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", device, err)
	}
	return &AT{
		s:      s,
		reader: bufio.NewReader(s),
	}, nil
}

func (a *AT) sendCommand(command string) (string, error) {
	if err := writeFull(a.s, []byte(command+"\r\n")); err != nil {
		return "", err
	}
	var sb strings.Builder
	for {
		line, err := a.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "", line == command:
			continue
		case line == "OK":
			return strings.TrimSpace(sb.String()), nil
		case line == "ERROR", strings.HasPrefix(line, "+CME ERROR:"), strings.HasPrefix(line, "+CMS ERROR:"):
			return "", errors.New(line)
		default:
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(line)
		}
	}
}

func (a *AT) AuthenticateAKA(ctx context.Context, request *sim.AKARequest) (*sim.AKAResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command, err := (sim.Authenticate3G{Request: request}).APDU()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.selectUSIMApplication(); err != nil {
		return nil, err
	}
	resp, err := a.transmitUSIM(command)
	if err != nil {
		return nil, err
	}
	var out sim.AKAResponse
	if err := out.UnmarshalBinary(resp.Data()); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *AT) Identity(ctx context.Context) (*sim.Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	iccid, err := a.iccid()
	if err != nil {
		return nil, err
	}
	if err := a.selectUSIMApplication(); err != nil {
		return nil, err
	}
	imsi, err := a.imsi()
	if err != nil {
		return nil, err
	}
	ad, err := a.administrativeData()
	if err != nil {
		return nil, err
	}
	var gid1 string
	if data, err := a.groupIdentifier1(); err == nil {
		gid1 = sim.GroupIdentifierString(data)
	}
	return sim.NewIdentity(sim.Identity{ICCID: iccid, IMSI: imsi, GID1: gid1}, ad)
}

func (a *AT) transmitAPDU(command []byte) ([]byte, error) {
	cmd, err := CSIMCommand(command).MarshalText()
	if err != nil {
		return nil, err
	}
	rawResponse, err := a.sendCommand(string(cmd))
	if err != nil {
		return nil, err
	}
	var response CSIMResponse
	if err := response.UnmarshalText([]byte(rawResponse)); err != nil {
		return nil, err
	}
	if len(response) < 2 {
		return nil, errors.New("invalid response status word")
	}
	return []byte(response), nil
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

func (a *AT) iccid() (string, error) {
	if err := a.selectFiles(0x3f00, 0x2fe2); err != nil {
		return "", fmt.Errorf("select EF_ICCID: %w", err)
	}
	resp, err := a.transmitUSIM((sim.ReadBinary{Length: 10}).APDU())
	if err != nil {
		return "", fmt.Errorf("read EF_ICCID: %w", err)
	}
	return sim.DecodeSwappedBCD(resp.Data(), true), nil
}

func (a *AT) imsi() (string, error) {
	if err := a.selectFiles(0x6f07); err != nil {
		return "", fmt.Errorf("select EF_IMSI: %w", err)
	}
	resp, err := a.transmitUSIM((sim.ReadBinary{Length: 9}).APDU())
	if err != nil {
		return "", fmt.Errorf("read EF_IMSI: %w", err)
	}
	var imsi sim.IMSI
	if err := imsi.UnmarshalBinary(resp.Data()); err != nil {
		return "", err
	}
	return imsi.String(), nil
}

func (a *AT) administrativeData() ([]byte, error) {
	if err := a.selectFiles(0x6fad); err != nil {
		return nil, fmt.Errorf("select EF_AD: %w", err)
	}
	resp, err := a.transmitUSIM((sim.ReadBinary{Length: 4}).APDU())
	if err != nil {
		return nil, fmt.Errorf("read EF_AD: %w", err)
	}
	return resp.Data(), nil
}

func (a *AT) groupIdentifier1() ([]byte, error) {
	if err := a.selectFiles(0x6f3e); err != nil {
		return nil, err
	}
	resp, err := a.transmitUSIM((sim.ReadBinary{}).APDU())
	if err != nil {
		return nil, err
	}
	return resp.Data(), nil
}

func (a *AT) selectUSIMApplication() error {
	aid, err := sim.DiscoverUSIMAID(a.transmitUSIM)
	if err != nil {
		return fmt.Errorf("discover USIM application: %w", err)
	}
	if _, err := a.transmitUSIM((sim.SelectName{Name: aid}).APDU()); err != nil {
		return fmt.Errorf("select USIM application: %w", err)
	}
	return nil
}

func (a *AT) selectFiles(ids ...uint16) error {
	for _, id := range ids {
		if _, err := a.transmitUSIM((sim.SelectID{ID: id}).APDU()); err != nil {
			return fmt.Errorf("select file %04X: %w", id, err)
		}
	}
	return nil
}

func (a *AT) transmitUSIM(command []byte) (apdu.Response, error) {
	raw, err := a.transmitAPDU(command)
	if err != nil {
		return nil, err
	}
	resp := apdu.Response(raw)
	if len(resp) < 2 {
		return nil, errors.New("short APDU response")
	}
	data := append([]byte(nil), resp.Data()...)
	for resp.HasMore() {
		resp, err = a.more(resp.SW2())
		if err != nil {
			return nil, err
		}
		data = append(data, resp.Data()...)
	}
	if !resp.OK() {
		return nil, fmt.Errorf("APDU status %04X", resp.SW())
	}
	return append(data, byte(resp.SW()>>8), byte(resp.SW())), nil
}

func (a *AT) more(le byte) (apdu.Response, error) {
	resp, err := a.transmitAPDU((&apdu.Request{CLA: 0x00, INS: 0xc0, Le: &le}).APDU())
	if err != nil {
		return nil, err
	}
	out := apdu.Response(resp)
	if len(out) < 2 {
		return nil, errors.New("short APDU response")
	}
	return out, nil
}

func (a *AT) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.s.Close()
}
