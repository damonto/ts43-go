package ts43

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrWebsheetDismissed              = errors.New("websheet dismissed")
	ErrWebsheetDefaultSMDPUnsupported = errors.New("websheet default SM-DP+ callback is unsupported")
)

func (c *Client) handleWebsheetResult(ctx context.Context, result *Result, callback WebsheetResult) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	switch callback.Event {
	case WebsheetEventProfileReadyWithActivationCode:
		if callback.ActivationCode == "" {
			return result, errors.New("activation code is required")
		}
		result.DownloadConfig = completeDownloadConfig(DownloadConfig{
			ActivationCode: callback.ActivationCode,
			ProfileICCID:   callback.ICCID,
			IMEI:           firstNonEmpty(callback.IMEI, offer.TargetDevice.IMEI),
		}, offer.TargetDevice)
		result.State = StatePendingDownload
		result.Event = DownloadReadyEvent{Config: result.DownloadConfig}
		c.emit(result.Event)
		return result, nil
	case WebsheetEventProfileReadyWithDefaultSMDP:
		return result, ErrWebsheetDefaultSMDPUnsupported
	case WebsheetEventSelectionCompleted:
		if callback.ICCID == "" && callback.IMEI == "" {
			return result, errors.New("selected ICCID or IMEI is required")
		}
		if callback.ICCID != "" {
			offer.SourceDevice.ICCID = callback.ICCID
			offer.SourceIdentity.ICCID = callback.ICCID
		}
		if callback.IMEI != "" {
			offer.SourceDevice.IMEI = callback.IMEI
		}
		return c.activate(ctx, offer, UserInputResult{Button: MessageButtonUnknown})
	case WebsheetEventDismissFlow:
		return result, ErrWebsheetDismissed
	case WebsheetEventFinishFlow:
		return c.continueWebsheetNextAction(ctx, result, callback.NextAction)
	case WebsheetEventDeleteToken:
		offer.AuthToken = AuthToken{}
		offer.TemporaryToken = TemporaryToken{}
		return result, nil
	case WebsheetEventCheckProfileServiceStatus:
		credential, err := c.transferCredential(ctx, offer)
		if err != nil {
			return result, err
		}
		configuration, err := c.pollConfiguration(ctx, offer, credential)
		result.Configuration = configuration
		if err != nil {
			return result, err
		}
		return c.applyDownloadConfiguration(result, configuration.Download)
	case WebsheetEventDeleteProfileInUse:
		result.State = StatePendingSourceProfileDeletion
		session.sourceProfileICCID = firstNonEmpty(callback.ICCID, session.sourceProfileICCID, offer.SourceDevice.ICCID, offer.SourceIdentity.ICCID)
		result.Event = SourceProfileDeletionEvent{ICCID: session.sourceProfileICCID}
		c.emit(result.Event)
		return result, nil
	default:
		return result, errors.New("websheet callback event is required")
	}
}

func (c *Client) continueWebsheetNextAction(ctx context.Context, result *Result, next Operation) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	if next == "" {
		next = OperationAcquireConf
	}
	switch next {
	case OperationAcquireConf:
		credential, err := c.transferCredential(ctx, offer)
		if err != nil {
			return result, err
		}
		configuration, err := c.pollConfiguration(ctx, offer, credential)
		result.Configuration = configuration
		if err != nil {
			return result, err
		}
		return c.applyDownloadConfiguration(result, configuration.Download)
	case OperationManage:
		return c.activate(ctx, offer, UserInputResult{Button: MessageButtonUnknown})
	default:
		return result, fmt.Errorf("unsupported websheet next action %q", next)
	}
}
