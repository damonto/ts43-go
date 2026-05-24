package ts43

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	OperationCheck       Operation = "CheckEligibility"
	OperationTempToken   Operation = "AcquireTemporaryToken"
	OperationManage      Operation = "ManageSubscription"
	OperationAcquireConf Operation = "AcquireConfiguration"
)

type Operation string

func (op Operation) String() string { return string(op) }

type OperationType int

const (
	OperationTypeOmit                 OperationType = -1
	OperationTypeTransferSubscription OperationType = 3
	OperationTypeUpdateSubscription   OperationType = 4
)

func (t OperationType) String() string { return strconv.Itoa(int(t)) }

type TransferType int

type SIMType uint8

const (
	SIMTypeESIM SIMType = iota
	SIMTypePSIM
)

const (
	TransferTypeDisabled TransferType = 0

	// TS.43 does not name these transfer-type values, so only the values that
	// affect this TS.43 client are named.
	// Other non-zero values fall back to the ordinary TS.43 auth-token flow.
	TransferTypeTemporaryToken TransferType = 2

	// TransferTypeReusableActivationCode and TransferTypeTS43V8 use the
	// source-side credential path.
	TransferTypeReusableActivationCode TransferType = 3
	TransferTypeTS43V8                 TransferType = 6

	TransferTypeOldTerminalIDs               TransferType = 7
	TransferTypeTemporaryTokenOldTerminalIDs TransferType = 9
	TransferTypeTemporaryTokenVariant11      TransferType = 11
)

// IsSourceCredentialTransferType reports whether t uses the source-side
// credential path supported by this package.
func IsSourceCredentialTransferType(t TransferType) bool {
	return t == TransferTypeReusableActivationCode || t == TransferTypeTS43V8
}

func (t TransferType) String() string {
	if t == TransferTypeDisabled {
		return ""
	}
	return strconv.Itoa(int(t))
}

type OperationResult int

const (
	OperationResultUnknown OperationResult = 0
	OperationResultSuccess OperationResult = 1
)

func (r OperationResult) String() string {
	if r == OperationResultUnknown {
		return ""
	}
	return strconv.Itoa(int(r))
}

func (r *OperationResult) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal operation result: %w", err)
	}
	if !ok {
		*r = OperationResultUnknown
		return nil
	}
	*r = OperationResult(n)
	return nil
}

type AppEligibility int

const (
	AppEligibilityUnknown AppEligibility = -1
	AppEligibilityEnabled AppEligibility = 1
)

func (e *AppEligibility) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal app eligibility: %w", err)
	}
	if !ok {
		*e = AppEligibilityUnknown
		return nil
	}
	*e = AppEligibility(n)
	return nil
}

type SubscriptionResult int

const (
	SubscriptionResultUnknown                        SubscriptionResult = 0
	SubscriptionResultContinueToWebsheet             SubscriptionResult = 1
	SubscriptionResultDownloadProfile                SubscriptionResult = 2
	SubscriptionResultDone                           SubscriptionResult = 3
	SubscriptionResultDelayedDownload                SubscriptionResult = 4
	SubscriptionResultDismiss                        SubscriptionResult = 5
	SubscriptionResultDeleteProfileInUse             SubscriptionResult = 6
	SubscriptionResultRedownloadableProfileMandatory SubscriptionResult = 7
	SubscriptionResultRequiresUserInput              SubscriptionResult = 8
)

func (r SubscriptionResult) String() string {
	if r == SubscriptionResultUnknown {
		return ""
	}
	return strconv.Itoa(int(r))
}

func (r *SubscriptionResult) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal subscription result: %w", err)
	}
	if !ok {
		*r = SubscriptionResultUnknown
		return nil
	}
	*r = SubscriptionResult(n)
	return nil
}

type ServiceStatus int

const (
	ServiceStatusUnknown            ServiceStatus = 0
	ServiceStatusActivated          ServiceStatus = 1
	ServiceStatusActivating         ServiceStatus = 2
	ServiceStatusDeactivated        ServiceStatus = 3
	ServiceStatusDeactivatedNoReuse ServiceStatus = 4
)

func (s ServiceStatus) String() string {
	if s == ServiceStatusUnknown {
		return ""
	}
	return strconv.Itoa(int(s))
}

func (s *ServiceStatus) UnmarshalText(text []byte) error {
	n, ok, err := enumInteger(text)
	if err != nil {
		return fmt.Errorf("unmarshal service status: %w", err)
	}
	if !ok {
		*s = ServiceStatusUnknown
		return nil
	}
	*s = ServiceStatus(n)
	return nil
}

type MessageButton int

const (
	MessageButtonUnknown  MessageButton = -1
	MessageButtonRejected MessageButton = 0
	MessageButtonAccepted MessageButton = 1
)

func (b MessageButton) String() string {
	if b == MessageButtonUnknown {
		return ""
	}
	return strconv.Itoa(int(b))
}

func enumInteger(text []byte) (int, bool, error) {
	s := strings.TrimSpace(string(text))
	if s == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}
