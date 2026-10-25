package ts43

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	errPollingNotTriggered = errors.New("polling not triggered")
	errDownloadInfoMissing = errors.New("download info is missing")
	// ErrSMDPTransferRequiresLPA reports that transfer type 8 cannot be
	// completed through TS.43 alone.
	ErrSMDPTransferRequiresLPA = errors.New("SMDP reusable activation code transfer requires LPA metadata lookup and source profile deletion")
)

const sourceProfileDeletedEntitlementProtocol = 1

func (c *Client) Transfer(ctx context.Context) (*Result, error) {
	if c.transfer.source.Channel == nil {
		return nil, errors.New("source channel is required")
	}
	identity, err := c.transfer.source.Channel.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("read source SIM identity: %w", err)
	}
	if identity == nil {
		return nil, errors.New("read source SIM identity: identity is missing")
	}
	if err := c.discoverEntitlement(*identity); err != nil {
		return nil, err
	}
	if c.transfer.entitlement.TransferType == TransferTypeReusableActivationCodeSMDP {
		return nil, ErrSMDPTransferRequiresLPA
	}
	source := c.transfer.source.Device
	source.ICCID = firstNonEmpty(source.ICCID, identity.ICCID)
	target := c.transfer.target.Device
	tokens, err := c.fetchTransferToken(ctx, *identity, source, target)
	if err != nil {
		return nil, err
	}
	offer := &transferOffer{
		SourceIdentity: *identity,
		SourceDevice:   source,
		TargetDevice:   target,
		AuthToken:      tokens.Auth,
		TemporaryToken: tokens.Temporary,
	}
	return c.activate(ctx, offer, UserInputResult{Button: MessageButtonUnknown})
}

func (c *Client) discoverEntitlement(identity Identity) error {
	if c.transfer.entitlement.Server != "" {
		return nil
	}
	requested := c.transfer.entitlement
	discovered, err := c.transfer.entitlements.DiscoverForChannel(identity, requested.SourceSIMType, requested.Channel)
	if err != nil {
		return fmt.Errorf("discover entitlement config: %w", err)
	}
	c.applyDiscoveredEntitlement(discovered)
	return nil
}

func (c *Client) applyDiscoveredEntitlement(discovered Entitlement) {
	requested := c.transfer.entitlement
	discovered.SourceSIMType = requested.SourceSIMType
	discovered.SamsungSubscriptionType = requested.SamsungSubscriptionType
	if c.runtime.explicitTimeout || discovered.Timeout == 0 {
		discovered.Timeout = requested.Timeout
	}
	discovered.AutoRetry511Limit = requested.AutoRetry511Limit
	c.transfer.entitlement = discovered
	if c.runtime.defaultHTTPClient {
		c.runtime.httpClient = NewHTTPClient(c.runtime.logger, discovered.Timeout)
	}
}

func (c *Client) Continue(ctx context.Context, result *Result, req ContinueRequest) (*Result, error) {
	if result == nil {
		return nil, errors.New("result is required")
	}
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	switch result.State {
	case StatePendingUserInput:
		if req.UserInput == nil {
			return result, errors.New("user input is required")
		}
		if req.UserInput.Button == MessageButtonUnknown {
			return result, errors.New("user input button is required")
		}
		return c.activate(ctx, session.offer, *req.UserInput)
	case StatePendingWebsheet:
		if req.Websheet == nil {
			return result, errors.New("websheet result is required")
		}
		return c.handleWebsheetResult(ctx, result, *req.Websheet)
	case StatePendingConfiguration:
		return c.continueConfiguration(ctx, result)
	case StatePendingActivation:
		return c.resumeActivation(ctx, result)
	case StatePendingSourceProfileDeletion:
		if !req.SourceProfileDeleted {
			return result, errors.New("source profile deletion confirmation is required")
		}
		if transferUsesSourceCredential(c.transfer.entitlement) {
			if session.offer.SourceDevice.EntitlementProtocol == 0 {
				session.offer.SourceDevice.EntitlementProtocol = sourceProfileDeletedEntitlementProtocol
			}
			return c.continueConfiguration(ctx, result)
		}
		if c.transfer.entitlement.Channel != EntitlementChannelSamsung && c.transfer.entitlement.TransferType != TransferTypeTemporaryTokenVariant11 {
			return c.continueConfiguration(ctx, result)
		}
		return c.activate(ctx, session.offer, UserInputResult{Button: MessageButtonUnknown})
	default:
		return result, fmt.Errorf("state %d cannot continue", result.State)
	}
}

func (c *Client) CompleteActivation(ctx context.Context, result *Result, activation ActivationResult) (*Result, error) {
	if result == nil {
		return nil, errors.New("result is required")
	}
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	if result.State == StateDone || result.State == StateDismissed {
		return result, nil
	}
	if result.State == StatePendingActivation {
		return c.resumeActivation(ctx, result)
	}
	if result.State == StatePendingConfiguration {
		return result, errors.New("download configuration is still pending")
	}
	offer := session.offer
	if iccid := firstNonEmpty(activation.ICCID, result.DownloadConfig.ProfileICCID); iccid != "" {
		offer.TargetDevice.ICCID = iccid
		result.DownloadConfig.ProfileICCID = iccid
	}
	auth, err := c.activationAuth(ctx, offer)
	if err != nil {
		return result, err
	}
	var update *ODSAResponse
	for attempt := 0; ; attempt++ {
		update, err = c.updateSubscriptionOnChannel(ctx, auth.channel, auth.identity, offer.SourceDevice, offer.TargetDevice, auth.credential)
		if auth.credential.rejectedBy(err) && attempt < c.transfer.entitlement.AutoRetry511Limit {
			nextAuth, refreshErr := c.refreshActivationAuth(ctx, offer)
			if refreshErr != nil {
				result.UpdateSubscription = update
				return result, refreshErr
			}
			auth = nextAuth
			result.DownloadConfig.ProfileICCID = offer.TargetDevice.ICCID
			continue
		}
		break
	}
	result.UpdateSubscription = update
	if err != nil {
		return result, err
	}
	c.updateTransferCredential(offer, update, auth.credential)
	// Once the update is accepted, errors from subsequent polling must leave
	// the result resumable without submitting the subscription update again.
	result.State = StatePendingActivation
	result.Event = ActivationPendingEvent{ICCID: offer.TargetDevice.ICCID, SubscriptionResult: update.SubscriptionResult}
	if c.transfer.entitlement.Channel == EntitlementChannelSamsung && update.SubscriptionResult == SubscriptionResultDismiss {
		result.State = StateDismissed
		result.Event = DismissEvent{SubscriptionResult: update.SubscriptionResult}
		c.emit(result.Event)
		return result, nil
	}
	if c.transfer.entitlement.Channel == EntitlementChannelSamsung && update.SubscriptionResult == SubscriptionResultDelayedDownload {
		c.emit(result.Event)
		return result, nil
	}
	return c.resumeActivation(ctx, result)
}

func (c *Client) resumeActivation(ctx context.Context, result *Result) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	auth, err := c.activationAuth(ctx, offer)
	if err != nil {
		return result, err
	}
	configuration, err := c.pollActivationConfiguration(ctx, offer, auth)
	result.DownloadConfig.ProfileICCID = offer.TargetDevice.ICCID
	result.UpdateConfiguration = configuration
	if errors.Is(err, errPollingNotTriggered) {
		result.State = StatePendingActivation
		result.Event = ActivationPendingEvent{
			ICCID:              offer.TargetDevice.ICCID,
			SubscriptionResult: result.UpdateSubscription.SubscriptionResult,
		}
		c.emit(result.Event)
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.State = StateDone
	result.Event = DoneEvent{}
	c.emit(result.Event)
	return result, nil
}

func (c *Client) fetchTransferToken(ctx context.Context, identity Identity, source, target Device) (TransferTokenResult, error) {
	return c.fetchTransferTokenWithChannel(ctx, c.transfer.source.Channel, identity, source, target)
}

func (c *Client) fetchTransferTokenWithChannel(ctx context.Context, channel Channel, identity Identity, source, target Device) (TransferTokenResult, error) {
	for attempt := 0; ; attempt++ {
		tokens, err := c.fetchTransferTokenOnce(ctx, channel, identity, source, target)
		if isHTTPStatus(err, 511) && attempt < c.transfer.entitlement.AutoRetry511Limit {
			continue
		}
		if err != nil {
			return TransferTokenResult{}, err
		}
		return tokens, nil
	}
}

func (c *Client) fetchTransferTokenOnce(ctx context.Context, channel Channel, identity Identity, source, target Device) (TransferTokenResult, error) {
	auth, err := c.authenticate(ctx, channel, identity, source)
	if err != nil {
		return TransferTokenResult{}, fmt.Errorf("authenticate TS.43: %w", err)
	}
	auth, err = c.checkEligibility(ctx, channel, identity, source, auth, target)
	if err != nil {
		return TransferTokenResult{}, err
	}
	return c.transferTokens(ctx, channel, identity, source, target, auth)
}

func (c *Client) activate(ctx context.Context, offer *transferOffer, answer UserInputResult) (*Result, error) {
	credential, err := c.transferCredential(ctx, offer)
	if err != nil {
		return &Result{session: &transferSession{offer: offer}}, err
	}
	var manage *ODSAResponse
	for manageAttempt := 0; ; manageAttempt++ {
		manage, err = c.manageSubscription(ctx, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, answer)
		if credential.rejectedBy(err) && manageAttempt < c.transfer.entitlement.AutoRetry511Limit {
			nextCredential, refreshErr := c.refreshTransferCredential(ctx, offer)
			if refreshErr != nil {
				return &Result{ManageSubscription: manage, session: &transferSession{offer: offer}}, refreshErr
			}
			credential = nextCredential
			continue
		}
		break
	}
	result := &Result{ManageSubscription: manage, session: &transferSession{offer: offer}}
	if err != nil {
		return result, err
	}
	c.updateTransferCredential(offer, manage, credential)
	if sourceProfileDeletionRequired(manage) {
		result.session.sourceProfileICCID = firstNonEmpty(offer.SourceDevice.ICCID, offer.SourceIdentity.ICCID)
		result.State = StatePendingSourceProfileDeletion
		result.Event = SourceProfileDeletionEvent{ICCID: result.session.sourceProfileICCID}
		c.emit(result.Event)
		return result, nil
	}
	if userInputRequired(manage) {
		result.State = StatePendingUserInput
		result.Event = UserInputEvent{Message: manage.Message}
		c.emit(result.Event)
		return result, nil
	}
	if websheetRequired(manage) {
		result.State = StatePendingWebsheet
		sheet := manage.Websheet
		if c.transfer.entitlement.Channel == EntitlementChannelSamsung {
			sheet = samsungWebsheet(c.transfer.entitlement, sheet, offer.TargetDevice)
		}
		result.Event = WebsheetEvent{Websheet: sheet}
		c.emit(result.Event)
		return result, nil
	}
	if downloadProfileReady(manage) {
		result.DownloadConfig = completeDownloadConfig(manage.Download, offer.TargetDevice)
		result.State = StatePendingDownload
		result.Event = DownloadReadyEvent{Config: result.DownloadConfig}
		c.emit(result.Event)
		return result, nil
	}
	updateConfigurationRequired := samsungUpdateConfigurationRequired(c.transfer.entitlement, manage)
	if subscriptionDone(manage) && !updateConfigurationRequired {
		result.State = StateDone
		result.Event = DoneEvent{}
		c.emit(result.Event)
		return result, nil
	}
	if subscriptionDismissed(manage) {
		result.State = StateDismissed
		result.Event = DismissEvent{SubscriptionResult: manage.SubscriptionResult}
		c.emit(result.Event)
		return result, nil
	}
	if delayedDownloadRequired(manage) && (c.transfer.entitlement.Channel == EntitlementChannelSamsung || c.transfer.entitlement.UseSMDSDelayedDownload) {
		result.State, result.Event = c.delayedDownloadState(offer, manage.SubscriptionResult)
		c.emit(result.Event)
		return result, nil
	}
	return c.continueConfiguration(ctx, result)
}

func (c *Client) continueConfiguration(ctx context.Context, result *Result) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	result.State = StatePendingConfiguration
	result.Event = ConfigurationPendingEvent{ICCID: offer.TargetDevice.ICCID}
	credential, err := c.transferCredential(ctx, offer)
	if err != nil {
		return result, err
	}
	configuration, err := c.pollConfiguration(ctx, offer, credential)
	result.Configuration = configuration
	if errors.Is(err, errPollingNotTriggered) {
		c.emit(result.Event)
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if result.ManageSubscription != nil && samsungUpdateConfigurationRequired(c.transfer.entitlement, result.ManageSubscription) && !configuration.DownloadInfoPresent {
		result.State = StateDone
		result.Event = DoneEvent{}
		c.emit(result.Event)
		return result, nil
	}
	return c.applyDownloadConfiguration(result, configuration.Download)
}

func (c *Client) pollConfiguration(ctx context.Context, offer *transferOffer, credential transferCredential) (*ODSAResponse, error) {
	deadline := c.now().Add(c.transfer.entitlement.PollTimeout)
	retries := 0
	for {
		if !c.now().Before(deadline) {
			return nil, errors.New("acquire configuration timeout")
		}
		resp, err := c.acquireConfiguration(ctx, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential)
		if credential.rejectedBy(err) && retries < c.transfer.entitlement.AutoRetry511Limit {
			retries++
			nextCredential, refreshErr := c.refreshTransferCredential(ctx, offer)
			if refreshErr != nil {
				return resp, refreshErr
			}
			credential = nextCredential
			continue
		}
		if err != nil {
			return resp, err
		}
		ready, err := c.waitForConfiguration(ctx, resp)
		if ready || err != nil {
			return resp, err
		}
	}
}

func (c *Client) pollActivationConfiguration(ctx context.Context, offer *transferOffer, auth activationAuth) (*ODSAResponse, error) {
	deadline := c.now().Add(c.transfer.entitlement.PollTimeout)
	retries := 0
	for {
		if !c.now().Before(deadline) {
			return nil, errors.New("activation configuration timeout")
		}
		resp, err := c.acquireActivationConfigurationOnChannel(ctx, auth.channel, auth.identity, offer.TargetDevice, auth.credential)
		if auth.credential.rejectedBy(err) && retries < c.transfer.entitlement.AutoRetry511Limit {
			retries++
			nextAuth, refreshErr := c.refreshActivationAuth(ctx, offer)
			if refreshErr != nil {
				return resp, refreshErr
			}
			auth = nextAuth
			continue
		}
		if err != nil {
			return resp, err
		}
		ready, err := c.waitForConfiguration(ctx, resp)
		if ready || err != nil {
			return resp, err
		}
	}
}

func (c *Client) refreshTransferCredential(ctx context.Context, offer *transferOffer) (transferCredential, error) {
	tokens, err := c.fetchTransferToken(ctx, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice)
	if err != nil {
		return transferCredential{}, fmt.Errorf("refresh transfer credential: %w", err)
	}
	offer.AuthToken = tokens.Auth
	offer.TemporaryToken = tokens.Temporary
	return c.transferCredentialFromTokens(tokens), nil
}

type activationAuth struct {
	identity   Identity
	channel    Channel
	credential transferCredential
}

func (c *Client) activationAuth(ctx context.Context, offer *transferOffer) (activationAuth, error) {
	auth := activationAuth{identity: offer.SourceIdentity, channel: c.transfer.source.Channel, credential: c.offerCredential(offer)}
	if !auth.credential.validAt(c.now()) {
		return c.refreshActivationAuth(ctx, offer)
	}
	if offer.activationIdentity != nil {
		auth.identity = *offer.activationIdentity
		auth.channel = c.transfer.target.Channel
	}
	return auth, nil
}

func (c *Client) refreshActivationAuth(ctx context.Context, offer *transferOffer) (activationAuth, error) {
	channel := c.transfer.target.Channel
	if channel == nil {
		return activationAuth{}, errors.New("refresh activation credential: target channel is required")
	}
	identity, err := channel.Identity(ctx)
	if err != nil {
		return activationAuth{}, fmt.Errorf("read target subscription identity: %w", err)
	}
	if identity == nil {
		return activationAuth{}, errors.New("read target subscription identity: identity is missing")
	}
	target := offer.TargetDevice
	target.ICCID = firstNonEmpty(identity.ICCID, target.ICCID)
	offer.TargetDevice.ICCID = target.ICCID
	tokens, err := c.fetchTransferTokenWithChannel(ctx, channel, *identity, target, target)
	if err != nil {
		return activationAuth{}, fmt.Errorf("refresh activation credential: %w", err)
	}
	offer.AuthToken = tokens.Auth
	offer.TemporaryToken = tokens.Temporary
	offer.activationIdentity = identity
	return activationAuth{identity: *identity, channel: channel, credential: c.transferCredentialFromTokens(tokens)}, nil
}

func (c *Client) transferTokens(ctx context.Context, channel Channel, identity Identity, source, target Device, auth AuthToken) (TransferTokenResult, error) {
	if !c.usesTemporaryToken() {
		return TransferTokenResult{Auth: auth}, nil
	}
	return c.acquireTemporaryToken(ctx, channel, identity, source, target, auth)
}

func (c *Client) transferCredential(ctx context.Context, offer *transferOffer) (transferCredential, error) {
	credential := c.offerCredential(offer)
	if credential.validAt(c.now()) {
		return credential, nil
	}
	return c.refreshTransferCredential(ctx, offer)
}

func (c *Client) transferCredentialFromTokens(tokens TransferTokenResult) transferCredential {
	if c.usesTemporaryToken() {
		return temporaryCredential(tokens.Temporary)
	}
	return authCredential(tokens.Auth)
}

func (c *Client) updateTransferCredential(offer *transferOffer, resp *ODSAResponse, credential transferCredential) transferCredential {
	if resp.AuthenticationToken == "" {
		return credential
	}
	offer.AuthToken = authTokenFromResponse(offer.AuthToken, resp, offer.AuthToken.Cookies, c.now())
	if c.usesTemporaryToken() {
		return credential
	}
	next := authTokenFromResponse(credential.auth, resp, credential.auth.Cookies, c.now())
	offer.AuthToken = next
	return authCredential(next)
}

func sourceProfileDeletionRequired(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess && resp.SubscriptionResult == SubscriptionResultDeleteProfileInUse
}

func userInputRequired(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess && resp.SubscriptionResult == SubscriptionResultRequiresUserInput
}

func websheetRequired(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess && resp.SubscriptionResult == SubscriptionResultContinueToWebsheet
}

func downloadProfileReady(resp *ODSAResponse) bool {
	if resp.OperationResult != OperationResultSuccess || resp.SubscriptionResult != SubscriptionResultDownloadProfile {
		return false
	}
	return resp.Download.ActivationCode != "" || resp.Download.SMDPFQDN != ""
}

func subscriptionDone(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess && resp.SubscriptionResult == SubscriptionResultDone
}

func samsungUpdateConfigurationRequired(entitlement Entitlement, resp *ODSAResponse) bool {
	return entitlement.Channel == EntitlementChannelSamsung &&
		samsungSubscriptionIntent(entitlement) == SamsungSubscriptionUpdate &&
		subscriptionDone(resp)
}

func subscriptionDismissed(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess && resp.SubscriptionResult == SubscriptionResultDismiss
}

func delayedDownloadRequired(resp *ODSAResponse) bool {
	return resp.OperationResult == OperationResultSuccess &&
		resp.SubscriptionResult == SubscriptionResultDelayedDownload
}

func (c *Client) delayedDownloadState(offer *transferOffer, subscriptionResult SubscriptionResult) (State, Event) {
	event := DelayedDownloadEvent{
		SourceICCID:        firstNonEmpty(offer.SourceDevice.ICCID, offer.SourceIdentity.ICCID),
		TargetEID:          offer.TargetDevice.EID,
		TargetIMEI:         offer.TargetDevice.IMEI,
		SubscriptionResult: subscriptionResult,
	}
	if c.transfer.entitlement.UseSMDSDelayedDownload {
		return StatePendingSMDSDiscovery, SMDSDiscoveryEvent(event)
	}
	return StatePendingDelayedDownload, event
}

func (c *Client) applyDownloadConfiguration(result *Result, cfg DownloadConfig) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	cfg = completeDownloadConfig(cfg, session.offer.TargetDevice)
	if cfg.ActivationCode == "" && cfg.SMDPFQDN == "" {
		return result, errDownloadInfoMissing
	}
	result.DownloadConfig = cfg
	result.State = StatePendingDownload
	result.Event = DownloadReadyEvent{Config: result.DownloadConfig}
	c.emit(result.Event)
	return result, nil
}

func requireTransferSession(result *Result) (*transferSession, error) {
	if result == nil || result.session == nil || result.session.offer == nil {
		return nil, errors.New("transfer session is required")
	}
	return result.session, nil
}

func completeDownloadConfig(cfg DownloadConfig, target Device) DownloadConfig {
	if cfg.IMEI == "" {
		cfg.IMEI = target.IMEI
	}
	return cfg
}

func (c *Client) pollInterval(resp *ODSAResponse) (time.Duration, bool) {
	if !resp.PollingIntervalSet && c.transfer.entitlement.Channel == EntitlementChannelSamsung {
		return 0, false
	}
	if !resp.PollingIntervalSet {
		return c.transfer.entitlement.PollInterval, c.transfer.entitlement.PollInterval > 0
	}
	if resp.PollingInterval <= 0 {
		return 0, false
	}
	switch resp.PollingIntervalUnit {
	case 1:
		return time.Duration(resp.PollingInterval) * time.Second, true
	case 2:
		return time.Duration(resp.PollingInterval) * 100 * time.Millisecond, true
	default:
		return time.Duration(resp.PollingInterval) * time.Minute, true
	}
}

func (c *Client) now() time.Time {
	if c.runtime.now != nil {
		return c.runtime.now()
	}
	return time.Now()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.runtime.sleep != nil {
		return c.runtime.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) emit(event Event) {
	if c.runtime.events == nil {
		return
	}
	select {
	case c.runtime.events <- event:
	default:
	}
}

func (c *Client) offerCredential(offer *transferOffer) transferCredential {
	if c.usesTemporaryToken() {
		return temporaryCredential(offer.TemporaryToken)
	}
	return authCredential(offer.AuthToken)
}

func (c *Client) waitForConfiguration(ctx context.Context, resp *ODSAResponse) (bool, error) {
	switch resp.ServiceStatus {
	case ServiceStatusActivated:
		return true, nil
	case ServiceStatusActivating:
		interval, ok := c.pollInterval(resp)
		if !ok {
			return false, errPollingNotTriggered
		}
		return false, c.sleep(ctx, interval)
	case ServiceStatusDeactivated:
		return false, errors.New("service is deactivated")
	case ServiceStatusDeactivatedNoReuse:
		return false, errors.New("service is deactivated with no ICCID reuse")
	default:
		return false, fmt.Errorf("unexpected service status %q", resp.ServiceStatus)
	}
}
