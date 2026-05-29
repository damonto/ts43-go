package ts43

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	ts43http "github.com/damonto/ts43-go/http"
	"github.com/damonto/ts43-go/sim"
)

//go:embed wfc.json
var wfcJSON []byte

var (
	ErrWFCConfigNotFound  = errors.New("wfc config not found")
	ErrWFCConfigAmbiguous = errors.New("wfc config is ambiguous")
)

const (
	WFCEntitlementDisabled     WFCEntitlementStatus = 0
	WFCEntitlementEnabled      WFCEntitlementStatus = 1
	WFCEntitlementIncompatible WFCEntitlementStatus = 2
	WFCEntitlementProvisioning WFCEntitlementStatus = 3
	WFCTCNotAvailable          WFCTCStatus          = 0
	WFCTCAvailable             WFCTCStatus          = 1
	WFCTCNotRequired           WFCTCStatus          = 2
	WFCTCInProgress            WFCTCStatus          = 3
	WFCAddressNotAvailable     WFCAddressStatus     = 0
	WFCAddressAvailable        WFCAddressStatus     = 1
	WFCAddressNotRequired      WFCAddressStatus     = 2
	WFCAddressInProgress       WFCAddressStatus     = 3
	WFCProvisionNotProvisioned WFCProvisionStatus   = 0
	WFCProvisionProvisioned    WFCProvisionStatus   = 1
	WFCProvisionNotRequired    WFCProvisionStatus   = 2
	WFCProvisionInProgress     WFCProvisionStatus   = 3
)

type WFCEntitlementStatus int

func (s WFCEntitlementStatus) String() string { return strconv.Itoa(int(s)) }

func (s *WFCEntitlementStatus) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal WFC entitlement status: %w", err)
	}
	if !ok {
		*s = WFCEntitlementIncompatible
		return nil
	}
	*s = WFCEntitlementStatus(n)
	return nil
}

type WFCTCStatus int

func (s WFCTCStatus) String() string { return strconv.Itoa(int(s)) }

func (s *WFCTCStatus) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal WFC terms status: %w", err)
	}
	if !ok {
		*s = WFCTCNotRequired
		return nil
	}
	*s = WFCTCStatus(n)
	return nil
}

type WFCAddressStatus int

func (s WFCAddressStatus) String() string { return strconv.Itoa(int(s)) }

func (s *WFCAddressStatus) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal WFC address status: %w", err)
	}
	if !ok {
		*s = WFCAddressNotRequired
		return nil
	}
	*s = WFCAddressStatus(n)
	return nil
}

type WFCProvisionStatus int

func (s WFCProvisionStatus) String() string { return strconv.Itoa(int(s)) }

func (s *WFCProvisionStatus) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal WFC provision status: %w", err)
	}
	if !ok {
		*s = WFCProvisionNotRequired
		return nil
	}
	*s = WFCProvisionStatus(n)
	return nil
}

type WFCConfig struct {
	CarrierName                      string
	Server                           string
	App                              string
	AppName                          string
	Version                          string
	UsePost                          bool
	ClientTS43                       string
	DefaultServiceEntitlementEnabled bool
	SkipActivation                   bool
	ShowVoWiFiWebview                bool
}

type wfcRecord struct {
	MCCMNC                           string  `json:"mccmnc"`
	GID1                             *string `json:"gid1"`
	CarrierName                      string  `json:"carrierName"`
	EntitlementServer                string  `json:"entitlementServer"`
	App                              string  `json:"app"`
	AppName                          string  `json:"appName"`
	EntitlementVersion               string  `json:"entitlementVersion"`
	UseHTTPPost                      bool    `json:"useHttpPost"`
	ClientTS43                       string  `json:"clientTS43"`
	DefaultServiceEntitlementEnabled bool    `json:"defaultServiceEntitlementStatus"`
	SkipActivation                   bool    `json:"skipActivation"`
	ShowVoWiFiWebview                bool    `json:"showVoWiFiWebview"`
}

func DiscoverWFC(identity sim.Identity) (WFCConfig, error) {
	if identity.MCC == "" || identity.MNC == "" {
		return WFCConfig{}, errors.New("source MCC/MNC is required")
	}
	var records []wfcRecord
	if err := json.Unmarshal(wfcJSON, &records); err != nil {
		return WFCConfig{}, fmt.Errorf("parse WFC config: %w", err)
	}
	record, err := selectWFCRecord(records, identity.MCCMNC(), identity.GID1)
	if err != nil {
		return WFCConfig{}, err
	}
	return record.config(), nil
}

func selectWFCRecord(records []wfcRecord, mccmnc, gid1 string) (wfcRecord, error) {
	var fallback []wfcRecord
	var gidMatches []wfcRecord
	for _, record := range records {
		if record.MCCMNC != mccmnc {
			continue
		}
		if record.GID1 == nil {
			fallback = append(fallback, record)
			continue
		}
		if gid1 != "" && strings.EqualFold(*record.GID1, gid1) {
			gidMatches = append(gidMatches, record)
		}
	}
	if len(gidMatches) == 1 {
		return gidMatches[0], nil
	}
	if len(gidMatches) > 1 {
		return wfcRecord{}, fmt.Errorf("%w for MCCMNC %s GID1 %s", ErrWFCConfigAmbiguous, mccmnc, gid1)
	}
	if len(fallback) == 1 {
		return fallback[0], nil
	}
	if len(fallback) > 1 {
		return wfcRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrWFCConfigAmbiguous, mccmnc)
	}
	return wfcRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrWFCConfigNotFound, mccmnc)
}

func (r wfcRecord) config() WFCConfig {
	return WFCConfig{
		CarrierName:                      r.CarrierName,
		Server:                           r.EntitlementServer,
		App:                              r.App,
		AppName:                          r.AppName,
		Version:                          firstNonEmpty(r.EntitlementVersion, "2.0"),
		UsePost:                          r.UseHTTPPost,
		ClientTS43:                       firstNonEmpty(r.ClientTS43, DefaultWFCClientTS43),
		DefaultServiceEntitlementEnabled: r.DefaultServiceEntitlementEnabled,
		SkipActivation:                   r.SkipActivation,
		ShowVoWiFiWebview:                r.ShowVoWiFiWebview,
	}
}

type WFCStatus struct {
	EntitlementStatus   WFCEntitlementStatus
	TCStatus            WFCTCStatus
	AddrStatus          WFCAddressStatus
	ProvStatus          WFCProvisionStatus
	ServiceFlowURL      string
	ServiceFlowUserData string
}

func (s WFCStatus) Entitled() bool {
	return s.EntitlementStatus == WFCEntitlementEnabled &&
		(s.ProvStatus == WFCProvisionProvisioned || s.ProvStatus == WFCProvisionNotRequired) &&
		(s.TCStatus == WFCTCAvailable || s.TCStatus == WFCTCNotRequired) &&
		(s.AddrStatus == WFCAddressAvailable || s.AddrStatus == WFCAddressNotRequired)
}

func (s WFCStatus) NeedsUserWebFlow() bool {
	return s.EntitlementStatus == WFCEntitlementDisabled &&
		(s.TCStatus == WFCTCNotAvailable || s.AddrStatus == WFCAddressNotAvailable)
}

func (s WFCStatus) InProgress() bool {
	return s.EntitlementStatus == WFCEntitlementProvisioning ||
		(s.EntitlementStatus == WFCEntitlementDisabled &&
			(s.TCStatus == WFCTCInProgress || s.AddrStatus == WFCAddressInProgress)) ||
		(s.EntitlementStatus == WFCEntitlementDisabled &&
			(s.ProvStatus == WFCProvisionNotProvisioned || s.ProvStatus == WFCProvisionInProgress) &&
			(s.TCStatus == WFCTCAvailable || s.TCStatus == WFCTCNotRequired) &&
			(s.AddrStatus == WFCAddressAvailable || s.AddrStatus == WFCAddressNotRequired))
}

func (s WFCStatus) Incompatible() bool {
	return s.EntitlementStatus == WFCEntitlementIncompatible
}

func CheckWFCEntitlement(ctx context.Context, cfg Config, identity sim.Identity, device Device, wfc WFCConfig) (WFCStatus, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Entitlement.Timeout == 0 {
		cfg.Entitlement.Timeout = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = ts43http.NewHTTPClient(cfg.Logger, cfg.Entitlement.Timeout)
	}
	client := &Client{cfg: cfg}
	return client.CheckWFCEntitlement(ctx, identity, device, wfc)
}

func (c *Client) CheckWFCEntitlement(ctx context.Context, identity sim.Identity, device Device, cfg WFCConfig) (WFCStatus, error) {
	if cfg.Server == "" {
		return WFCStatus{}, errors.New("wfc entitlement server is required")
	}
	next := *c
	next.cfg.Entitlement = Entitlement{
		Server:  cfg.Server,
		UsePost: cfg.UsePost,
		Timeout: c.cfg.Entitlement.Timeout,
	}
	req := ServiceEntitlementRequest{
		EntitlementVersion: firstNonEmpty(cfg.Version, "2.0"),
		App:                firstNonEmpty(cfg.App, AppVoWiFi),
		TerminalID:         device.IMEI,
		TerminalVendor:     device.Vendor,
		TerminalModel:      device.Model,
		TerminalSoftware:   device.SoftwareVersion,
		AppName:            cfg.AppName,
		Accept:             DefaultResponseAccept,
		ClientTS43:         firstNonEmpty(cfg.ClientTS43, DefaultWFCClientTS43),
	}
	body, _, err := next.callRaw(ctx, identity, req, ODSAOperation{})
	if err != nil {
		return WFCStatus{}, err
	}
	var status WFCStatus
	emptyBody := len(bytes.TrimSpace(body)) == 0
	if !emptyBody {
		if err := status.UnmarshalBinary(body); err != nil {
			return WFCStatus{}, err
		}
	}
	if emptyBody && cfg.DefaultServiceEntitlementEnabled {
		return WFCStatus{
			EntitlementStatus: WFCEntitlementEnabled,
			TCStatus:          WFCTCNotRequired,
			AddrStatus:        WFCAddressNotRequired,
			ProvStatus:        WFCProvisionNotRequired,
		}, nil
	}
	if emptyBody {
		return WFCStatus{
			EntitlementStatus: WFCEntitlementIncompatible,
			TCStatus:          WFCTCNotAvailable,
			AddrStatus:        WFCAddressNotAvailable,
			ProvStatus:        WFCProvisionNotProvisioned,
		}, nil
	}
	return status, nil
}

func (s *WFCStatus) UnmarshalBinary(data []byte) error {
	if json.Valid(data) {
		return s.unmarshalJSON(data)
	}
	return s.unmarshalXML(data)
}

func (s *WFCStatus) unmarshalJSON(data []byte) error {
	var payload wfcPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	return s.apply(payload)
}

func (s *WFCStatus) unmarshalXML(data []byte) error {
	var payload wfcPayload
	if err := xml.Unmarshal(data, &payload); err != nil {
		return err
	}
	return s.apply(payload)
}

func (s *WFCStatus) apply(payload wfcPayload) error {
	status, err := payload.status()
	if err != nil {
		return err
	}
	*s = status
	return nil
}

type wfcPayload struct {
	Application *wfcFields
	Root        wfcFields
}

func (p *wfcPayload) UnmarshalJSON(data []byte) error {
	var named struct {
		Application *wfcFields `json:"APPLICATION"`
	}
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	var root wfcFields
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	p.Application = named.Application
	p.Root = root
	return nil
}

func (p *wfcPayload) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	var first *wfcFields
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "characteristic" || characteristicType(t) != "APPLICATION" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var app wfcFields
			if err := decoder.DecodeElement(&app, &t); err != nil {
				return err
			}
			if first == nil {
				first = &app
			}
			if app.AppID.String() == AppVoWiFi {
				p.Application = &app
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if p.Application == nil {
					p.Application = first
				}
				return nil
			}
		}
	}
}

func (p wfcPayload) status() (WFCStatus, error) {
	fields := p.Application
	if fields == nil {
		fields = &p.Root
	}
	if fields.empty() {
		return WFCStatus{}, nil
	}
	return fields.status()
}

type wfcFields struct {
	AppID               stringField `json:"AppID"`
	EntitlementStatus   stringField `json:"EntitlementStatus"`
	TCStatus            stringField `json:"TC_Status"`
	AddrStatus          stringField `json:"AddrStatus"`
	ProvStatus          stringField `json:"ProvStatus"`
	ServiceFlowURL      stringField `json:"ServiceFlow_URL"`
	ServiceFlowUserData stringField `json:"ServiceFlow_UserData"`
}

func (f *wfcFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "parm" && t.Name.Local != "param" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var param xmlParam
			if err := decoder.DecodeElement(&param, &t); err != nil {
				return err
			}
			f.set(param.Name, param.Value)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *wfcFields) set(name, value string) {
	switch name {
	case "AppID":
		f.AppID = stringField(value)
	case "EntitlementStatus":
		f.EntitlementStatus = stringField(value)
	case "TC_Status":
		f.TCStatus = stringField(value)
	case "AddrStatus":
		f.AddrStatus = stringField(value)
	case "ProvStatus":
		f.ProvStatus = stringField(value)
	case "ServiceFlow_URL":
		f.ServiceFlowURL = stringField(value)
	case "ServiceFlow_UserData":
		f.ServiceFlowUserData = stringField(value)
	}
}

func (f wfcFields) empty() bool {
	return f.AppID == "" &&
		f.EntitlementStatus == "" &&
		f.TCStatus == "" &&
		f.AddrStatus == "" &&
		f.ProvStatus == "" &&
		f.ServiceFlowURL == "" &&
		f.ServiceFlowUserData == ""
}

func (f wfcFields) status() (WFCStatus, error) {
	var entitlementStatus WFCEntitlementStatus
	if err := entitlementStatus.UnmarshalText([]byte(f.EntitlementStatus)); err != nil {
		return WFCStatus{}, err
	}
	var tcStatus WFCTCStatus
	if err := tcStatus.UnmarshalText([]byte(f.TCStatus)); err != nil {
		return WFCStatus{}, err
	}
	var addrStatus WFCAddressStatus
	if err := addrStatus.UnmarshalText([]byte(f.AddrStatus)); err != nil {
		return WFCStatus{}, err
	}
	var provStatus WFCProvisionStatus
	if err := provStatus.UnmarshalText([]byte(f.ProvStatus)); err != nil {
		return WFCStatus{}, err
	}
	return WFCStatus{
		EntitlementStatus:   entitlementStatus,
		TCStatus:            tcStatus,
		AddrStatus:          addrStatus,
		ProvStatus:          provStatus,
		ServiceFlowURL:      f.ServiceFlowURL.String(),
		ServiceFlowUserData: f.ServiceFlowUserData.String(),
	}, nil
}
