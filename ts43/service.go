package ts43

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/damonto/ts43-go/sim"
)

var (
	errPollingNotTriggered = errors.New("polling not triggered")
	errDownloadInfoMissing = errors.New("download info is missing")
)

const sourceProfileDeletedEntitlementProtocol = 1

func (c *Client) Transfer(ctx context.Context) (*Result, error) {
	return transferFlow{client: c}.Transfer(ctx)
}

func (c *Client) Continue(ctx context.Context, result *Result, req ContinueRequest) (*Result, error) {
	return transferFlow{client: c}.Continue(ctx, result, req)
}

func (c *Client) CompleteActivation(ctx context.Context, result *Result, activation ActivationResult) (*Result, error) {
	return transferFlow{client: c}.CompleteActivation(ctx, result, activation)
}

type transferFlow struct {
	client *Client
}

func (f transferFlow) Transfer(ctx context.Context) (*Result, error) {
	c := f.client
	identity, err := c.transfer.source.Channel.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("read source SIM identity: %w", err)
	}
	if err := c.discoverEntitlement(*identity); err != nil {
		return nil, err
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

func (c *Client) discoverEntitlement(identity sim.Identity) error {
	if c.transfer.entitlement.Server != "" {
		return nil
	}
	requested := c.transfer.entitlement
	discovered, err := DiscoverEntitlementForChannel(identity, requested.SourceSIMType, requested.Channel)
	if err != nil {
		return fmt.Errorf("discover entitlement config: %w", err)
	}
	discovered.SourceSIMType = requested.SourceSIMType
	if requested.Timeout != defaultEntitlementTimeout || discovered.Timeout == 0 {
		discovered.Timeout = requested.Timeout
	}
	discovered.AutoRetry511Limit = requested.AutoRetry511Limit
	c.transfer.entitlement = discovered
	return nil
}

func (f transferFlow) Continue(ctx context.Context, result *Result, req ContinueRequest) (*Result, error) {
	c := f.client
	if result == nil {
		return nil, errors.New("result is required")
	}
	switch result.State {
	case StatePendingUserInput:
		session, err := requireTransferSession(result)
		if err != nil {
			return result, err
		}
		if req.UserInput == nil {
			return result, errors.New("user input is required")
		}
		if req.UserInput.Button == MessageButtonUnknown {
			return result, errors.New("user input button is required")
		}
		return c.activate(ctx, session.offer, *req.UserInput)
	case StatePendingWebsheet:
		if _, err := requireTransferSession(result); err != nil {
			return result, err
		}
		if req.Websheet == nil {
			return result, errors.New("websheet result is required")
		}
		return c.handleWebsheetResult(ctx, result, *req.Websheet)
	case StatePendingSourceProfileDeletion:
		session, err := requireTransferSession(result)
		if err != nil {
			return result, err
		}
		if !req.SourceProfileDeleted {
			return result, errors.New("source profile deletion confirmation is required")
		}
		if IsSourceCredentialTransferType(c.transfer.entitlement.TransferType) {
			if session.offer.SourceDevice.EntitlementProtocol == 0 {
				session.offer.SourceDevice.EntitlementProtocol = sourceProfileDeletedEntitlementProtocol
			}
			return c.pollAfterSourceProfileDeletion(ctx, result)
		}
		return c.activate(ctx, session.offer, UserInputResult{Button: MessageButtonUnknown})
	default:
		return result, fmt.Errorf("state %d cannot continue", result.State)
	}
}

func (f transferFlow) CompleteActivation(ctx context.Context, result *Result, activation ActivationResult) (*Result, error) {
	c := f.client
	if result == nil {
		return nil, errors.New("result is required")
	}
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	if iccid := firstNonEmpty(activation.ICCID, result.DownloadConfig.ProfileICCID); iccid != "" {
		offer.TargetDevice.ICCID = iccid
		result.DownloadConfig.ProfileICCID = iccid
	}
	if IsSourceCredentialTransferType(c.transfer.entitlement.TransferType) {
		result.State = StateDone
		result.Event = DoneEvent{}
		c.emit(result.Event)
		return result, nil
	}
	credential, err := c.transferCredential(ctx, offer)
	if err != nil {
		return result, err
	}
	var update *ODSAResponse
	for attempt := 0; ; attempt++ {
		update, err = c.updateSubscription(ctx, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential)
		if isHTTPStatus(err, 511) && attempt < c.transfer.entitlement.AutoRetry511Limit {
			nextCredential, refreshErr := c.refreshTransferCredential(ctx, offer)
			if refreshErr != nil {
				result.UpdateSubscription = update
				return result, refreshErr
			}
			credential = nextCredential
			continue
		}
		break
	}
	result.UpdateSubscription = update
	if err != nil {
		return result, err
	}
	if update.SubscriptionResult == SubscriptionResultDone {
		result.State = StateDone
		result.Event = DoneEvent{}
		c.emit(result.Event)
		return result, nil
	}
	if update.SubscriptionResult == SubscriptionResultDismiss {
		result.State = StateDismissed
		result.Event = DismissEvent{SubscriptionResult: update.SubscriptionResult}
		c.emit(result.Event)
		return result, nil
	}
	if update.SubscriptionResult == SubscriptionResultDelayedDownload {
		result.State = StatePendingActivation
		result.Event = ActivationPendingEvent{
			ICCID:              offer.TargetDevice.ICCID,
			SubscriptionResult: update.SubscriptionResult,
		}
		c.emit(result.Event)
		return result, nil
	}
	configuration, err := c.pollActivationConfiguration(ctx, offer, credential)
	result.UpdateConfiguration = configuration
	if errors.Is(err, errPollingNotTriggered) {
		result.State = StatePendingActivation
		result.Event = ActivationPendingEvent{
			ICCID:              offer.TargetDevice.ICCID,
			SubscriptionResult: update.SubscriptionResult,
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

func (c *Client) fetchTransferToken(ctx context.Context, identity sim.Identity, source, target Device) (TransferTokenResult, error) {
	for attempt := 0; ; attempt++ {
		tokens, err := c.fetchTransferTokenOnce(ctx, identity, source, target)
		if isHTTPStatus(err, 511) && attempt < c.transfer.entitlement.AutoRetry511Limit {
			continue
		}
		if err != nil {
			return TransferTokenResult{}, err
		}
		return tokens, nil
	}
}

func (c *Client) fetchTransferTokenOnce(ctx context.Context, identity sim.Identity, source, target Device) (TransferTokenResult, error) {
	auth, err := c.Authenticate(ctx, identity, source)
	if err != nil {
		return TransferTokenResult{}, fmt.Errorf("authenticate TS.43: %w", err)
	}
	auth, err = c.CheckEligibility(ctx, identity, source, auth, target)
	if err != nil {
		return TransferTokenResult{}, err
	}
	return c.transferTokens(ctx, identity, source, target, auth)
}

func (c *Client) activate(ctx context.Context, offer *transferOffer, answer UserInputResult) (*Result, error) {
	credential, err := c.transferCredential(ctx, offer)
	if err != nil {
		return &Result{session: &transferSession{offer: offer}}, err
	}
	var manage *ODSAResponse
	for manageAttempt := 0; ; manageAttempt++ {
		manage, err = c.manageSubscription(ctx, offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, answer)
		if isHTTPStatus(err, 511) && manageAttempt < c.transfer.entitlement.AutoRetry511Limit {
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
	credential = c.updateTransferCredential(offer, manage, credential)
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
		result.Event = WebsheetEvent{Websheet: manage.Websheet}
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
	if subscriptionDone(manage) {
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
	if delayedDownloadRequired(manage) {
		result.State, result.Event = c.delayedDownloadState(offer, manage.SubscriptionResult)
		c.emit(result.Event)
		return result, nil
	}
	configuration, err := c.pollConfiguration(ctx, offer, credential)
	result.Configuration = configuration
	if errors.Is(err, errPollingNotTriggered) {
		result.State = StatePendingActivation
		result.Event = ActivationPendingEvent{
			ICCID:              offer.TargetDevice.ICCID,
			SubscriptionResult: manage.SubscriptionResult,
		}
		c.emit(result.Event)
		return result, nil
	}
	if err != nil {
		return result, err
	}
	return c.applyDownloadConfiguration(result, configuration.Download)
}

func (c *Client) pollAfterSourceProfileDeletion(ctx context.Context, result *Result) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	credential, err := c.transferCredential(ctx, offer)
	if err != nil {
		return result, err
	}
	configuration, err := c.pollConfiguration(ctx, offer, credential)
	result.Configuration = configuration
	if errors.Is(err, errPollingNotTriggered) {
		result.State = StatePendingActivation
		result.Event = ActivationPendingEvent{
			ICCID: offer.TargetDevice.ICCID,
		}
		c.emit(result.Event)
		return result, nil
	}
	if err != nil {
		return result, err
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
		if isHTTPStatus(err, 511) && retries < c.transfer.entitlement.AutoRetry511Limit {
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
		switch resp.ServiceStatus {
		case ServiceStatusActivated:
			return resp, nil
		case ServiceStatusActivating:
			interval, ok := c.pollInterval(resp)
			if !ok {
				return resp, errPollingNotTriggered
			}
			if err := c.sleep(ctx, interval); err != nil {
				return resp, err
			}
		case ServiceStatusDeactivated:
			return resp, errors.New("service is deactivated")
		case ServiceStatusDeactivatedNoReuse:
			return resp, errors.New("service is deactivated with no ICCID reuse")
		default:
			return resp, fmt.Errorf("unexpected service status %q", resp.ServiceStatus)
		}
	}
}

func (c *Client) pollActivationConfiguration(ctx context.Context, offer *transferOffer, credential transferCredential) (*ODSAResponse, error) {
	deadline := c.now().Add(c.transfer.entitlement.PollTimeout)
	retries := 0
	for {
		if !c.now().Before(deadline) {
			return nil, errors.New("activation configuration timeout")
		}
		resp, err := c.acquireActivationConfiguration(ctx, offer.SourceIdentity, offer.TargetDevice, credential)
		if isHTTPStatus(err, 511) && retries < c.transfer.entitlement.AutoRetry511Limit {
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
		switch resp.ServiceStatus {
		case ServiceStatusActivated:
			return resp, nil
		case ServiceStatusActivating:
			interval, ok := c.pollInterval(resp)
			if !ok {
				return resp, errPollingNotTriggered
			}
			if err := c.sleep(ctx, interval); err != nil {
				return resp, err
			}
		case ServiceStatusDeactivated:
			return resp, errors.New("activation service is deactivated")
		case ServiceStatusDeactivatedNoReuse:
			return resp, errors.New("activation service is deactivated with no ICCID reuse")
		default:
			return resp, fmt.Errorf("unexpected activation service status %q", resp.ServiceStatus)
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

func (c *Client) transferTokens(ctx context.Context, identity sim.Identity, source, target Device, auth AuthToken) (TransferTokenResult, error) {
	if !c.usesTemporaryToken() {
		return TransferTokenResult{Auth: auth}, nil
	}
	return c.AcquireTemporaryToken(ctx, identity, source, target, auth)
}

func (c *Client) transferCredential(ctx context.Context, offer *transferOffer) (transferCredential, error) {
	if c.usesTemporaryToken() {
		if offer.TemporaryToken.Value != "" {
			return temporaryCredential(offer.TemporaryToken), nil
		}
		return c.refreshTransferCredential(ctx, offer)
	}
	if offer.AuthToken.Value != "" {
		return authCredential(offer.AuthToken), nil
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
