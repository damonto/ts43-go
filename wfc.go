package ts43

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
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
	// NeedsIMSProvisioning enables VERS-driven resets to carrier defaults.
	NeedsIMSProvisioning bool
	SkipActivation       bool
	ShowVoWiFiWebview    bool
	WebsheetClientName   string
}

type WFCResult struct {
	Status             WFCStatus
	AuthToken          AuthToken
	Configuration      WFCConfiguration
	ConfigurationReset bool
	Cancelled          bool
	request            *WFCEntitlementRequest
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
	EntitlementStatus      WFCEntitlementStatus
	TCStatus               WFCTCStatus
	AddrStatus             WFCAddressStatus
	ProvStatus             WFCProvisionStatus
	ServiceFlowURL         string
	ServiceFlowUserData    string
	MessageForIncompatible string
	AddrExpiry             string
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
	if s.EntitlementStatus == WFCEntitlementProvisioning {
		return WFCActivationInProgress
	}
	if s.EntitlementStatus == WFCEntitlementDisabled {
		if s.TCStatus == WFCTCNotAvailable || s.AddrStatus == WFCAddressNotAvailable {
			return WFCActivationWebView
		}
		if s.TCStatus == WFCTCInProgress ||
			s.AddrStatus == WFCAddressInProgress ||
			s.ProvStatus == WFCProvisionInProgress ||
			(s.ProvStatus == WFCProvisionNotProvisioned &&
				(s.TCStatus == WFCTCAvailable || s.TCStatus == WFCTCNotRequired) &&
				(s.AddrStatus == WFCAddressAvailable || s.AddrStatus == WFCAddressNotRequired)) {
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

// ShouldShowAddressUpdate allows an entitled user to update their emergency
// address even when the carrier skips initial activation.
func (c WFCConfig) ShouldShowAddressUpdate(status WFCStatus) bool {
	return c.ShowVoWiFiWebview && status.ServiceFlowURL != "" && !status.Incompatible()
}

func CheckWFCEntitlement(ctx context.Context, cfg Config, req WFCEntitlementRequest) (WFCResult, error) {
	client := newClient(cfg)
	return client.CheckWFCEntitlement(ctx, req)
}

func (c *Client) CheckWFCEntitlement(ctx context.Context, req WFCEntitlementRequest) (WFCResult, error) {
	token := req.AuthToken
	if !authCredential(token).validAt(c.now()) {
		token = AuthToken{}
	}
	req.AuthToken = token
	result, err := c.checkWFCEntitlementOnce(ctx, req)
	if token.Value != "" && isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) {
		req.AuthToken = AuthToken{}
		result, err = c.checkWFCEntitlementOnce(ctx, req)
	}
	if err != nil {
		return result, err
	}
	req.AuthToken = result.AuthToken
	result.request = &req
	return result, nil
}

// ContinueWFC rechecks entitlement after portal completion. Cancellation keeps
// the previous entitlement state; changing the device's WFC setting is up to the caller.
func (c *Client) ContinueWFC(ctx context.Context, previous WFCResult, callback WebsheetResult) (WFCResult, error) {
	if previous.request == nil {
		return previous, errors.New("WFC request is required")
	}
	switch callback.Event {
	case WebsheetEventVoWiFiDismissFlow:
		previous.Cancelled = true
		return previous, nil
	case WebsheetEventVoWiFiEntitlementChanged:
		return c.CheckWFCEntitlement(ctx, *previous.request)
	default:
		return previous, errors.New("WFC portal callback is required")
	}
}

func (c *Client) checkWFCEntitlementOnce(ctx context.Context, input WFCEntitlementRequest) (WFCResult, error) {
	cfg, device, token := input.Config, input.Device, input.AuthToken
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
	body, cookies, err := next.callRaw(ctx, input.Identity, req, ODSAOperation{})
	if err != nil {
		return WFCResult{}, err
	}
	var payload wfcPayload
	if err := payload.UnmarshalBinary(body); err != nil {
		return WFCResult{}, err
	}
	result := WFCResult{Configuration: payload.Configuration}
	if cfg.NeedsIMSProvisioning && payload.Configuration.resetToDefault() {
		result.ConfigurationReset = true
		result.Status = WFCStatus{EntitlementStatus: WFCEntitlementDisabled, TCStatus: WFCTCNotRequired, AddrStatus: WFCAddressNotRequired, ProvStatus: WFCProvisionNotRequired}
		if cfg.DefaultServiceEntitlementEnabled {
			result.Status.EntitlementStatus = WFCEntitlementEnabled
		}
		// A VERS reset also invalidates cached authentication credentials.
		return result, nil
	}
	result.Status, err = payload.status()
	if err != nil {
		return WFCResult{}, err
	}
	token.Cookies = mergeCookieHeaders(token.Cookies, cookies)
	result.AuthToken, err = payload.authToken(token, next.now())
	if err != nil {
		return WFCResult{}, err
	}
	return result, nil
}
