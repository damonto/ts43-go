package ts43

import (
	"strconv"
	"strings"
)

func temporaryTokenOperation() ODSAOperation {
	return ODSAOperation{
		Operation:        OperationTempToken,
		OperationType:    OperationTypeOmit,
		OperationTargets: []Operation{OperationManage, OperationAcquireConf},
	}
}

func checkEligibilityOperation() ODSAOperation {
	return ODSAOperation{
		Operation:     OperationCheck,
		OperationType: OperationTypeOmit,
	}
}

func targetTerminalID(entitlement Entitlement, target Device) string {
	if sendAllTargetTerminalIDs(entitlement) && len(target.IMEIs) > 0 {
		return strings.Join(target.IMEIs, ",")
	}
	return target.IMEI
}

func sendAllTargetTerminalIDs(entitlement Entitlement) bool {
	// Pixel enables multi-IMEI sharing for carriers advertising TS.43 14.0+.
	version, err := strconv.ParseFloat(strings.TrimSpace(entitlement.Version), 64)
	return err == nil && version >= 14
}

func manageSubscriptionOperation(entitlement Entitlement, req ManageSubscriptionRequest) ODSAOperation {
	source, target, answer := req.Source, req.Target, req.UserInput
	op := ODSAOperation{
		Operation:        OperationManage,
		OperationType:    OperationTypeTransferSubscription,
		MessageResponse:  answer.Response,
		MessageButton:    answer.Button,
		MessageButtonSet: answer.Button != MessageButtonUnknown,
	}
	addTransferTerminalFields(&op, entitlement, target)
	addOldTerminalFields(&op, entitlement, source)
	return op
}

func acquireConfigurationOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:                   OperationAcquireConf,
		OperationType:               OperationTypeOmit,
		OldTerminalEntitlementProto: entitlementProtocol(source.EntitlementProtocol),
	}
	addTransferTerminalFields(&op, entitlement, target)
	return op
}

func updateSubscriptionOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationManage,
		OperationType: OperationTypeUpdateSubscription,
		TerminalEID:   target.EID,
		TerminalICCID: target.ICCID,
	}
	addOldTerminalFields(&op, entitlement, source)
	return op
}

func activationConfigurationOperation(target Device) ODSAOperation {
	return ODSAOperation{
		Operation:     OperationAcquireConf,
		OperationType: OperationTypeOmit,
		TerminalEID:   target.EID,
	}
}

func addTransferTerminalFields(op *ODSAOperation, entitlement Entitlement, target Device) {
	if !IsSourceCredentialTransferType(entitlement.TransferType) && entitlement.TransferType != TransferTypeOldTerminalIDs {
		op.TerminalEID = target.EID
	}
	if !includeTargetTerminalIdentity(entitlement) {
		return
	}
	// Pixel's transfer operations populate only these two target fields. The
	// IMEI list belongs in target_terminal_id, not target_terminal_imeis.
	op.TargetTerminalID = targetTerminalID(entitlement, target)
	op.TargetTerminalEID = target.EID
}

func addOldTerminalFields(op *ODSAOperation, entitlement Entitlement, source Device) {
	op.OldTerminalEntitlementProto = entitlementProtocol(source.EntitlementProtocol)
	if !includeOldTerminalIDs(entitlement, op.OperationType) {
		return
	}
	op.OldTerminalID = source.IMEI
	op.OldTerminalICCID = source.ICCID
}

func includeTargetTerminalIdentity(entitlement Entitlement) bool {
	switch entitlement.TransferType {
	case TransferTypeTemporaryToken, TransferTypeTemporaryTokenOldTerminalIDs, TransferTypeTemporaryTokenVariant11:
		return entitlement.SetTargetTerminalFields
	default:
		return true
	}
}

func includeOldTerminalIDs(entitlement Entitlement, operationType OperationType) bool {
	if !entitlement.RestrictOldTerminalFields {
		return true
	}
	// GMS cets preserves source IDs for source-credential transfers. Its cett
	// post-installation update applies the narrower type 7/9 rule instead.
	if operationType == OperationTypeTransferSubscription && IsSourceCredentialTransferType(entitlement.TransferType) {
		return true
	}
	switch entitlement.TransferType {
	case TransferTypeOldTerminalIDs, TransferTypeTemporaryTokenOldTerminalIDs:
		return true
	default:
		return false
	}
}

func transferRequestDevice(entitlement Entitlement, source, target Device) Device {
	if IsSourceCredentialTransferType(entitlement.TransferType) {
		return source
	}
	return target
}
