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
	EntitlementServer                          string  `json:"entitlementServer"`
	AppName                                    string  `json:"appName"`
	EntitlementVersion                         string  `json:"entitlementVersion"`
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
	if identity.MCC == "" || identity.MNC == "" {
		return Entitlement{}, errors.New("source MCC/MNC is required")
	}
	var records []entitlementRecord
	if err := json.Unmarshal(entitlementJSON, &records); err != nil {
		return Entitlement{}, fmt.Errorf("parse entitlement config: %w", err)
	}
	record, err := selectEntitlementRecord(records, identity.MCCMNC(), identity.GID1)
	if err != nil {
		return Entitlement{}, err
	}
	return record.entitlement(sourceSIMType)
}

func selectEntitlementRecord(records []entitlementRecord, mccmnc, gid1 string) (entitlementRecord, error) {
	var fallback []entitlementRecord
	var gidMatches []entitlementRecord
	for _, record := range records {
		if record.MCCMNC != mccmnc {
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
		return r.toEntitlement(TransferType(r.TransferTypeEsim)), nil
	case SIMTypePSIM:
		if !r.SupportPsimToEsimTransfer {
			return Entitlement{}, fmt.Errorf("%w for MCCMNC %s pSIM source", ErrEntitlementUnsupported, r.MCCMNC)
		}
		return r.toEntitlement(TransferType(r.TransferTypePsim)), nil
	default:
		return Entitlement{}, errors.New("source SIM type is invalid")
	}
}

func (r entitlementRecord) toEntitlement(transferType TransferType) Entitlement {
	return Entitlement{
		Server:                    r.EntitlementServer,
		UsePost:                   r.UseHTTPPost,
		TransferType:              transferType,
		UseTempTokenConversion:    r.UseTempTokenConversion,
		SetTargetTerminalFields:   r.SetTargetTerminalFields,
		RestrictOldTerminalFields: r.RestrictOldTerminalFields,
		UseSMDSDelayedDownload:    r.UseSMDSDelayedDownload,
		PollInterval:              time.Duration(r.AcquireConfigurationPollingIntervalMinutes) * time.Minute,
		PollTimeout:               time.Duration(r.AcquireConfigurationPollingTimeoutMinutes) * time.Minute,
		AppName:                   r.AppName,
		Version:                   r.EntitlementVersion,
	}
}
