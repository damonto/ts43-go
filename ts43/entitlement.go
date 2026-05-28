package ts43

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/damonto/ts43-go/sim"
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
	MCCMNC                                     string  `json:"mccmnc"`
	GID1                                       *string `json:"gid1"`
	CarrierName                                string  `json:"carrierName"`
	Channel                                    string  `json:"channel"`
	EntitlementServer                          string  `json:"entitlementServer"`
	App                                        string  `json:"app"`
	AppName                                    string  `json:"appName"`
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
func DiscoverEntitlement(identity sim.Identity, sourceSIMType SIMType) (Entitlement, error) {
	return DiscoverEntitlementForChannel(identity, sourceSIMType, "")
}

// DiscoverEntitlementForChannel returns a carrier TS.43 entitlement
// configuration for the requested client channel.
func DiscoverEntitlementForChannel(identity sim.Identity, sourceSIMType SIMType, channel EntitlementChannel) (Entitlement, error) {
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
	record, err := selectEntitlementRecord(records, identity.MCCMNC(), identity.GID1, normalizeEntitlementChannel(channel))
	if err != nil {
		return Entitlement{}, err
	}
	return record.entitlement(sourceSIMType)
}

func discoverEntitlementAuto(records []entitlementRecord, identity sim.Identity, sourceSIMType SIMType) (Entitlement, error) {
	var unsupported error
	for _, channel := range []EntitlementChannel{EntitlementChannelGoogle, EntitlementChannelSamsung} {
		record, err := selectEntitlementRecord(records, identity.MCCMNC(), identity.GID1, channel)
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
	return Entitlement{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementNotFound, identity.MCCMNC())
}

func selectEntitlementRecord(records []entitlementRecord, mccmnc, gid1 string, channel EntitlementChannel) (entitlementRecord, error) {
	var fallback []entitlementRecord
	var gidMatches []entitlementRecord
	for _, record := range records {
		if record.MCCMNC != mccmnc {
			continue
		}
		if normalizeEntitlementChannel(EntitlementChannel(record.Channel)) != channel {
			continue
		}
		if record.GID1 == nil {
			fallback = append(fallback, record)
			continue
		}
		if gid1 != "" && strings.EqualFold(*record.GID1, gid1) {
			gidMatches = append(gidMatches, record)
		}
	}
	if len(gidMatches) == 1 {
		return gidMatches[0], nil
	}
	if len(gidMatches) > 1 {
		return entitlementRecord{}, fmt.Errorf("%w for MCCMNC %s GID1 %s", ErrEntitlementAmbiguous, mccmnc, gid1)
	}
	if len(fallback) == 1 {
		return fallback[0], nil
	}
	if len(fallback) > 1 {
		return entitlementRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementAmbiguous, mccmnc)
	}
	return entitlementRecord{}, fmt.Errorf("%w for MCCMNC %s", ErrEntitlementNotFound, mccmnc)
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
		Channel:                   normalizeEntitlementChannel(EntitlementChannel(r.Channel)),
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
		AppName:                   r.AppName,
		ProvisioningVersion:       r.ProvisioningVersion,
		RequestVersion:            r.RequestVersion,
		Version:                   r.EntitlementVersion,
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
