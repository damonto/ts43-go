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
	switch callback.Event {
	case WebsheetEventProfileReadyWithActivationCode:
		if callback.ActivationCode == "" {
			return result, errors.New("activation code is required")
		}
		result.DownloadConfig = completeDownloadConfig(DownloadConfig{
			ActivationCode: callback.ActivationCode,
			ProfileICCID:   callback.ICCID,
			IMEI:           firstNonEmpty(callback.IMEI, result.Offer.TargetDevice.IMEI),
		}, result.Offer.TargetDevice)
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
		offer := result.Offer
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
		result.Offer.AuthToken = AuthToken{}
		result.Offer.TemporaryToken = TemporaryToken{}
		return result, nil
	case WebsheetEventCheckProfileServiceStatus:
		credential, err := c.transferCredential(ctx, result.Offer)
		if err != nil {
			return result, err
		}
		configuration, err := c.pollConfiguration(ctx, result.Offer, credential)
		result.Configuration = configuration
		if err != nil {
			return result, err
		}
		return c.applyDownloadConfiguration(result, configuration.Download)
	case WebsheetEventDeleteProfileInUse:
		result.State = StatePendingSourceProfileDeletion
		result.SourceProfileICCID = firstNonEmpty(callback.ICCID, result.SourceProfileICCID, result.Offer.SourceDevice.ICCID, result.Offer.SourceIdentity.ICCID)
		result.Event = SourceProfileDeletionEvent{ICCID: result.SourceProfileICCID}
		c.emit(result.Event)
		return result, nil
	default:
		return result, errors.New("websheet callback event is required")
	}
}

func (c *Client) continueWebsheetNextAction(ctx context.Context, result *Result, next Operation) (*Result, error) {
	if next == "" {
		next = OperationAcquireConf
	}
	switch next {
	case OperationAcquireConf:
		credential, err := c.transferCredential(ctx, result.Offer)
		if err != nil {
			return result, err
		}
		configuration, err := c.pollConfiguration(ctx, result.Offer, credential)
		result.Configuration = configuration
		if err != nil {
			return result, err
		}
		return c.applyDownloadConfiguration(result, configuration.Download)
	case OperationManage:
		return c.activate(ctx, result.Offer, UserInputResult{Button: MessageButtonUnknown})
	default:
		return result, fmt.Errorf("unsupported websheet next action %q", next)
	}
}
