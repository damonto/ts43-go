package ts43

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Client struct {
	runtime  clientRuntime
	transfer transferDefaults
}

type clientRuntime struct {
	logger             *slog.Logger
	httpClient         HTTPDoer
	defaultHTTPClient  bool
	explicitTimeout    bool
	explicitRetryLimit bool
	events             chan<- Event
	now                func() time.Time
	sleep              func(context.Context, time.Duration) error
}

type transferDefaults struct {
	entitlements *EntitlementConfigs
	entitlement  Entitlement
	source       Endpoint
	target       Endpoint
	companion    Device
	appVersion   string
	clientTS43   string
	realm        string
}

// TransferTokenResult carries the source auth token and, for transfer types
// 2/9/11, the temporary token issued for target-side transfer operations.
type TransferTokenResult struct {
	Auth      AuthToken
	Temporary TemporaryToken
}

type transferCredential struct {
	auth           AuthToken
	temporary      TemporaryToken
	temporaryToken bool
}

type eapRelayPacket struct {
	Packet string `json:"eap-relay-packet"`
}

type entitlementTransport struct {
	client  *Client
	channel Channel
}

const (
	defaultEntitlementTimeout = 30 * time.Second
	maxEAPRelayRounds         = 4
)

func New(cfg *Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Source.Device.IMEI == "" {
		return nil, errors.New("source IMEI is required")
	}
	if cfg.Target.Device.IMEI == "" {
		return nil, errors.New("target IMEI is required")
	}
	if cfg.Target.Device.EID == "" {
		return nil, errors.New("target EID is required")
	}
	return newClient(*cfg), nil
}

func newClient(cfg Config) *Client {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	explicitTimeout := cfg.Entitlement.Timeout != 0
	explicitRetryLimit := cfg.Entitlement.AutoRetry511Limit != 0
	cfg.Entitlement.TransferType = selectedTransferType(cfg.Entitlement)
	if cfg.Entitlement.Timeout == 0 {
		cfg.Entitlement.Timeout = defaultEntitlementTimeout
	}
	cfg.HTTPClient = withTS43Transport(cfg.Logger, cfg.HTTPClient)
	defaultHTTPClient := cfg.HTTPClient == nil
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = NewHTTPClient(cfg.Logger, cfg.Entitlement.Timeout)
	}
	if cfg.Entitlement.PollInterval == 0 {
		cfg.Entitlement.PollInterval = time.Minute
	}
	if cfg.Entitlement.PollTimeout == 0 {
		cfg.Entitlement.PollTimeout = 10 * time.Minute
	}
	if cfg.Entitlement.AutoRetry511Limit == 0 {
		cfg.Entitlement.AutoRetry511Limit = 1
	}
	return &Client{
		runtime: clientRuntime{
			logger:             cfg.Logger,
			httpClient:         cfg.HTTPClient,
			defaultHTTPClient:  defaultHTTPClient,
			explicitTimeout:    explicitTimeout,
			explicitRetryLimit: explicitRetryLimit,
			events:             cfg.Events,
			now:                cfg.Now,
			sleep:              cfg.Sleep,
		},
		transfer: transferDefaults{
			entitlement:  cfg.Entitlement,
			entitlements: cfg.Entitlements,
			source:       cfg.Source,
			target:       cfg.Target,
			companion:    cfg.Companion,
			appVersion:   cfg.AppVersion,
			clientTS43:   cfg.ClientTS43,
			realm:        cfg.Realm,
		},
	}
}

// Authenticate obtains an authentication token, retrying HTTP 511 responses up
// to Entitlement.AutoRetry511Limit times.
func (c *Client) Authenticate(ctx context.Context, identity Identity, device Device) (AuthToken, error) {
	for attempt := 0; ; attempt++ {
		token, err := c.authenticate(ctx, c.transfer.source.Channel, identity, device)
		if !isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) || attempt >= c.transfer.entitlement.AutoRetry511Limit {
			return token, err
		}
	}
}

// authenticate performs one authentication attempt. Transfer flows apply their
// retry budget around authentication, eligibility, and temporary-token issuance.
func (c *Client) authenticate(ctx context.Context, channel Channel, identity Identity, device Device) (AuthToken, error) {
	req := c.baseRequest(device, "", "")
	op := ODSAOperation{}
	resp, cookies, err := c.callWithChannel(ctx, channel, identity, req, op)
	if err != nil {
		return AuthToken{}, err
	}
	if resp.AuthenticationToken == "" {
		return AuthToken{}, errors.New("authentication token is missing")
	}
	expires := c.now().Add(time.Duration(resp.AuthTokenValidity) * time.Second)
	return AuthToken{Value: resp.AuthenticationToken, ExpiresAt: expires, Cookies: cookies}, nil
}

func (c *Client) AcquireTemporaryToken(ctx context.Context, req SubscriptionTransferRequest, auth AuthToken) (TransferTokenResult, error) {
	return c.acquireTemporaryToken(ctx, c.transfer.source.Channel, req.Identity, req.Source, auth)
}

func (c *Client) acquireTemporaryToken(ctx context.Context, channel Channel, identity Identity, source Device, auth AuthToken) (TransferTokenResult, error) {
	req := c.authRequest(source, auth)
	op := temporaryTokenOperation()
	c.addOptionalOperationFields(&op)
	resp, cookies, err := c.callWithChannel(ctx, channel, identity, req, op)
	if err != nil {
		return TransferTokenResult{}, err
	}
	if resp.OperationResult != OperationResultUnknown && resp.OperationResult != OperationResultSuccess {
		return TransferTokenResult{}, &CarrierDeniedError{Operation: OperationTempToken, Result: resp}
	}
	if resp.TemporaryToken == "" {
		return TransferTokenResult{}, errors.New("temporary token is missing")
	}
	if !slices.Contains(resp.OperationTargets, OperationManage) || !slices.Contains(resp.OperationTargets, OperationAcquireConf) {
		return TransferTokenResult{}, errors.New("temporary token does not allow transfer operations")
	}
	expiresAt, err := temporaryTokenExpiresAt(resp.TemporaryTokenExpiry)
	if err != nil {
		return TransferTokenResult{}, err
	}
	return TransferTokenResult{
		Auth: authTokenFromResponse(auth, resp, cookies, c.now()),
		Temporary: TemporaryToken{
			Value:     resp.TemporaryToken,
			ExpiresAt: expiresAt,
			Cookies:   cookies,
			Targets:   append([]Operation(nil), resp.OperationTargets...),
		},
	}, nil
}

func (c *Client) CheckEligibility(ctx context.Context, req SubscriptionTransferRequest, auth AuthToken) (AuthToken, error) {
	return c.checkEligibility(ctx, c.transfer.source.Channel, req.Identity, req.Source, req.Target, auth)
}

func (c *Client) checkEligibility(ctx context.Context, channel Channel, identity Identity, source, target Device, auth AuthToken) (AuthToken, error) {
	req := c.authRequest(source, auth)
	op := checkEligibilityOperation()
	if c.transfer.entitlement.SetCheckEligibilityTargetTerminalID || sendAllTargetTerminalIDs(c.transfer.entitlement) {
		op.TargetTerminalID = targetTerminalID(c.transfer.entitlement, target)
	}
	c.addOptionalOperationFields(&op)
	resp, cookies, err := c.callWithChannel(ctx, channel, identity, req, op)
	if err != nil {
		return AuthToken{}, err
	}
	if resp.OperationResult != OperationResultSuccess || resp.PrimaryAppEligibility != AppEligibilityEnabled {
		return AuthToken{}, &CarrierDeniedError{Operation: OperationCheck, Result: resp}
	}
	return authTokenFromResponse(auth, resp, cookies, c.now()), nil
}

func (c *Client) ManageSubscription(ctx context.Context, req ManageSubscriptionRequest, auth AuthToken) (*ODSAResponse, error) {
	return c.manageSubscription(ctx, req.Identity, req.Source, req.Target, authCredential(auth), req.UserInput)
}

func (c *Client) ManageSubscriptionWithTemporaryToken(ctx context.Context, req ManageSubscriptionRequest, token TemporaryToken) (*ODSAResponse, error) {
	return c.manageSubscription(ctx, req.Identity, req.Source, req.Target, temporaryCredential(token), req.UserInput)
}

func (c *Client) manageSubscription(ctx context.Context, identity Identity, source Device, target Device, credential transferCredential, answer UserInputResult) (*ODSAResponse, error) {
	req, op := c.manageSubscriptionRequest(ManageSubscriptionRequest{Source: source, Target: target, UserInput: answer}, credential)
	resp, _, err := c.call(ctx, identity, req, op)
	if err != nil {
		return resp, err
	}
	if !manageSubscriptionAccepted(resp) {
		return resp, &CarrierDeniedError{Operation: OperationManage, Result: resp}
	}
	return resp, nil
}

func (c *Client) manageSubscriptionRequest(input ManageSubscriptionRequest, credential transferCredential) (ServiceEntitlementRequest, ODSAOperation) {
	reqDevice := transferRequestDevice(c.transfer.entitlement, input.Source, input.Target)
	req := c.transferRequest(reqDevice, credential)
	op := manageSubscriptionOperation(c.transfer.entitlement, input)
	c.addOptionalOperationFields(&op)
	return req, op
}

func (c *Client) AcquireConfiguration(ctx context.Context, req SubscriptionTransferRequest, auth AuthToken) (*ODSAResponse, error) {
	return c.acquireConfiguration(ctx, req.Identity, req.Source, req.Target, authCredential(auth))
}

func (c *Client) AcquireConfigurationWithTemporaryToken(ctx context.Context, req SubscriptionTransferRequest, token TemporaryToken) (*ODSAResponse, error) {
	return c.acquireConfiguration(ctx, req.Identity, req.Source, req.Target, temporaryCredential(token))
}

func (c *Client) acquireConfiguration(ctx context.Context, identity Identity, source Device, target Device, credential transferCredential) (*ODSAResponse, error) {
	reqDevice := transferRequestDevice(c.transfer.entitlement, source, target)
	req := c.transferRequest(reqDevice, credential)
	op := acquireConfigurationOperation(c.transfer.entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.call(ctx, identity, req, op)
	return checkedConfiguration(resp, err)
}

func (c *Client) UpdateSubscription(ctx context.Context, req SubscriptionTransferRequest, auth AuthToken) (*ODSAResponse, error) {
	return c.updateSubscription(ctx, req.Identity, req.Source, req.Target, authCredential(auth))
}

func (c *Client) UpdateSubscriptionWithTemporaryToken(ctx context.Context, req SubscriptionTransferRequest, token TemporaryToken) (*ODSAResponse, error) {
	return c.updateSubscription(ctx, req.Identity, req.Source, req.Target, temporaryCredential(token))
}

func (c *Client) updateSubscription(ctx context.Context, identity Identity, source Device, target Device, credential transferCredential) (*ODSAResponse, error) {
	return c.updateSubscriptionOnChannel(ctx, c.transfer.source.Channel, identity, source, target, credential)
}

func (c *Client) updateSubscriptionOnChannel(ctx context.Context, channel Channel, identity Identity, source Device, target Device, credential transferCredential) (*ODSAResponse, error) {
	req := c.activationRequest(target, credential)
	op := updateSubscriptionOperation(c.transfer.entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.callWithChannel(ctx, channel, identity, req, op)
	if err != nil {
		return resp, err
	}
	if !subscriptionUpdateAccepted(resp) {
		return resp, &CarrierDeniedError{Operation: OperationManage, Result: resp}
	}
	return resp, nil
}

func (c *Client) AcquireActivationConfiguration(ctx context.Context, req ActivationConfigurationRequest, auth AuthToken) (*ODSAResponse, error) {
	return c.acquireActivationConfiguration(ctx, req.Identity, req.Target, authCredential(auth))
}

func (c *Client) AcquireActivationConfigurationWithTemporaryToken(ctx context.Context, req ActivationConfigurationRequest, token TemporaryToken) (*ODSAResponse, error) {
	return c.acquireActivationConfiguration(ctx, req.Identity, req.Target, temporaryCredential(token))
}

func (c *Client) acquireActivationConfiguration(ctx context.Context, identity Identity, target Device, credential transferCredential) (*ODSAResponse, error) {
	return c.acquireActivationConfigurationOnChannel(ctx, c.transfer.source.Channel, identity, target, credential)
}

func (c *Client) acquireActivationConfigurationOnChannel(ctx context.Context, channel Channel, identity Identity, target Device, credential transferCredential) (*ODSAResponse, error) {
	req := c.activationRequest(target, credential)
	op := activationConfigurationOperation(target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.callWithChannel(ctx, channel, identity, req, op)
	return checkedConfiguration(resp, err)
}

func checkedConfiguration(resp *ODSAResponse, err error) (*ODSAResponse, error) {
	if err != nil {
		return resp, err
	}
	// Some Google servers omit OperationResult. An explicit rejection must
	// never be interpreted as download or activation readiness.
	if resp.OperationResult != OperationResultUnknown && resp.OperationResult != OperationResultSuccess {
		return resp, &CarrierDeniedError{Operation: OperationAcquireConf, Result: resp}
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, identity Identity, req ServiceEntitlementRequest, op ODSAOperation) (*ODSAResponse, []string, error) {
	return c.callWithChannel(ctx, c.transfer.source.Channel, identity, req, op)
}

func (c *Client) callWithChannel(ctx context.Context, channel Channel, identity Identity, req ServiceEntitlementRequest, op ODSAOperation) (*ODSAResponse, []string, error) {
	body, cookies, err := c.callRawWithChannel(ctx, channel, identity, req, op)
	if err != nil {
		return nil, cookies, err
	}
	var resp ODSAResponse
	if err := resp.UnmarshalBinary(body); err != nil {
		return nil, cookies, err
	}
	resp.cookies = cookies
	return &resp, cookies, nil
}

func (c *Client) callRaw(ctx context.Context, identity Identity, req ServiceEntitlementRequest, op ODSAOperation) ([]byte, []string, error) {
	return c.callRawWithChannel(ctx, c.transfer.source.Channel, identity, req, op)
}

func (c *Client) callRawWithChannel(ctx context.Context, channel Channel, identity Identity, req ServiceEntitlementRequest, op ODSAOperation) ([]byte, []string, error) {
	return entitlementTransport{client: c, channel: channel}.callRaw(ctx, identity, req, op)
}

func (t entitlementTransport) callRaw(ctx context.Context, identity Identity, req ServiceEntitlementRequest, op ODSAOperation) ([]byte, []string, error) {
	c := t.client
	httpReq, err := buildHTTPRequest(ctx, c.transfer.entitlement, identity, req, op, c.clientTS43(), c.realm())
	if err != nil {
		return nil, nil, err
	}
	c.runtime.logger.Debug("TS.43 request", "method", httpReq.Method, "url", httpReq.URL.String(), "auth", authMode(req))
	body, cookies, err := DoRaw(ctx, t.client.runtime.httpClient, httpReq)
	if err != nil {
		return nil, cookieHeaders(cookies), err
	}
	body, cookies, err = t.eapRelay(ctx, identity, body, req, cookies)
	if err != nil {
		return nil, cookieHeaders(cookies), err
	}
	return body, cookieHeaders(cookies), nil
}

func (t entitlementTransport) eapRelay(ctx context.Context, identity Identity, body []byte, req ServiceEntitlementRequest, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	c := t.client
	serverURL, err := url.Parse(c.transfer.entitlement.Server)
	if err != nil {
		return nil, cookies, fmt.Errorf("parse entitlement URL: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, cookies, fmt.Errorf("create entitlement cookie jar: %w", err)
	}
	seed := &http.Request{Header: make(http.Header)}
	for _, header := range req.Cookies {
		seed.Header.Add("Cookie", header)
	}
	// Return the complete effective cookie set so callers do not resurrect
	// cookies deleted during authentication or a token-authenticated request.
	jar.SetCookies(serverURL, seed.Cookies())
	jar.SetCookies(serverURL, cookies)
	responseCookies := jar.Cookies(serverURL)
	for attempt := 0; ; attempt++ {
		packet := relayPacket(body)
		if packet == "" {
			return body, responseCookies, nil
		}
		if attempt == maxEAPRelayRounds {
			return nil, responseCookies, errors.New("EAP-AKA relay exhausted")
		}
		var challenge AKAChallenge
		if err := challenge.UnmarshalText([]byte(packet)); err != nil {
			return nil, responseCookies, err
		}
		if t.channel == nil {
			return nil, responseCookies, errors.New("EAP-AKA channel is required")
		}
		akaResp, err := t.channel.AuthenticateAKA(ctx, challenge.AuthenticationRequest())
		if err != nil {
			return nil, responseCookies, err
		}
		eapID, err := IMSIEAPIdentity(mccMNC(identity), identity.IMSI, c.realm())
		if err != nil {
			return nil, responseCookies, err
		}
		responseText, err := RespondToChallengeBase64(&challenge, akaResp, eapID)
		if err != nil {
			return nil, responseCookies, err
		}
		next, nextCookies, err := t.postRelay(ctx, responseText, req, responseCookies)
		jar.SetCookies(serverURL, nextCookies)
		responseCookies = jar.Cookies(serverURL)
		if err != nil {
			return nil, responseCookies, err
		}
		body = next
	}
}

func (c *Client) postRelay(ctx context.Context, packet string, req ServiceEntitlementRequest, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	return entitlementTransport{client: c}.postRelay(ctx, packet, req, cookies)
}

func (t entitlementTransport) postRelay(ctx context.Context, packet string, req ServiceEntitlementRequest, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	c := t.client
	data, err := json.Marshal(eapRelayPacket{Packet: packet})
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.transfer.entitlement.Server, bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", EAPRelayContentType)
	httpReq.Header.Set("Accept", relayResponseAccept(req))
	httpReq.Header.Set("User-Agent", userAgent(req, firstNonEmpty(req.ClientTS43, c.clientTS43())))
	for _, cookie := range cookies {
		httpReq.AddCookie(cookie)
	}
	c.runtime.logger.Debug("TS.43 EAP relay", "method", httpReq.Method, "url", httpReq.URL.String())
	return DoRaw(ctx, t.client.runtime.httpClient, httpReq)
}

func relayResponseAccept(req ServiceEntitlementRequest) string {
	if req.App == AppVoWiFi || req.ClientTS43 == DefaultWFCClientTS43 {
		return firstNonEmpty(req.Accept, WFCResponseAccept)
	}
	return eapRelayAccept(responseAccept(req))
}

func relayPacket(data []byte) string {
	var body eapRelayPacket
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	return body.Packet
}

func (c *Client) baseRequest(device Device, authToken, tempToken string) ServiceEntitlementRequest {
	return ServiceEntitlementRequest{
		EntitlementVersion:  c.entitlementVersion(),
		RequestVersion:      c.requestVersion(),
		AuthenticationToken: authToken,
		TemporaryToken:      tempToken,
		App:                 c.app(),
		TerminalID:          device.IMEI,
		TerminalVendor:      device.Vendor,
		TerminalModel:       device.Model,
		TerminalSoftware:    device.SoftwareVersion,
		AppName:             c.appName(),
		AppVersion:          c.transfer.appVersion,
		ProvisioningVersion: c.transfer.entitlement.ProvisioningVersion,
		Accept:              c.accept(),
	}
}

func (c *Client) authRequest(device Device, token AuthToken) ServiceEntitlementRequest {
	req := c.baseRequest(device, token.Value, "")
	req.Cookies = token.Cookies
	return req
}

func (c *Client) temporaryRequest(device Device, token TemporaryToken) ServiceEntitlementRequest {
	req := c.baseRequest(device, "", token.Value)
	req.Cookies = token.Cookies
	return req
}

func (c *Client) transferRequest(device Device, credential transferCredential) ServiceEntitlementRequest {
	if credential.temporaryToken {
		return c.temporaryRequest(device, credential.temporary)
	}
	return c.authRequest(device, credential.auth)
}

func (c *Client) activationRequest(target Device, credential transferCredential) ServiceEntitlementRequest {
	req := c.transferRequest(target, credential)
	// GMS cett builds a fresh activation request without the transfer app_name.
	req.AppName = ""
	return req
}

func (c *Client) addOptionalOperationFields(op *ODSAOperation) {
	companion := c.transfer.companion
	op.CompanionTerminalID = companion.IMEI
	op.CompanionTerminalVendor = companion.Vendor
	op.CompanionTerminalModel = companion.Model
	op.CompanionTerminalSoftware = companion.SoftwareVersion
	op.CompanionTerminalFriendlyName = companion.FriendlyName
	op.CompanionTerminalService = companion.Service
	op.CompanionTerminalICCID = companion.ICCID
	op.CompanionTerminalEID = companion.EID
}

func (c *Client) clientTS43() string {
	return firstNonEmpty(c.transfer.clientTS43, DefaultODSAClientTS43)
}

func (c *Client) realm() string {
	return firstNonEmpty(c.transfer.realm, DefaultEAPAKARealm)
}

func (c *Client) appName() string {
	if c.transfer.entitlement.OmitAppName {
		return ""
	}
	if c.transfer.entitlement.AppName != "" {
		return c.transfer.entitlement.AppName
	}
	return "androidTransfer"
}

func (c *Client) accept() string {
	if c.app() == AppODSAPrimary {
		return ODSAResponseAccept
	}
	return DefaultResponseAccept
}

func (c *Client) app() string {
	if c.transfer.entitlement.App != "" {
		return c.transfer.entitlement.App
	}
	return AppODSAPrimary
}

func (c *Client) requestVersion() string {
	if c.transfer.entitlement.RequestVersion != "" {
		return c.transfer.entitlement.RequestVersion
	}
	return "0"
}

func (c *Client) entitlementVersion() string {
	return firstNonEmpty(c.transfer.entitlement.Version, "9.0")
}

func selectedTransferType(entitlement Entitlement) TransferType {
	if entitlement.TransferType != TransferTypeDisabled || entitlement.SourceSIMType != SIMTypePSIM {
		return entitlement.TransferType
	}
	if entitlement.UseTempTokenConversion {
		return TransferTypeTemporaryTokenOldTerminalIDs
	}
	return TransferTypeOldTerminalIDs
}

func transferTypeUsesTemporaryToken(transferType TransferType) bool {
	switch transferType {
	case TransferTypeTemporaryToken, TransferTypeTemporaryTokenOldTerminalIDs, TransferTypeTemporaryTokenVariant11:
		return true
	default:
		return false
	}
}

func (c *Client) usesTemporaryToken() bool {
	return transferTypeUsesTemporaryToken(c.transfer.entitlement.TransferType)
}

func isHTTPStatus(err error, status int) bool {
	httpErr, ok := errors.AsType[*HTTPError](err)
	return ok && httpErr.StatusCode == status
}

func manageSubscriptionAccepted(resp *ODSAResponse) bool {
	if resp.OperationResult != OperationResultSuccess {
		return false
	}
	switch resp.SubscriptionResult {
	case SubscriptionResultDownloadProfile:
		return resp.Download.ActivationCode != "" || resp.Download.SMDPFQDN != ""
	case SubscriptionResultDelayedDownload:
		return true
	case SubscriptionResultDone, SubscriptionResultDismiss, SubscriptionResultDeleteProfileInUse:
		return true
	case SubscriptionResultContinueToWebsheet:
		return resp.Websheet.URL != ""
	case SubscriptionResultRequiresUserInput:
		return resp.Message.Text != ""
	default:
		return false
	}
}

func subscriptionUpdateAccepted(resp *ODSAResponse) bool {
	return resp != nil && resp.OperationResult == OperationResultSuccess &&
		resp.SubscriptionResult == SubscriptionResultDone
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func temporaryTokenExpiresAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("temporary token expiry is missing")
	}
	var expiresAt time.Time
	if err := expiresAt.UnmarshalText([]byte(value)); err != nil {
		return time.Time{}, fmt.Errorf("parse temporary token expiry: %w", err)
	}
	return expiresAt, nil
}

func authCredential(token AuthToken) transferCredential {
	return transferCredential{auth: token}
}

func temporaryCredential(token TemporaryToken) transferCredential {
	return transferCredential{temporary: token, temporaryToken: true}
}

func authTokenFromResponse(current AuthToken, resp *ODSAResponse, cookies []string, now time.Time) AuthToken {
	next := current
	next.Cookies = cookies
	if resp.AuthenticationToken == "" {
		return next
	}
	next.Value = resp.AuthenticationToken
	next.ExpiresAt = now.Add(time.Duration(resp.AuthTokenValidity) * time.Second)
	return next
}

func authMode(req ServiceEntitlementRequest) string {
	switch {
	case req.AuthenticationToken != "":
		return "token"
	case req.TemporaryToken != "":
		return "temporary_token"
	default:
		return "eap_aka"
	}
}

func (c transferCredential) validAt(now time.Time) bool {
	value, expiry := c.auth.Value, c.auth.ExpiresAt
	if c.temporaryToken {
		value, expiry = c.temporary.Value, c.temporary.ExpiresAt
	}
	return value != "" && (expiry.IsZero() || now.Before(expiry))
}

func (c transferCredential) rejectedBy(err error) bool {
	return isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) ||
		(c.temporaryToken && isHTTPStatus(err, http.StatusUnauthorized))
}
