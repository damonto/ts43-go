package ts43

func googleTemporaryTokenOperation() ODSAOperation {
	return ODSAOperation{
		Operation:        OperationTempToken,
		OperationType:    OperationTypeOmit,
		OperationTargets: []Operation{OperationManage, OperationAcquireConf},
	}
}

func googleCheckEligibilityOperation() ODSAOperation {
	return ODSAOperation{
		Operation:     OperationCheck,
		OperationType: OperationTypeOmit,
	}
}

func googleManageSubscriptionOperation(entitlement Entitlement, req ManageSubscriptionRequest) ODSAOperation {
	source, target, answer := req.Source, req.Target, req.UserInput
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
		Operation:                   OperationAcquireConf,
		OperationType:               OperationTypeOmit,
		OldTerminalEntitlementProto: oldTerminalEntitlementProtocol(source),
	}
	if IsSourceCredentialTransferType(entitlement.TransferType) {
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalEID = target.EID
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
	op.OldTerminalEntitlementProto = oldTerminalEntitlementProtocol(source)
	if !googleIncludeOldTerminalIDs(entitlement) {
		return
	}
	op.OldTerminalID = source.IMEI
	op.OldTerminalICCID = source.ICCID
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
