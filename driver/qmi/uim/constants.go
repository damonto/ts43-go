package uim

type PhysicalCardState uint32

const (
	PhysicalCardStateUnknown PhysicalCardState = 0x00
	PhysicalCardStateAbsent  PhysicalCardState = 0x01
	PhysicalCardStatePresent PhysicalCardState = 0x02
)

type SlotState uint32

const (
	SlotStateInactive SlotState = 0x00
	SlotStateActive   SlotState = 0x01
)

type CardState uint8

const (
	CardStateAbsent  CardState = 0x00
	CardStatePresent CardState = 0x01
	CardStateUnknown CardState = 0x02
)

type CardApplicationType uint8

const (
	CardApplicationTypeUnknown CardApplicationType = 0x00
	CardApplicationTypeSIM     CardApplicationType = 0x01
	CardApplicationTypeUSIM    CardApplicationType = 0x02
	CardApplicationTypeRUIM    CardApplicationType = 0x03
	CardApplicationTypeCSIM    CardApplicationType = 0x04
	CardApplicationTypeISIM    CardApplicationType = 0x05
)

type CardApplicationState uint8

const (
	CardApplicationStateUnknown                   CardApplicationState = 0x00
	CardApplicationStateDetected                  CardApplicationState = 0x01
	CardApplicationStatePIN1OrUPinPinRequired     CardApplicationState = 0x02
	CardApplicationStatePUK1OrUPinPUKRequired     CardApplicationState = 0x03
	CardApplicationStateCheckPersonalizationState CardApplicationState = 0x04
	CardApplicationStatePIN1Blocked               CardApplicationState = 0x05
	CardApplicationStateIllegal                   CardApplicationState = 0x06
	CardApplicationStateReady                     CardApplicationState = 0x07
)

type AuthenticationContext uint8

const (
	AuthenticationContextGSMAlgorithm      AuthenticationContext = 0x00
	AuthenticationContextCAVEAlgorithm     AuthenticationContext = 0x01
	AuthenticationContextGSMSecurity       AuthenticationContext = 0x02
	AuthenticationContext3G                AuthenticationContext = 0x03
	AuthenticationContextVGCS              AuthenticationContext = 0x04
	AuthenticationContextGBABootstrapping  AuthenticationContext = 0x05
	AuthenticationContextGBANAFDerivation  AuthenticationContext = 0x06
	AuthenticationContextMBMSMSKUpdate     AuthenticationContext = 0x07
	AuthenticationContextMBMSMTKGeneration AuthenticationContext = 0x08
	AuthenticationContextMBMSMSKDeletion   AuthenticationContext = 0x09
	AuthenticationContextMBMSMUKDeletion   AuthenticationContext = 0x0a
	AuthenticationContextIMSAKA            AuthenticationContext = 0x0b
	AuthenticationContextHTTPDigest        AuthenticationContext = 0x0c
	AuthenticationContextComputeIPCHAP     AuthenticationContext = 0x0d
	AuthenticationContextComputeIPMNHA     AuthenticationContext = 0x0e
	AuthenticationContextComputeIPMIPRRQ   AuthenticationContext = 0x0f
	AuthenticationContextComputeIPMNAAA    AuthenticationContext = 0x10
	AuthenticationContextComputeIPHRPD     AuthenticationContext = 0x11
)

type SessionType uint8

const (
	SessionTypePrimaryGWProvisioning   SessionType = 0x00
	SessionTypePrimary1XProvisioning   SessionType = 0x01
	SessionTypeSecondaryGWProvisioning SessionType = 0x02
	SessionTypeSecondary1XProvisioning SessionType = 0x03
	SessionTypeNonProvisioningOnSlot1  SessionType = 0x04
	SessionTypeNonProvisioningOnSlot2  SessionType = 0x05
	SessionTypeCardOnSlot1             SessionType = 0x06
	SessionTypeCardOnSlot2             SessionType = 0x07
	SessionTypeLogicalChannelOnSlot1   SessionType = 0x08
	SessionTypeLogicalChannelOnSlot2   SessionType = 0x09
)
