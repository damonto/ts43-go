package ts43

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/damonto/ts43-go/sim"
)

const (
	AppODSAPrimary      = "ap2009"
	DefaultAccept       = "text/vnd.wap.connectivity-xml"
	EAPRelayContentType = "application/vnd.gsma.eap-relay.v1.0+json"
	DefaultClientTS43   = "client-Primary-ODSA"
	DefaultEAPAkaRealm  = "nai.epc"
)

type Config struct {
	Logger      *slog.Logger
	HTTPClient  HTTPDoer
	Entitlement Entitlement
	Source      Endpoint
	Target      Endpoint
	Companion   Device
	Events      chan<- Event
	AppVersion  string
	ClientTS43  string
	Realm       string
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
}

type Entitlement struct {
	Server                  string
	UsePost                 bool
	Timeout                 time.Duration
	SourceSIMType           SIMType
	TransferType            TransferType
	UseTempTokenConversion  bool
	SetTargetTerminalFields bool
	// RestrictOldTerminalFields mirrors the GMS Ljotr.Q feature flag. When it
	// is false, GMS sends old terminal IDs broadly; when true, it only sends
	// them for transfer types 7 and 9.
	RestrictOldTerminalFields bool
	// UseSMDSDelayedDownload routes server-delayed downloads through SM-DS discovery.
	UseSMDSDelayedDownload bool
	PollInterval           time.Duration
	PollTimeout            time.Duration
	AutoRetry511Limit      int
	AppName                string
	Version                string
}

type Endpoint struct {
	Channel Channel
	Device  Device
}

type Channel interface {
	Identity(context.Context) (*sim.Identity, error)
	AuthenticateAKA(context.Context, *sim.AKARequest) (*sim.AKAResponse, error)
}

type Device struct {
	IMEI                string
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
	StatePendingDelayedDownload
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

type DelayedDownloadEvent struct {
	SourceICCID        string
	TargetEID          string
	TargetIMEI         string
	SubscriptionResult SubscriptionResult
}

func (DelayedDownloadEvent) ts43Event() {}

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
	State State
	Event Event

	Offer                         *Offer
	ManageSubscription            *ODSAResponse
	UpdateSubscription            *ODSAResponse
	Configuration                 *ODSAResponse
	UpdateConfiguration           *ODSAResponse
	DownloadConfig                DownloadConfig
	SourceProfileDeletionRequired bool
	SourceProfileDeleted          bool
	SourceProfileICCID            string
}

type Offer struct {
	SourceIdentity sim.Identity
	SourceDevice   Device
	TargetDevice   Device
	AuthToken      AuthToken
	TemporaryToken TemporaryToken
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
)

type DownloadConfig struct {
	ActivationCode string
	SMDPFQDN       string
	MatchingID     string
	ProfileICCID   string
	IMEI           string
}
