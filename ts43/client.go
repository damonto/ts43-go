package ts43

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	ts43http "github.com/damonto/ts43-go/http"
	"github.com/damonto/ts43-go/sim"
)

type Client struct {
	cfg Config
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

const defaultEntitlementTimeout = 30 * time.Second

func New(cfg *Config) (*Client, error) {
	if cfg.Source.Device.IMEI == "" {
		return nil, errors.New("source IMEI is required")
	}
	if cfg.Target.Device.IMEI == "" {
		return nil, errors.New("target IMEI is required")
	}
	if cfg.Target.Device.EID == "" {
		return nil, errors.New("target EID is required")
	}
	next := *cfg
	if next.Logger == nil {
		next.Logger = slog.Default()
	}
	next.Entitlement.TransferType = selectedTransferType(next.Entitlement)
	if next.Entitlement.Timeout == 0 {
		next.Entitlement.Timeout = defaultEntitlementTimeout
	}
	if next.Entitlement.PollInterval == 0 {
		next.Entitlement.PollInterval = time.Minute
	}
	if next.Entitlement.PollTimeout == 0 {
		next.Entitlement.PollTimeout = 10 * time.Minute
	}
	if next.Entitlement.AutoRetry511Limit == 0 {
		next.Entitlement.AutoRetry511Limit = 1
	}
	return &Client{cfg: next}, nil
}

func (c *Client) Authenticate(ctx context.Context, identity sim.Identity, device Device) (AuthToken, error) {
	req := c.baseRequest(device, "", "")
	op := ODSAOperation{}
	resp, cookies, err := c.call(ctx, identity, req, op)
	if isHTTPStatus(err, http.StatusNetworkAuthenticationRequired) {
		req.OmitEAPID = true
		resp, cookies, err = c.call(ctx, identity, req, op)
	}
	if err != nil {
		return AuthToken{}, err
	}
	if resp.AuthenticationToken == "" {
		return AuthToken{}, errors.New("authentication token is missing")
	}
	expires := time.Time{}
	if resp.AuthTokenValidity > 0 {
		expires = time.Now().Add(time.Duration(resp.AuthTokenValidity) * time.Second)
	}
	return AuthToken{Value: resp.AuthenticationToken, ExpiresAt: expires, Cookies: cookies}, nil
}

func (c *Client) AcquireTemporaryToken(ctx context.Context, identity sim.Identity, source, target Device, auth AuthToken) (TransferTokenResult, error) {
	req := c.authRequest(source, auth)
	op := temporaryTokenOperation(c.cfg.Entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, cookies, err := c.call(ctx, identity, req, op)
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
	expiresAt, err := temporaryTokenExpiresAt(resp.TemporaryTokenExpiry, time.Now())
	if err != nil {
		return TransferTokenResult{}, err
	}
	mergedCookies := mergeCookieHeaders(auth.Cookies, cookies)
	return TransferTokenResult{
		Auth: authTokenFromResponse(auth, resp, mergedCookies),
		Temporary: TemporaryToken{
			Value:     resp.TemporaryToken,
			ExpiresAt: expiresAt,
			Cookies:   mergedCookies,
			Targets:   append([]Operation(nil), resp.OperationTargets...),
		},
	}, nil
}

func (c *Client) CheckEligibility(ctx context.Context, identity sim.Identity, source Device, auth AuthToken, target Device) (AuthToken, error) {
	req := c.authRequest(source, auth)
	op := checkEligibilityOperation(c.cfg.Entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, cookies, err := c.call(ctx, identity, req, op)
	if err != nil {
		return AuthToken{}, err
	}
	if resp.OperationResult != OperationResultSuccess || resp.PrimaryAppEligibility != AppEligibilityEnabled {
		return AuthToken{}, &CarrierDeniedError{Operation: OperationCheck, Result: resp}
	}
	return authTokenFromResponse(auth, resp, mergeCookieHeaders(auth.Cookies, cookies)), nil
}

func (c *Client) ManageSubscription(ctx context.Context, identity sim.Identity, source Device, target Device, auth AuthToken, answer UserInputResult) (*ODSAResponse, error) {
	return c.manageSubscription(ctx, identity, source, target, authCredential(auth), answer)
}

func (c *Client) ManageSubscriptionWithTemporaryToken(ctx context.Context, identity sim.Identity, source Device, target Device, token TemporaryToken, answer UserInputResult) (*ODSAResponse, error) {
	return c.manageSubscription(ctx, identity, source, target, temporaryCredential(token), answer)
}

func (c *Client) manageSubscription(ctx context.Context, identity sim.Identity, source Device, target Device, credential transferCredential, answer UserInputResult) (*ODSAResponse, error) {
	req, op := c.manageSubscriptionRequest(source, target, credential, answer)
	resp, _, err := c.call(ctx, identity, req, op)
	if err != nil {
		return resp, err
	}
	if !manageSubscriptionAccepted(resp) {
		return resp, &CarrierDeniedError{Operation: OperationManage, Result: resp}
	}
	return resp, nil
}

func (c *Client) manageSubscriptionRequest(source Device, target Device, credential transferCredential, answer UserInputResult) (ServiceEntitlementRequest, ODSAOperation) {
	reqDevice := manageSubscriptionRequestDevice(c.cfg.Entitlement, source, target)
	req := c.transferRequest(reqDevice, credential)
	op := manageSubscriptionOperation(c.cfg.Entitlement, source, target, answer)
	c.addOptionalOperationFields(&op)
	return req, op
}

func (c *Client) AcquireConfiguration(ctx context.Context, identity sim.Identity, source Device, target Device, auth AuthToken) (*ODSAResponse, error) {
	return c.acquireConfiguration(ctx, identity, source, target, authCredential(auth))
}

func (c *Client) AcquireConfigurationWithTemporaryToken(ctx context.Context, identity sim.Identity, source Device, target Device, token TemporaryToken) (*ODSAResponse, error) {
	return c.acquireConfiguration(ctx, identity, source, target, temporaryCredential(token))
}

func (c *Client) acquireConfiguration(ctx context.Context, identity sim.Identity, source Device, target Device, credential transferCredential) (*ODSAResponse, error) {
	reqDevice := acquireConfigurationRequestDevice(c.cfg.Entitlement, source, target)
	req := c.transferRequest(reqDevice, credential)
	op := acquireConfigurationOperation(c.cfg.Entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.call(ctx, identity, req, op)
	return resp, err
}

func (c *Client) UpdateSubscription(ctx context.Context, identity sim.Identity, source Device, target Device, auth AuthToken) (*ODSAResponse, error) {
	return c.updateSubscription(ctx, identity, source, target, authCredential(auth))
}

func (c *Client) UpdateSubscriptionWithTemporaryToken(ctx context.Context, identity sim.Identity, source Device, target Device, token TemporaryToken) (*ODSAResponse, error) {
	return c.updateSubscription(ctx, identity, source, target, temporaryCredential(token))
}

func (c *Client) updateSubscription(ctx context.Context, identity sim.Identity, source Device, target Device, credential transferCredential) (*ODSAResponse, error) {
	req := c.transferRequest(target, credential)
	op := updateSubscriptionOperation(c.cfg.Entitlement, source, target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.call(ctx, identity, req, op)
	if err != nil {
		return resp, err
	}
	if !subscriptionUpdateAccepted(resp) {
		return resp, &CarrierDeniedError{Operation: OperationManage, Result: resp}
	}
	return resp, nil
}

func (c *Client) AcquireActivationConfiguration(ctx context.Context, identity sim.Identity, target Device, auth AuthToken) (*ODSAResponse, error) {
	return c.acquireActivationConfiguration(ctx, identity, target, authCredential(auth))
}

func (c *Client) AcquireActivationConfigurationWithTemporaryToken(ctx context.Context, identity sim.Identity, target Device, token TemporaryToken) (*ODSAResponse, error) {
	return c.acquireActivationConfiguration(ctx, identity, target, temporaryCredential(token))
}

func (c *Client) acquireActivationConfiguration(ctx context.Context, identity sim.Identity, target Device, credential transferCredential) (*ODSAResponse, error) {
	req := c.transferRequest(target, credential)
	op := activationConfigurationOperation(c.cfg.Entitlement, target)
	c.addOptionalOperationFields(&op)
	resp, _, err := c.call(ctx, identity, req, op)
	return resp, err
}

func (c *Client) call(ctx context.Context, identity sim.Identity, req ServiceEntitlementRequest, op ODSAOperation) (*ODSAResponse, []string, error) {
	body, cookies, err := c.callRaw(ctx, identity, req, op)
	if err != nil {
		return nil, cookies, err
	}
	var resp ODSAResponse
	if err := resp.UnmarshalBinary(body); err != nil {
		return nil, cookies, err
	}
	return &resp, cookies, nil
}

func (c *Client) callRaw(ctx context.Context, identity sim.Identity, req ServiceEntitlementRequest, op ODSAOperation) ([]byte, []string, error) {
	httpReq, err := buildHTTPRequest(ctx, c.cfg.Entitlement, identity, req, op, c.clientTS43(), c.realm())
	if err != nil {
		return nil, nil, err
	}
	c.cfg.Logger.Debug("TS.43 request", "method", httpReq.Method, "url", httpReq.URL.String(), "auth", authMode(req))
	body, cookies, err := ts43http.DoRaw(ctx, c.httpClient(), httpReq)
	if err != nil {
		return nil, cookieHeaders(cookies), err
	}
	body, cookies, err = c.eapRelay(ctx, identity, body, req, cookies)
	if err != nil {
		return nil, cookieHeaders(cookies), err
	}
	return body, cookieHeaders(cookies), nil
}

func (c *Client) eapRelay(ctx context.Context, identity sim.Identity, body []byte, req ServiceEntitlementRequest, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	requestCookies := cookies
	responseCookies := cookies
	for range 3 {
		packet := relayPacket(body)
		if packet == "" {
			return body, responseCookies, nil
		}
		var challenge sim.AKAChallenge
		if err := challenge.UnmarshalText([]byte(packet)); err != nil {
			return nil, responseCookies, err
		}
		akaResp, err := c.cfg.Source.Channel.AuthenticateAKA(ctx, challenge.AuthenticationRequest())
		if err != nil {
			return nil, responseCookies, err
		}
		eapID, err := sim.IMSIEAPIdentity(identity.MCCMNC(), identity.IMSI, c.realm())
		if err != nil {
			return nil, responseCookies, err
		}
		responseText, err := sim.RespondToChallengeBase64(&challenge, akaResp, eapID)
		if err != nil {
			return nil, responseCookies, err
		}
		next, nextCookies, err := c.postRelay(ctx, responseText, req, requestCookies)
		if err != nil {
			return nil, responseCookies, err
		}
		requestCookies = nextCookies
		responseCookies = nextCookies
		body = next
	}
	return nil, responseCookies, errors.New("EAP-AKA relay exhausted")
}

func (c *Client) postRelay(ctx context.Context, packet string, req ServiceEntitlementRequest, cookies []*http.Cookie) ([]byte, []*http.Cookie, error) {
	data, err := json.Marshal(eapRelayPacket{Packet: packet})
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Entitlement.Server, bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	httpReq.Header.Set("Content-Type", EAPRelayContentType)
	httpReq.Header.Set("Accept", eapRelayAccept(req.Accept))
	httpReq.Header.Set("User-Agent", userAgent(req, firstNonEmpty(req.ClientTS43, c.clientTS43())))
	for _, cookie := range cookies {
		httpReq.AddCookie(cookie)
	}
	c.cfg.Logger.Debug("TS.43 EAP relay", "method", httpReq.Method, "url", httpReq.URL.String())
	return ts43http.DoRaw(ctx, c.httpClient(), httpReq)
}

func (c *Client) httpClient() HTTPDoer {
	if c.cfg.HTTPClient != nil {
		return c.cfg.HTTPClient
	}
	c.cfg.HTTPClient = ts43http.NewHTTPClient(c.cfg.Logger, c.cfg.Entitlement.Timeout)
	return c.cfg.HTTPClient
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
		AppVersion:          c.cfg.AppVersion,
		ProvisioningVersion: c.cfg.Entitlement.ProvisioningVersion,
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

func (c *Client) addOptionalOperationFields(op *ODSAOperation) {
	companion := c.cfg.Companion
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
	if c.cfg.ClientTS43 != "" {
		return c.cfg.ClientTS43
	}
	if c.cfg.Entitlement.Channel == EntitlementChannelSamsung {
		return SamsungODSAClientTS43
	}
	return firstNonEmpty(c.cfg.ClientTS43, DefaultODSAClientTS43)
}

func (c *Client) realm() string {
	return firstNonEmpty(c.cfg.Realm, DefaultEAPAkaRealm)
}

func (c *Client) appName() string {
	if c.cfg.Entitlement.AppName != "" {
		return c.cfg.Entitlement.AppName
	}
	if c.cfg.Entitlement.Channel == EntitlementChannelSamsung {
		return AppODSAPrimary
	}
	return "androidTransfer"
}

func (c *Client) accept() string {
	if c.cfg.Entitlement.Channel == EntitlementChannelSamsung {
		return ""
	}
	return DefaultResponseAccept
}

func (c *Client) app() string {
	if c.cfg.Entitlement.App != "" {
		return c.cfg.Entitlement.App
	}
	if c.cfg.Entitlement.Channel == EntitlementChannelSamsung {
		return AppODSAPrimary
	}
	return AppODSAPrimary
}

func (c *Client) requestVersion() string {
	if c.cfg.Entitlement.RequestVersion != "" {
		return c.cfg.Entitlement.RequestVersion
	}
	if c.cfg.Entitlement.Channel == EntitlementChannelSamsung {
		return "1"
	}
	return "0"
}

func (c *Client) entitlementVersion() string {
	return firstNonEmpty(c.cfg.Entitlement.Version, "9.0")
}

func oldTerminalEntitlementProtocol(source Device) string {
	if source.EntitlementProtocol != 0 {
		return entitlementProtocol(source.EntitlementProtocol)
	}
	return ""
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
	return transferTypeUsesTemporaryToken(c.cfg.Entitlement.TransferType)
}

func isHTTPStatus(err error, status int) bool {
	var httpErr *ts43http.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == status
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
	if resp.OperationResult != OperationResultSuccess {
		return false
	}
	switch resp.SubscriptionResult {
	case SubscriptionResultDone, SubscriptionResultDelayedDownload, SubscriptionResultDismiss:
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func terminalIMEIs(device Device) []string {
	if len(device.IMEIs) == 0 {
		return nil
	}
	return append([]string(nil), device.IMEIs...)
}

func temporaryTokenExpiresAt(value string, _ time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("temporary token expiry is missing")
	}
	if expiresAt, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return expiresAt, nil
	}
	return time.Time{}, fmt.Errorf("parse temporary token expiry %q", value)
}

func authCredential(token AuthToken) transferCredential {
	return transferCredential{auth: token}
}

func temporaryCredential(token TemporaryToken) transferCredential {
	return transferCredential{temporary: token, temporaryToken: true}
}

func authTokenFromResponse(current AuthToken, resp *ODSAResponse, cookies []string) AuthToken {
	next := current
	next.Cookies = cookies
	if resp.AuthenticationToken == "" {
		return next
	}
	next.Value = resp.AuthenticationToken
	next.ExpiresAt = time.Time{}
	if resp.AuthTokenValidity > 0 {
		next.ExpiresAt = time.Now().Add(time.Duration(resp.AuthTokenValidity) * time.Second)
	}
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
