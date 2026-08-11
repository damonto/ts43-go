package ts43

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed wfc.json
var wfcJSON []byte

var (
	ErrWFCConfigNotFound  = errors.New("wfc config not found")
	ErrWFCConfigAmbiguous = errors.New("wfc config is ambiguous")
)

const (
	appVoLTE  = "ap2003"
	appSMSOIP = "ap2005"
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
	WFCActivationUnavailable   WFCActivationMode    = 0
	WFCActivationInProgress    WFCActivationMode    = 1
	WFCActivationWebView       WFCActivationMode    = 2
	WFCActivationAvailable     WFCActivationMode    = 3
)

type WFCEntitlementStatus int

func (s WFCEntitlementStatus) String() string { return strconv.Itoa(int(s)) }

func (s WFCEntitlementStatus) MarshalText() ([]byte, error) { return s.AppendText(nil) }

func (s WFCEntitlementStatus) AppendText(dst []byte) ([]byte, error) {
	return strconv.AppendInt(dst, int64(s), 10), nil
}

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

func (s WFCTCStatus) MarshalText() ([]byte, error) { return s.AppendText(nil) }

func (s WFCTCStatus) AppendText(dst []byte) ([]byte, error) {
	return strconv.AppendInt(dst, int64(s), 10), nil
}

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

func (s WFCAddressStatus) MarshalText() ([]byte, error) { return s.AppendText(nil) }

func (s WFCAddressStatus) AppendText(dst []byte) ([]byte, error) {
	return strconv.AppendInt(dst, int64(s), 10), nil
}

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

func (s WFCProvisionStatus) MarshalText() ([]byte, error) { return s.AppendText(nil) }

func (s WFCProvisionStatus) AppendText(dst []byte) ([]byte, error) {
	return strconv.AppendInt(dst, int64(s), 10), nil
}

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

type WFCActivationMode int

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
	WebsheetClientName               string
}

type WFCResult struct {
	Status    WFCStatus
	AuthToken AuthToken
}

type WFCEntitlementRequest struct {
	Identity  Identity
	Device    Device
	Config    WFCConfig
	AuthToken AuthToken
}

type wfcRecord struct {
	MCCMNC                           string  `json:"mccmnc"`
	GID1                             *string `json:"gid1"`
	GID2                             *string `json:"gid2"`
	SPN                              *string `json:"spn"`
	IMSIPrefixXPattern               *string `json:"imsiPrefixXpattern"`
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
	WebsheetClientName               string  `json:"websheetClientName"`
}

func DiscoverWFC(identity Identity) (WFCConfig, error) {
	if identity.MCC == "" || identity.MNC == "" {
		return WFCConfig{}, errors.New("source MCC/MNC is required")
	}
	var records []wfcRecord
	if err := json.Unmarshal(wfcJSON, &records); err != nil {
		return WFCConfig{}, fmt.Errorf("parse WFC config: %w", err)
	}
	record, err := selectWFCRecord(records, identity)
	if err != nil {
		return WFCConfig{}, err
	}
	return record.config(), nil
}

func selectWFCRecord(records []wfcRecord, identity Identity) (wfcRecord, error) {
	var matches []wfcRecord
	bestScore := -1
	for _, record := range records {
		score, ok := record.match().score(identity)
		if !ok || score < bestScore {
			continue
		}
		if score > bestScore {
			matches = matches[:0]
			bestScore = score
		}
		if slices.ContainsFunc(matches, func(match wfcRecord) bool {
			return match.CarrierName == record.CarrierName
		}) {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return wfcRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrWFCConfigAmbiguous, mccMNC(identity))
	}
	return wfcRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrWFCConfigNotFound, mccMNC(identity))
}

func (r wfcRecord) match() carrierMatch {
	return carrierMatch{
		MCCMNC:             r.MCCMNC,
		GID1:               r.GID1,
		GID2:               r.GID2,
		SPN:                r.SPN,
		IMSIPrefixXPattern: r.IMSIPrefixXPattern,
	}
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
		WebsheetClientName:               r.WebsheetClientName,
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
	return s.ActivationMode() == WFCActivationAvailable
}

func (s WFCStatus) NeedsUserWebFlow() bool {
	return s.ActivationMode() == WFCActivationWebView
}

func (s WFCStatus) InProgress() bool {
	return s.ActivationMode() == WFCActivationInProgress
}

func (s WFCStatus) Incompatible() bool {
	return s.EntitlementStatus == WFCEntitlementIncompatible
}

func (s WFCStatus) ActivationMode() WFCActivationMode {
	if s.EntitlementStatus == WFCEntitlementIncompatible {
		return WFCActivationUnavailable
	}
	if s.EntitlementStatus == WFCEntitlementDisabled {
		if s.TCStatus == WFCTCNotAvailable || s.AddrStatus == WFCAddressNotAvailable {
			return WFCActivationWebView
		}
		if s.TCStatus == WFCTCInProgress ||
			s.AddrStatus == WFCAddressInProgress ||
			s.ProvStatus == WFCProvisionInProgress {
			return WFCActivationInProgress
		}
		return WFCActivationUnavailable
	}
	if s.EntitlementStatus == WFCEntitlementEnabled &&
		(s.ProvStatus == WFCProvisionProvisioned || s.ProvStatus == WFCProvisionNotRequired) &&
		(s.TCStatus == WFCTCAvailable || s.TCStatus == WFCTCNotRequired) &&
		(s.AddrStatus == WFCAddressAvailable || s.AddrStatus == WFCAddressNotRequired) {
		return WFCActivationAvailable
	}
	return WFCActivationUnavailable
}

func (c WFCConfig) ShouldShowWebFlow(status WFCStatus) bool {
	if c.SkipActivation || !c.ShowVoWiFiWebview {
		return false
	}
	return status.NeedsUserWebFlow()
}

func CheckWFCEntitlement(ctx context.Context, cfg Config, req WFCEntitlementRequest) (WFCResult, error) {
	client := newClient(cfg)
	return client.CheckWFCEntitlement(ctx, req)
}

func (c *Client) CheckWFCEntitlement(ctx context.Context, req WFCEntitlementRequest) (WFCResult, error) {
	if req.AuthToken.Value != "" {
		result, err := c.checkWFCEntitlementOnce(ctx, req.Identity, req.Device, req.Config, req.AuthToken)
		if !isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) {
			return result, err
		}
	}
	return c.checkWFCEntitlementOnce(ctx, req.Identity, req.Device, req.Config, AuthToken{})
}

func (c *Client) checkWFCEntitlementOnce(ctx context.Context, identity Identity, device Device, cfg WFCConfig, token AuthToken) (WFCResult, error) {
	if cfg.Server == "" {
		return WFCResult{}, errors.New("wfc entitlement server is required")
	}
	next := *c
	next.transfer.entitlement = Entitlement{
		Server:  cfg.Server,
		UsePost: cfg.UsePost,
		Timeout: c.transfer.entitlement.Timeout,
	}
	req := ServiceEntitlementRequest{
		EntitlementVersion:  firstNonEmpty(cfg.Version, "2.0"),
		App:                 firstNonEmpty(cfg.App, AppVoWiFi),
		TerminalID:          device.IMEI,
		TerminalVendor:      device.Vendor,
		TerminalModel:       device.Model,
		TerminalSoftware:    device.SoftwareVersion,
		AppName:             cfg.AppName,
		Accept:              WFCResponseAccept,
		ClientTS43:          firstNonEmpty(cfg.ClientTS43, DefaultWFCClientTS43),
		AuthenticationToken: token.Value,
		Cookies:             token.Cookies,
	}
	body, cookies, err := next.callRaw(ctx, identity, req, ODSAOperation{})
	if err != nil {
		return WFCResult{}, err
	}
	var status WFCStatus
	emptyBody := len(bytes.TrimSpace(body)) == 0
	if !emptyBody {
		if err := status.UnmarshalBinary(body); err != nil {
			return WFCResult{}, err
		}
	}
	nextToken := token
	if parsed, err := wfcAuthTokenFromBody(body, token, cookies, next.now()); err == nil {
		nextToken = parsed
	}
	if emptyBody && cfg.DefaultServiceEntitlementEnabled {
		return WFCResult{Status: WFCStatus{
			EntitlementStatus: WFCEntitlementEnabled,
			TCStatus:          WFCTCNotRequired,
			AddrStatus:        WFCAddressNotRequired,
			ProvStatus:        WFCProvisionNotRequired,
		}, AuthToken: nextToken}, nil
	}
	if emptyBody {
		return WFCResult{Status: WFCStatus{
			EntitlementStatus: WFCEntitlementIncompatible,
			TCStatus:          WFCTCNotAvailable,
			AddrStatus:        WFCAddressNotAvailable,
			ProvStatus:        WFCProvisionNotProvisioned,
		}, AuthToken: nextToken}, nil
	}
	return WFCResult{Status: status, AuthToken: nextToken}, nil
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
		escaped, ok := escapeBareXMLAmpersands(data)
		if !ok {
			return err
		}
		if retryErr := xml.Unmarshal(escaped, &payload); retryErr != nil {
			return err
		}
	}
	return s.apply(payload)
}

func escapeBareXMLAmpersands(data []byte) ([]byte, bool) {
	var out []byte
	changed := false
	for i := range data {
		if data[i] != '&' {
			out = append(out, data[i])
			continue
		}
		if validXMLReference(data[i:]) {
			out = append(out, data[i])
			continue
		}
		out = append(out, '&', 'a', 'm', 'p', ';')
		changed = true
	}
	return out, changed
}

func validXMLReference(data []byte) bool {
	for _, ref := range [...]string{"&amp;", "&lt;", "&gt;", "&apos;", "&quot;"} {
		if bytes.HasPrefix(data, []byte(ref)) {
			return true
		}
	}
	if len(data) < 4 || data[1] != '#' {
		return false
	}
	i := 2
	if data[i] == 'x' || data[i] == 'X' {
		i++
		start := i
		for i < len(data) && isXMLHexDigit(data[i]) {
			i++
		}
		return i > start && i < len(data) && data[i] == ';'
	}
	start := i
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		i++
	}
	return i > start && i < len(data) && data[i] == ';'
}

func isXMLHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
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
	var root wfcFields
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}

	var fieldsByName map[string]json.RawMessage
	if err := json.Unmarshal(data, &fieldsByName); err != nil {
		return err
	}
	var vowifi, generic, volte, smsoip wfcFields
	var hasVoWiFi, hasGeneric, hasVoLTE, hasSMSOIP bool
	for name, raw := range fieldsByName {
		appID, ok := wfcJSONAppID(name)
		if !ok {
			continue
		}
		app, err := decodeWFCJSONFields(raw, appID)
		if err != nil {
			return err
		}
		if app.AppID.String() == AppVoWiFi {
			vowifi = app
			hasVoWiFi = true
			continue
		}
		switch app.AppID.String() {
		case appVoLTE:
			volte = app
			hasVoLTE = true
		case appSMSOIP:
			smsoip = app
			hasSMSOIP = true
		default:
			generic = app
			hasGeneric = true
		}
	}
	switch {
	case hasVoWiFi:
		p.Application = &vowifi
	case hasGeneric:
		p.Application = &generic
	case hasVoLTE:
		p.Application = &volte
	case hasSMSOIP:
		p.Application = &smsoip
	}
	p.Root = root
	return nil
}

func wfcJSONAppID(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, "APPLICATION"):
		return "", true
	case strings.EqualFold(name, AppVoWiFi):
		return AppVoWiFi, true
	case strings.EqualFold(name, appVoLTE):
		return appVoLTE, true
	case strings.EqualFold(name, appSMSOIP):
		return appSMSOIP, true
	default:
		return "", false
	}
}

func decodeWFCJSONFields(data []byte, appID string) (wfcFields, error) {
	var fields wfcFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return wfcFields{}, err
	}
	if fields.AppID == "" && appID != "" {
		fields.AppID = stringField(appID)
	}
	return fields, nil
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
			if t.Name.Local != "characteristic" || !strings.EqualFold(characteristicType(t), "APPLICATION") {
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
	required            uint8
}

const (
	wfcFieldEntitlement uint8 = 1 << iota
	wfcFieldTC
	wfcFieldAddr
	wfcFieldProv
)

func (f *wfcFields) UnmarshalJSON(data []byte) error {
	var fields map[string]stringField
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		f.set(name, value.String())
	}
	return nil
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
	switch normalizeWFCFieldName(name) {
	case "appid":
		f.AppID = stringField(value)
	case "entitlementstatus":
		f.EntitlementStatus = stringField(value)
		f.required |= wfcFieldEntitlement
	case "tcstatus":
		f.TCStatus = stringField(value)
		f.required |= wfcFieldTC
	case "addrstatus":
		f.AddrStatus = stringField(value)
		f.required |= wfcFieldAddr
	case "provstatus":
		f.ProvStatus = stringField(value)
		f.required |= wfcFieldProv
	case "serviceflowurl":
		f.ServiceFlowURL = stringField(value)
	case "serviceflowuserdata":
		f.ServiceFlowUserData = stringField(value)
	}
}

func normalizeWFCFieldName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
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
	if err := f.requireStatusFields(); err != nil {
		return WFCStatus{}, err
	}
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

func (f wfcFields) requireStatusFields() error {
	var missing []string
	if f.required&wfcFieldEntitlement == 0 || f.EntitlementStatus.String() == "" {
		missing = append(missing, "EntitlementStatus")
	}
	if f.required&wfcFieldTC == 0 || f.TCStatus.String() == "" {
		missing = append(missing, "TC_Status")
	}
	if f.required&wfcFieldAddr == 0 || f.AddrStatus.String() == "" {
		missing = append(missing, "AddrStatus")
	}
	if f.required&wfcFieldProv == 0 || f.ProvStatus.String() == "" {
		missing = append(missing, "ProvStatus")
	}
	if len(missing) > 0 {
		return fmt.Errorf("wfc status missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func wfcAuthTokenFromBody(body []byte, current AuthToken, cookies []string, now time.Time) (AuthToken, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return current, nil
	}
	var payload odsaPayload
	var err error
	if json.Valid(body) {
		err = json.Unmarshal(body, &payload)
	} else {
		err = xml.Unmarshal(body, &payload)
	}
	if err != nil {
		return current, err
	}
	if payload.Token == nil || payload.Token.Token == "" {
		return current, nil
	}
	next := current
	next.Value = payload.Token.Token.String()
	next.Cookies = mergeCookieHeaders(current.Cookies, cookies)
	validity, err := wfcAuthTokenValidity(payload.Token.Validity.String())
	if err != nil {
		return current, err
	}
	next.ExpiresAt = now.Add(time.Duration(validity) * time.Second)
	return next, nil
}

func wfcAuthTokenValidity(value string) (int, error) {
	validity, err := parseOptionalInt(value)
	if err != nil {
		return 0, fmt.Errorf("parse WFC authentication token validity: %w", err)
	}
	if validity.value > 0 {
		return validity.value, nil
	}
	return 3600, nil
}
