package ts43

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const (
	AppODSAPrimary        = "ap2009"
	AppVoWiFi             = "ap2004"
	DefaultResponseAccept = "application/json, text/vnd.wap.connectivity-xml"
	ODSAResponseAccept    = "text/vnd.wap.connectivity-xml"
	WFCResponseAccept     = "text/vnd.wap.connectivity-xml"
	EAPRelayContentType   = "application/vnd.gsma.eap-relay.v1.0+json"
	DefaultODSAClientTS43 = "client-Primary-ODSA"
	DefaultWFCClientTS43  = "client-IMS-Entitlement"
	DefaultEAPAKARealm    = "nai.epc"
)

type Config struct {
	// Entitlements supplies discovery records when Entitlement.Server is empty.
	Entitlements *EntitlementConfigs
	Logger       *slog.Logger
	// HTTPClient performs TS.43 requests. New clones and configures a standard
	// *http.Client whose Transport is nil or *http.Transport. Other implementations
	// are used unchanged and must provide TS.43-compatible TLS settings.
	// A nil interface or nil *http.Client uses the default client.
	HTTPClient  HTTPDoer
	Entitlement Entitlement
	Source      Endpoint
	// Target.Channel is required to refresh credentials with the activated
	// target profile when a post-download token expires or is rejected (401/511).
	Target     Endpoint
	Companion  Device
	Events     chan<- Event
	AppVersion string
	ClientTS43 string
	Realm      string
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
}

type Entitlement struct {
	Server                 string
	UsePost                bool
	Timeout                time.Duration
	SourceSIMType          SIMType
	TransferType           TransferType
	UseTempTokenConversion bool
	// SetTargetTerminalFields includes target identity fields for transfer
	// types 2, 9, and 11. Types 3, 6, and 7 include them unconditionally.
	SetTargetTerminalFields bool
	// SetCheckEligibilityTargetTerminalID includes the target identifier in
	// CheckEligibility. TS.43 14.0+ includes it regardless of this flag.
	SetCheckEligibilityTargetTerminalID bool
	// SupportRetryAfter allows download-configuration polling to continue on HTTP
	// 503. UseServerRetryAfterDelay selects the Retry-After delay in seconds when
	// valid; otherwise the carrier's PollInterval is used.
	SupportRetryAfter bool
	// UseServerRetryAfterDelay defaults to true in catalog records when omitted.
	// Direct Entitlement configuration uses this value as supplied.
	UseServerRetryAfterDelay bool
	// EnableDelayedESIMPushNotification preserves the carrier capability. This
	// library does not register notification tokens or receive push messages.
	EnableDelayedESIMPushNotification bool
	// SupportsMultipleTransfer preserves the carrier capability for integrating
	// applications. Each Transfer invocation still manages one subscription.
	SupportsMultipleTransfer bool
	// RestrictOldTerminalFields mirrors the GMS flag 45764267. When true,
	// initial transfers send old terminal IDs for types 3, 6, 7, and 9;
	// post-installation updates send them only for types 7 and 9.
	// When false, both operations include old terminal IDs for all types.
	RestrictOldTerminalFields bool
	// UseSMDSDelayedDownload routes server-delayed downloads through SM-DS discovery.
	UseSMDSDelayedDownload bool
	PollInterval           time.Duration
	PollTimeout            time.Duration
	AutoRetry511Limit      int
	App                    string
	// OmitAppName preserves an explicit empty carrier app_name.
	OmitAppName         bool
	AppName             string
	ProvisioningVersion string
	RequestVersion      string
	Version             string
}

type Endpoint struct {
	Channel Channel
	Device  Device
}

type Channel interface {
	Identity(context.Context) (*Identity, error)
	AuthenticateAKA(context.Context, *AKARequest) (*AKAResponse, error)
}

type Device struct {
	IMEI string
	// IMEIs lists all device IMEIs. TS.43 14.0+ joins target IMEIs with commas
	// in target_terminal_id where that field is sent, falling back to IMEI
	// when this list is empty. The request's terminal_id remains IMEI.
	IMEIs               []string
	ICCID               string
	EID                 string
	Vendor              string
	Model               string
	SoftwareVersion     string
	SerialNumber        string
	FriendlyName        string
	Service             string
	EntitlementProtocol int
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type State uint8

const (
	StateUnknown State = iota
	StatePendingUserInput
	StatePendingWebsheet
	StatePendingSourceProfileDeletion
	StatePendingDownload
	StatePendingActivation
	StateDone
	StatePendingSMDSDiscovery
	StateDismissed
	// StatePendingConfiguration waits for download configuration before installation.
	StatePendingConfiguration
)

type Event interface {
	ts43Event()
}

type UserInputEvent struct {
	Message Message
}

func (UserInputEvent) ts43Event() {}

type UserInputResult struct {
	Response string
	Button   MessageButton
}

type SubscriptionTransferRequest struct {
	Identity Identity
	Source   Device
	Target   Device
}

type ManageSubscriptionRequest struct {
	Identity  Identity
	Source    Device
	Target    Device
	UserInput UserInputResult
}

type ActivationConfigurationRequest struct {
	Identity Identity
	Target   Device
}

type WebsheetEvent struct {
	Websheet Websheet
}

func (WebsheetEvent) ts43Event() {}

type WebsheetResult struct {
	Event          WebsheetCallbackEvent
	ActivationCode string
	SMDPFQDN       string
	ICCID          string
	IMEI           string
	NextAction     Operation
}

type SourceProfileDeletionEvent struct {
	ICCID string
}

func (SourceProfileDeletionEvent) ts43Event() {}

type DownloadReadyEvent struct {
	Config DownloadConfig
}

func (DownloadReadyEvent) ts43Event() {}

type SMDSDiscoveryEvent struct {
	SourceICCID        string
	TargetEID          string
	TargetIMEI         string
	SubscriptionResult SubscriptionResult
}

func (SMDSDiscoveryEvent) ts43Event() {}

// ConfigurationPendingEvent requests a later check for profile download data.
type ConfigurationPendingEvent struct {
	ICCID string
}

func (ConfigurationPendingEvent) ts43Event() {}

type ActivationPendingEvent struct {
	ICCID              string
	SubscriptionResult SubscriptionResult
}

func (ActivationPendingEvent) ts43Event() {}

type DismissEvent struct {
	SubscriptionResult SubscriptionResult
}

func (DismissEvent) ts43Event() {}

type DoneEvent struct{}

func (DoneEvent) ts43Event() {}

type ContinueRequest struct {
	UserInput            *UserInputResult
	Websheet             *WebsheetResult
	SourceProfileDeleted bool
}

type ActivationResult struct {
	ICCID string
}

type Result struct {
	State               State
	Event               Event
	ManageSubscription  *ODSAResponse
	UpdateSubscription  *ODSAResponse
	Configuration       *ODSAResponse
	UpdateConfiguration *ODSAResponse
	DownloadConfig      DownloadConfig

	session *transferSession
}

type transferSession struct {
	offer              *transferOffer
	sourceProfileICCID string
	websheetOpen       bool
	profileInstalled   bool

	// downloadActivated carries Google's isActivated download-credential flag.
	downloadActivated bool
}

type transferOffer struct {
	SourceIdentity     Identity
	SourceDevice       Device
	TargetDevice       Device
	AuthToken          AuthToken
	TemporaryToken     TemporaryToken
	activationIdentity *Identity
}

// AuthToken is the TS.43 source authentication token sent as IMSI + token.
type AuthToken struct {
	Value     string
	ExpiresAt time.Time
	Cookies   []string
}

// TemporaryToken is the AcquireTemporaryToken credential sent as temporary_token.
type TemporaryToken struct {
	Value     string
	ExpiresAt time.Time
	Cookies   []string
	Targets   []Operation
}

type Message struct {
	Title                                   string
	Text                                    string
	AcceptButton                            bool
	AcceptButtonLabel                       string
	RejectButton                            bool
	RejectButtonLabel                       string
	AcceptFreeText                          bool
	AcceptFreeTextHint                      string
	AcceptFreeTextValidation                string
	AcceptFreeTextValidationFailedErrorText string
}

type Websheet struct {
	URL          string
	UserData     string
	ContentsType string
}

type WebsheetCallbackEvent int

const (
	WebsheetEventUnknown WebsheetCallbackEvent = iota
	WebsheetEventProfileReadyWithActivationCode
	WebsheetEventProfileReadyWithDefaultSMDP
	WebsheetEventSelectionCompleted
	WebsheetEventDismissFlow
	WebsheetEventFinishFlow
	WebsheetEventDeleteToken
	WebsheetEventCheckProfileServiceStatus
	WebsheetEventDeleteProfileInUse
	WebsheetEventVoWiFiEntitlementChanged
	WebsheetEventVoWiFiDismissFlow
)

type DownloadConfig struct {
	ActivationCode string
	SMDPFQDN       string
	MatchingID     string
	ProfileICCID   string
	IMEI           string
}
