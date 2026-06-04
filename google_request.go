package ts43

func googleTemporaryTokenOperation(entitlement Entitlement, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:        OperationTempToken,
		OperationType:    OperationTypeOmit,
		OperationTargets: []Operation{OperationManage, OperationAcquireConf},
	}
	if googleIncludeTargetTerminalIdentity(entitlement) {
		setGoogleTargetTerminalFields(&op, target, true)
	}
	return op
}

func googleCheckEligibilityOperation(entitlement Entitlement, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationCheck,
		OperationType: OperationTypeOmit,
	}
	if googleIncludeTargetTerminalIdentity(entitlement) {
		setGoogleTargetTerminalFields(&op, target, true)
	}
	return op
}

func googleManageSubscriptionOperation(entitlement Entitlement, source, target Device, answer UserInputResult) ODSAOperation {
	op := baseManageSubscriptionOperation(OperationTypeTransferSubscription, answer)
	if IsSourceCredentialTransferType(entitlement.TransferType) {
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalEID = target.EID
	} else {
		op.TerminalEID = target.EID
		op.TerminalICCID = target.ICCID
		if googleIncludeTargetTerminalIdentity(entitlement) {
			setGoogleTargetTerminalFields(&op, target, true)
		}
	}
	addGoogleOldTerminalFields(&op, entitlement, source)
	return op
}

func googleAcquireConfigurationOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationAcquireConf,
		OperationType: OperationTypeOmit,
	}
	if IsSourceCredentialTransferType(entitlement.TransferType) {
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalEID = target.EID
		op.OldTerminalEntitlementProto = oldTerminalEntitlementProtocol(source)
		return op
	}
	op.TerminalEID = target.EID
	op.TerminalICCID = target.ICCID
	if googleIncludeTargetTerminalIdentity(entitlement) {
		setGoogleTargetTerminalFields(&op, target, true)
	}
	return op
}

func googleUpdateSubscriptionOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationManage,
		OperationType: OperationTypeUpdateSubscription,
		TerminalEID:   target.EID,
		TerminalICCID: target.ICCID,
	}
	if googleIncludeTargetTerminalIdentity(entitlement) {
		setGoogleTargetTerminalFields(&op, target, true)
	}
	addGoogleOldTerminalFields(&op, entitlement, source)
	return op
}

func googleActivationConfigurationOperation(entitlement Entitlement, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationAcquireConf,
		OperationType: OperationTypeOmit,
		TerminalEID:   target.EID,
	}
	if googleIncludeTargetTerminalIdentity(entitlement) {
		setGoogleTargetTerminalFields(&op, target, true)
	}
	return op
}

func setGoogleTargetTerminalFields(op *ODSAOperation, target Device, includeIdentity bool) {
	if includeIdentity {
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalEID = target.EID
	}
	op.TargetTerminalICCID = target.ICCID
	op.TargetTerminalModel = target.Model
	op.TargetTerminalSN = target.SerialNumber
	op.TargetTerminalIMEIs = terminalIMEIs(target)
	op.TargetTerminalEntitlementProto = entitlementProtocol(target.EntitlementProtocol)
}

func addGoogleOldTerminalFields(op *ODSAOperation, entitlement Entitlement, source Device) {
	if !googleIncludeOldTerminalIDs(entitlement) {
		return
	}
	op.OldTerminalID = source.IMEI
	op.OldTerminalICCID = source.ICCID
	op.OldTerminalEntitlementProto = oldTerminalEntitlementProtocol(source)
}

func googleIncludeTargetTerminalIdentity(entitlement Entitlement) bool {
	switch entitlement.TransferType {
	case TransferTypeTemporaryToken, TransferTypeTemporaryTokenVariant11:
		return entitlement.SetTargetTerminalFields
	default:
		return true
	}
}

func googleIncludeOldTerminalIDs(entitlement Entitlement) bool {
	if !entitlement.RestrictOldTerminalFields {
		return true
	}
	switch entitlement.TransferType {
	case TransferTypeOldTerminalIDs, TransferTypeTemporaryTokenOldTerminalIDs:
		return true
	default:
		return false
	}
}
