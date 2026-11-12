package ts43

import (
	"context"
	"errors"
	"fmt"
)

// ErrWebsheetDismissed reports that the carrier websheet dismissed the flow.
var ErrWebsheetDismissed = errors.New("websheet dismissed")

func (c *Client) handleWebsheetResult(ctx context.Context, result *Result, callback WebsheetResult) (*Result, error) {
	session, err := requireTransferSession(result)
	if err != nil {
		return result, err
	}
	offer := session.offer
	switch callback.Event {
	case WebsheetEventProfileReadyWithActivationCode:
		if session.profileInstalled {
			return result, nil
		}
		if callback.ActivationCode == "" {
			return result, errors.New("activation code is required")
		}
		return c.applyDownloadConfiguration(result, DownloadConfig{
			ActivationCode: callback.ActivationCode,
			ProfileICCID:   callback.ICCID,
			IMEI:           callback.IMEI,
		}, ServiceStatusUnknown)
	case WebsheetEventProfileReadyWithDefaultSMDP:
		if session.profileInstalled {
			return result, nil
		}
		if callback.SMDPFQDN == "" {
			return result, errors.New("default SM-DP+ is required")
		}
		iccid := callback.ICCID
		if iccid == "0" {
			iccid = ""
		}
		return c.applyDownloadConfiguration(result, DownloadConfig{
			SMDPFQDN:     callback.SMDPFQDN,
			ProfileICCID: iccid,
			IMEI:         callback.IMEI,
		}, ServiceStatusUnknown)
	case WebsheetEventSelectionCompleted:
		if session.profileInstalled {
			return result, nil
		}
		if callback.ICCID == "" && callback.IMEI == "" {
			return result, errors.New("selected ICCID or IMEI is required")
		}
		session.websheetOpen = false
		if callback.ICCID != "" {
			offer.SourceDevice.ICCID = callback.ICCID
			offer.SourceIdentity.ICCID = callback.ICCID
		}
		if callback.IMEI != "" {
			offer.SourceDevice.IMEI = callback.IMEI
		}
		return c.activate(ctx, offer, UserInputResult{Button: MessageButtonUnknown})
	case WebsheetEventDismissFlow:
		session.websheetOpen = false
		return result, ErrWebsheetDismissed
	case WebsheetEventFinishFlow:
		session.websheetOpen = false
		return c.continueWebsheetNextAction(ctx, result, callback.NextAction)
	case WebsheetEventDeleteToken:
		offer.AuthToken = AuthToken{}
		offer.TemporaryToken = TemporaryToken{}
		return result, nil
	case WebsheetEventCheckProfileServiceStatus:
		if session.profileInstalled {
			return c.resumeActivation(ctx, result)
		}
		return c.continueConfiguration(ctx, result)
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
	if session.profileInstalled && (next == OperationAcquireConf || next == OperationManage) {
		// Continue activation reporting or confirmation after installation;
		// a portal callback must never request another transfer or download.
		return c.resumeActivation(ctx, result)
	}
	switch next {
	case OperationAcquireConf:
		return c.continueConfiguration(ctx, result)
	case OperationManage:
		return c.activate(ctx, offer, UserInputResult{Button: MessageButtonUnknown})
	default:
		return result, fmt.Errorf("unsupported websheet next action %q", next)
	}
}
