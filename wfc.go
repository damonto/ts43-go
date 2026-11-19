package ts43

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
)

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
	// NeedsIMSProvisioning queries additional IMS services and applies carrier VERS state.
	NeedsIMSProvisioning bool
	SkipActivation       bool
	ShowVoWiFiWebview    bool
	WebsheetClientName   string
}

type WFCResult struct {
	Status        WFCStatus
	AuthToken     AuthToken
	Configuration WFCConfiguration
	// ConfigurationReset reports a VERS reset or an authentication reset that
	// invalidates cached state even if the full authentication retry fails.
	ConfigurationReset bool
	Cancelled          bool
	request            *WFCEntitlementRequest
}

type WFCEntitlementRequest struct {
	Identity  Identity
	Device    Device
	Config    WFCConfig
	AuthToken AuthToken
	// Configuration is the VERS state from the last check for this SIM and server.
	// It is used when Config.NeedsIMSProvisioning is enabled.
	Configuration WFCConfiguration
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
	NeedsIMSProvisioning             bool    `json:"needsIMSProvisioning,omitempty"`
	SkipActivation                   bool    `json:"skipActivation"`
	ShowVoWiFiWebview                bool    `json:"showVoWiFiWebview"`
	WebsheetClientName               string  `json:"websheetClientName"`
}

// Discover selects the Wi-Fi Calling configuration for the SIM identity.
func (c *WFCConfigs) Discover(identity Identity) (WFCConfig, error) {
	if identity.MCC == "" || identity.MNC == "" {
		return WFCConfig{}, errors.New("source MCC/MNC is required")
	}
	var records []wfcRecord
	if c != nil {
		records = c.records
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
		NeedsIMSProvisioning:             r.NeedsIMSProvisioning,
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

// CheckWFCEntitlement queries the carrier using cfg's HTTP transport and source SIM.
func CheckWFCEntitlement(ctx context.Context, cfg Config, req WFCEntitlementRequest) (WFCResult, error) {
	client := newClient(cfg)
	return client.CheckWFCEntitlement(ctx, req)
}

// CheckWFCEntitlement queries the current Wi-Fi Calling entitlement for req.
// A result that reports ConfigurationReset invalidates the previous credentials
// even if the check also returns an error.
func (c *Client) CheckWFCEntitlement(ctx context.Context, req WFCEntitlementRequest) (WFCResult, error) {
	if !authCredential(req.AuthToken).validAt(c.now()) {
		req.AuthToken = AuthToken{}
	}
	result, err := c.checkWFCEntitlementOnce(ctx, req)
	if (req.AuthToken.Value != "" || req.Config.NeedsIMSProvisioning) &&
		isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) {
		req.AuthToken = AuthToken{}
		req.Configuration = WFCConfiguration{}
		result, err = c.checkWFCEntitlementOnce(ctx, req)
		if err != nil {
			result.ConfigurationReset = true
		}
	}
	if err != nil {
		return result, err
	}
	req.AuthToken = result.AuthToken
	req.AuthToken.Cookies = slices.Clone(result.AuthToken.Cookies)
	req.Configuration = result.Configuration
	result.request = &req
	return result, nil
}

// ContinueWFC rechecks entitlement after portal completion. On cancellation or
// recheck failure it returns the previous status, with any rejected credentials
// removed. The caller owns the device's WFC setting.
func (c *Client) ContinueWFC(ctx context.Context, previous WFCResult, callback WebsheetResult) (WFCResult, error) {
	if previous.request == nil {
		return previous, errors.New("WFC request is required")
	}
	switch callback.Event {
	case WebsheetEventVoWiFiDismissFlow:
		previous.Cancelled = true
		return previous, nil
	case WebsheetEventVoWiFiEntitlementChanged:
		result, err := c.CheckWFCEntitlement(ctx, *previous.request)
		if err == nil {
			return result, nil
		}
		if result.ConfigurationReset {
			req := *previous.request
			req.AuthToken = AuthToken{}
			req.Configuration = WFCConfiguration{}
			previous.request = &req
			previous.AuthToken = AuthToken{}
			previous.Configuration = WFCConfiguration{}
			previous.ConfigurationReset = true
		}
		return previous, err
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
	var additionalApps []string
	if cfg.NeedsIMSProvisioning {
		req.RequestVersion = strconv.Itoa(input.Configuration.requestVersion())
		additionalApps = []string{appVoLTE, appSMSOIP}
	}
	transport := entitlementTransport{client: &next, channel: next.transfer.source.Channel}
	body, cookies, err := transport.callRaw(ctx, input.Identity, req, ODSAOperation{}, additionalApps...)
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
		result.Status = WFCStatus{EntitlementStatus: WFCEntitlementIncompatible}
		if cfg.DefaultServiceEntitlementEnabled {
			result.Status = WFCStatus{
				EntitlementStatus: WFCEntitlementEnabled,
				TCStatus:          WFCTCNotRequired,
				AddrStatus:        WFCAddressNotRequired,
				ProvStatus:        WFCProvisionNotRequired,
			}
		}
		// A VERS reset also invalidates cached authentication credentials.
		return result, nil
	}
	result.Status, err = payload.status()
	if err != nil {
		return WFCResult{}, err
	}
	token.Cookies = cookies
	result.AuthToken, err = payload.authToken(token, next.now())
	if err != nil {
		return WFCResult{}, err
	}
	return result, nil
}
