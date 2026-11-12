package ts43

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

var (
	// ErrEntitlementNotFound reports that no supplied carrier entitlement
	// record matches the source SIM identity.
	ErrEntitlementNotFound = errors.New("entitlement config not found")

	// ErrEntitlementAmbiguous reports that discovery matched multiple carrier
	// records and cannot safely choose one.
	ErrEntitlementAmbiguous = errors.New("entitlement config is ambiguous")

	// ErrEntitlementUnsupported reports that the matched carrier does not
	// support the requested pSIM/eSIM transfer direction.
	ErrEntitlementUnsupported = errors.New("entitlement transfer is unsupported")
)

type entitlementRecord struct {
	MCCMNC                                     string  `json:"mccmnc"`
	PreferredAPN                               *string `json:"preferredApn"`
	GID1                                       *string `json:"gid1"`
	GID2                                       *string `json:"gid2"`
	SPN                                        *string `json:"spn"`
	IMSIPrefixXPattern                         *string `json:"imsiPrefixXpattern"`
	CarrierName                                string  `json:"carrierName"`
	EntitlementServer                          string  `json:"entitlementServer"`
	App                                        string  `json:"app"`
	AppName                                    *string `json:"appName"`
	EntitlementVersion                         string  `json:"entitlementVersion"`
	ProvisioningVersion                        string  `json:"provisioningVersion"`
	RequestVersion                             string  `json:"requestVersion"`
	TimeoutSeconds                             int     `json:"timeoutSeconds"`
	TransferTypePsim                           int     `json:"transferTypePsim"`
	TransferTypeEsim                           int     `json:"transferTypeEsim"`
	AcquireConfigurationPollingIntervalMinutes int     `json:"acquireConfigurationPollingIntervalMinutes"`
	AcquireConfigurationPollingTimeoutMinutes  int     `json:"acquireConfigurationPollingTimeoutMinutes"`
	UseHTTPPost                                bool    `json:"useHttpPost"`
	AutoRetry511Limit                          *int    `json:"autoRetry511Limit"`
	SupportPsimToEsimTransfer                  bool    `json:"supportPsimToEsimTransfer"`
	SupportEsimToEsimTransfer                  bool    `json:"supportEsimToEsimTransfer"`
	UseSMDSDelayedDownload                     bool    `json:"useSmdsDelayedDownload"`
	UseTempTokenConversion                     bool    `json:"useTempTokenConversion"`
	SetTargetTerminalFields                    bool    `json:"setTargetTerminalFields"`
	RestrictOldTerminalFields                  bool    `json:"restrictOldTerminalFields"`
}

// Discover returns the Google transfer entitlement configuration for the source
// SIM identity in the application-supplied carrier configuration.
func (c *EntitlementConfigs) Discover(identity Identity, sourceSIMType SIMType) (Entitlement, error) {
	if identity.MCC == "" || identity.MNC == "" {
		return Entitlement{}, errors.New("source MCC/MNC is required")
	}
	var records []entitlementRecord
	if c != nil {
		records = c.records
	}
	record, err := selectEntitlementRecord(records, identity)
	if err != nil {
		return Entitlement{}, err
	}
	return record.entitlement(sourceSIMType)
}

func selectEntitlementRecord(records []entitlementRecord, identity Identity) (entitlementRecord, error) {
	var matches []entitlementRecord
	bestScore := -1
	for _, record := range records {
		score, ok := record.match().googleScore(identity)
		if !ok || score < bestScore {
			continue
		}
		if score > bestScore {
			matches = matches[:0]
			bestScore = score
		}
		if slices.ContainsFunc(matches, func(match entitlementRecord) bool {
			return match.CarrierName == record.CarrierName && match.EntitlementServer == record.EntitlementServer
		}) {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return entitlementRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementAmbiguous, mccMNC(identity))
	}
	return entitlementRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementNotFound, mccMNC(identity))
}

func (r entitlementRecord) match() carrierMatch {
	return carrierMatch{
		MCCMNC:             r.MCCMNC,
		PreferredAPN:       r.PreferredAPN,
		GID1:               r.GID1,
		GID2:               r.GID2,
		SPN:                r.SPN,
		IMSIPrefixXPattern: r.IMSIPrefixXPattern,
	}
}

func (r entitlementRecord) entitlement(sourceSIMType SIMType) (Entitlement, error) {
	switch sourceSIMType {
	case SIMTypeESIM:
		if !r.SupportEsimToEsimTransfer {
			return Entitlement{}, fmt.Errorf("%w for MCCMNC %s eSIM source", ErrEntitlementUnsupported, r.MCCMNC)
		}
		return r.toEntitlement(sourceSIMType, TransferType(r.TransferTypeEsim)), nil
	case SIMTypePSIM:
		if !r.SupportPsimToEsimTransfer {
			return Entitlement{}, fmt.Errorf("%w for MCCMNC %s pSIM source", ErrEntitlementUnsupported, r.MCCMNC)
		}
		return r.toEntitlement(sourceSIMType, TransferType(r.TransferTypePsim)), nil
	default:
		return Entitlement{}, errors.New("source SIM type is invalid")
	}
}

func (r entitlementRecord) toEntitlement(sourceSIMType SIMType, transferType TransferType) Entitlement {
	entitlement := Entitlement{
		Server:                    r.EntitlementServer,
		UsePost:                   r.UseHTTPPost,
		SourceSIMType:             sourceSIMType,
		TransferType:              transferType,
		UseTempTokenConversion:    r.UseTempTokenConversion,
		SetTargetTerminalFields:   r.SetTargetTerminalFields,
		RestrictOldTerminalFields: r.RestrictOldTerminalFields,
		UseSMDSDelayedDownload:    r.UseSMDSDelayedDownload,
		PollInterval:              time.Duration(r.AcquireConfigurationPollingIntervalMinutes) * time.Minute,
		PollTimeout:               time.Duration(r.AcquireConfigurationPollingTimeoutMinutes) * time.Minute,
		App:                       r.App,

		ProvisioningVersion: r.ProvisioningVersion,
		RequestVersion:      r.RequestVersion,
		Version:             r.EntitlementVersion,
	}
	// An explicit zero retry limit disables retries; an absent field uses
	// the same default as a directly configured client.
	entitlement.AutoRetry511Limit = 1
	if r.AutoRetry511Limit != nil {
		entitlement.AutoRetry511Limit = *r.AutoRetry511Limit
	}
	if r.AppName != nil {
		entitlement.AppName = *r.AppName
		entitlement.OmitAppName = *r.AppName == ""
	}
	if r.TimeoutSeconds > 0 {
		entitlement.Timeout = time.Duration(r.TimeoutSeconds) * time.Second
	}
	return entitlement
}
