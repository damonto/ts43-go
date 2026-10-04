package ts43

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

//go:embed entitlement.json
var entitlementJSON []byte

var (
	// ErrEntitlementNotFound reports that no embedded carrier entitlement
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
	SamsungAndroidEntitlement                  bool    `json:"samsungAndroidEntitlement"`
	MCCMNC                                     string  `json:"mccmnc"`
	PreferredAPN                               *string `json:"preferredApn"`
	SamsungExtraTerminalParams                 bool    `json:"samsungExtraTerminalParams"`
	SamsungInjectWebsheetParams                bool    `json:"samsungInjectWebsheetParams"`
	SamsungInjectWebsheetParamsLGU             bool    `json:"samsungInjectWebsheetParamsLgu"`
	SamsungLegacyUserAgent                     bool    `json:"samsungLegacyUserAgent"`
	GID1                                       *string `json:"gid1"`
	GID2                                       *string `json:"gid2"`
	SPN                                        *string `json:"spn"`
	IMSIPrefixXPattern                         *string `json:"imsiPrefixXpattern"`
	CarrierName                                string  `json:"carrierName"`
	Channel                                    string  `json:"channel"`
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
	SupportPsimToEsimTransfer                  bool    `json:"supportPsimToEsimTransfer"`
	SupportEsimToEsimTransfer                  bool    `json:"supportEsimToEsimTransfer"`
	UseSMDSDelayedDownload                     bool    `json:"useSmdsDelayedDownload"`
	UseTempTokenConversion                     bool    `json:"useTempTokenConversion"`
	SetTargetTerminalFields                    bool    `json:"setTargetTerminalFields"`
	RestrictOldTerminalFields                  bool    `json:"restrictOldTerminalFields"`
}

// DiscoverEntitlement returns the carrier TS.43 entitlement configuration for
// the source SIM identity embedded in this package's entitlement list.
func DiscoverEntitlement(identity Identity, sourceSIMType SIMType) (Entitlement, error) {
	return DiscoverEntitlementForChannel(identity, sourceSIMType, "")
}

// DiscoverEntitlementForChannel returns a carrier TS.43 entitlement
// configuration for the requested client channel.
func DiscoverEntitlementForChannel(identity Identity, sourceSIMType SIMType, channel EntitlementChannel) (Entitlement, error) {
	if identity.MCC == "" || identity.MNC == "" {
		return Entitlement{}, errors.New("source MCC/MNC is required")
	}
	var records []entitlementRecord
	if err := json.Unmarshal(entitlementJSON, &records); err != nil {
		return Entitlement{}, fmt.Errorf("parse entitlement config: %w", err)
	}
	if channel == "" {
		return discoverEntitlementAuto(records, identity, sourceSIMType)
	}
	record, err := selectEntitlementRecord(records, identity, normalizeEntitlementChannel(channel))
	if err != nil {
		return Entitlement{}, err
	}
	return record.entitlement(sourceSIMType)
}

func discoverEntitlementAuto(records []entitlementRecord, identity Identity, sourceSIMType SIMType) (Entitlement, error) {
	var unsupported error
	for _, channel := range []EntitlementChannel{EntitlementChannelGoogle, EntitlementChannelSamsung} {
		record, err := selectEntitlementRecord(records, identity, channel)
		if errors.Is(err, ErrEntitlementNotFound) {
			continue
		}
		if err != nil {
			return Entitlement{}, err
		}
		entitlement, err := record.entitlement(sourceSIMType)
		if errors.Is(err, ErrEntitlementUnsupported) {
			if unsupported == nil {
				unsupported = err
			}
			continue
		}
		return entitlement, err
	}
	if unsupported != nil {
		return Entitlement{}, unsupported
	}
	return Entitlement{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementNotFound, mccMNC(identity))
}

func selectEntitlementRecord(records []entitlementRecord, identity Identity, channel EntitlementChannel) (entitlementRecord, error) {
	var matches []entitlementRecord
	bestScore := -1
	for _, record := range records {
		if normalizeEntitlementChannel(EntitlementChannel(record.Channel)) != channel {
			continue
		}
		score, ok := record.score(identity)
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
		SamsungAndroidEntitlement:      r.SamsungAndroidEntitlement,
		Server:                         r.EntitlementServer,
		Channel:                        normalizeEntitlementChannel(EntitlementChannel(r.Channel)),
		UsePost:                        r.UseHTTPPost,
		SamsungExtraTerminalParams:     r.SamsungExtraTerminalParams,
		SamsungInjectWebsheetParams:    r.SamsungInjectWebsheetParams,
		SamsungInjectWebsheetParamsLGU: r.SamsungInjectWebsheetParamsLGU,
		SamsungLegacyUserAgent:         r.SamsungLegacyUserAgent,
		SourceSIMType:                  sourceSIMType,
		TransferType:                   transferType,
		UseTempTokenConversion:         r.UseTempTokenConversion,
		SetTargetTerminalFields:        r.SetTargetTerminalFields,
		RestrictOldTerminalFields:      r.RestrictOldTerminalFields,
		UseSMDSDelayedDownload:         r.UseSMDSDelayedDownload,
		PollInterval:                   time.Duration(r.AcquireConfigurationPollingIntervalMinutes) * time.Minute,
		PollTimeout:                    time.Duration(r.AcquireConfigurationPollingTimeoutMinutes) * time.Minute,
		App:                            r.App,

		ProvisioningVersion: r.ProvisioningVersion,
		RequestVersion:      r.RequestVersion,
		Version:             r.EntitlementVersion,
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

func normalizeEntitlementChannel(channel EntitlementChannel) EntitlementChannel {
	if channel == "" {
		return EntitlementChannelGoogle
	}
	return channel
}

func (r entitlementRecord) score(identity Identity) (int, bool) {
	match := r.match()
	if normalizeEntitlementChannel(EntitlementChannel(r.Channel)) == EntitlementChannelGoogle {
		return match.googleScore(identity)
	}
	return match.score(identity)
}
