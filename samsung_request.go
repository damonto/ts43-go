package ts43

func samsungTemporaryTokenOperation(source Device) ODSAOperation {
	return ODSAOperation{
		Operation:        OperationTempToken,
		OperationType:    OperationTypeOmit,
		OperationTargets: []Operation{OperationManage, OperationAcquireConf},
		TerminalICCID:    source.ICCID,
	}
}

func samsungCheckEligibilityOperation(source, target Device) ODSAOperation {
	return ODSAOperation{
		Operation:        OperationCheck,
		OperationType:    OperationTypeOmit,
		TerminalICCID:    source.ICCID,
		TargetTerminalID: target.IMEI,
	}
}

func samsungManageSubscriptionOperation(entitlement Entitlement, req ManageSubscriptionRequest) ODSAOperation {
	source, target, answer := req.Source, req.Target, req.UserInput
	op := baseManageSubscriptionOperation(samsungManageSubscriptionOperationType(entitlement), answer)
	op.TargetTerminalID = target.IMEI
	op.TargetTerminalICCID = target.ICCID
	op.OldTerminalID = source.IMEI
	op.OldTerminalICCID = source.ICCID
	switch {
	case transferUsesSourceCredential(entitlement):
		op.TerminalEID = source.EID
		op.TerminalICCID = source.ICCID
		op.TargetTerminalEID = target.EID
	case samsungDigitalizedTransfer(entitlement) && !samsungDigitalizedTempToken4FF(entitlement):
		// Ordinary conversion still identifies the physical SIM as terminal SIM.
		op.TerminalICCID = source.ICCID
		op.TargetTerminalEID = target.EID
		op.OldTerminalID = ""
		op.OldTerminalICCID = ""
	default:
		setSamsungTargetEID(&op, entitlement, target)
		if !samsungDigitalizedTempToken4FF(entitlement) {
			op.TerminalICCID = target.ICCID
		}
	}
	return op
}

func samsungAcquireConfigurationOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	op := ODSAOperation{
		Operation:     OperationAcquireConf,
		OperationType: OperationTypeOmit,
	}
	if transferUsesSourceCredential(entitlement) {
		op.TargetTerminalID = target.IMEI
		op.TerminalEID = firstNonEmpty(source.EID, target.EID)
		op.TerminalICCID = source.ICCID
		op.TargetTerminalICCID = target.ICCID
		return op
	}
	op.TerminalICCID = target.ICCID
	if samsungDigitalizedTransfer(entitlement) && !samsungDigitalizedTempToken4FF(entitlement) {
		op.TerminalICCID = source.ICCID
	}
	setSamsungTargetEID(&op, entitlement, target)
	op.TargetTerminalID = target.IMEI
	op.TargetTerminalICCID = target.ICCID
	return op
}

func samsungUpdateSubscriptionOperation(source, target Device) ODSAOperation {
	return ODSAOperation{
		Operation:           OperationManage,
		OperationType:       OperationTypeUpdateSubscription,
		TerminalEID:         target.EID,
		TerminalICCID:       target.ICCID,
		TargetTerminalID:    target.IMEI,
		TargetTerminalICCID: target.ICCID,
		OldTerminalID:       source.IMEI,
		OldTerminalICCID:    source.ICCID,
	}
}

func samsungActivationConfigurationOperation(target Device) ODSAOperation {
	return ODSAOperation{
		Operation:           OperationAcquireConf,
		OperationType:       OperationTypeOmit,
		TerminalEID:         target.EID,
		TerminalICCID:       target.ICCID,
		TargetTerminalID:    target.IMEI,
		TargetTerminalICCID: target.ICCID,
	}
}

func samsungManageSubscriptionOperationType(entitlement Entitlement) OperationType {
	switch samsungSubscriptionIntent(entitlement) {
	case SamsungSubscriptionUnsubscribe, SamsungSubscriptionUnsubscribe4FF:
		return OperationTypeSamsungUnsubscribe
	case SamsungSubscriptionChange, SamsungSubscriptionChange4FF:
		return OperationTypeSamsungChangeSubscription
	case SamsungSubscriptionDigitalizedSIM,
		SamsungSubscriptionTransferOpenID,
		SamsungSubscriptionTransferSMSOTP,
		SamsungSubscriptionRemoteAKATransfer,
		SamsungSubscriptionContinueTemporaryToken:
		return OperationTypeTransferSubscription
	case SamsungSubscriptionUpdate:
		return OperationTypeUpdateSubscription
	default:
		return OperationTypeSamsungD2DTransfer
	}
}

func samsungDigitalizedTempToken4FF(entitlement Entitlement) bool {
	return samsungDigitalizedTransfer(entitlement) &&
		entitlement.UseTempTokenConversion
}

func samsungDigitalizedTransfer(entitlement Entitlement) bool {
	return entitlement.Channel == EntitlementChannelSamsung &&
		samsungSubscriptionIntent(entitlement) == SamsungSubscriptionDigitalizedSIM
}

func samsungSubscriptionIntent(entitlement Entitlement) SamsungSubscriptionType {
	if entitlement.SamsungSubscriptionType != SamsungSubscriptionUnspecified {
		return entitlement.SamsungSubscriptionType
	}
	if entitlement.SourceSIMType == SIMTypePSIM {
		return SamsungSubscriptionDigitalizedSIM
	}
	if transferTypeUsesTemporaryToken(entitlement.TransferType) {
		return SamsungSubscriptionContinueTemporaryToken
	}
	return SamsungSubscriptionRemoteAKATransfer
}

func setSamsungTargetEID(op *ODSAOperation, entitlement Entitlement, target Device) {
	usesTargetEID := entitlement.TransferType == TransferTypeOldTerminalIDs ||
		(samsungDigitalizedTransfer(entitlement) && !samsungDigitalizedTempToken4FF(entitlement))
	if usesTargetEID {
		op.TerminalEID = ""
		op.TargetTerminalEID = target.EID
		return
	}
	op.TerminalEID = target.EID
	op.TargetTerminalEID = ""
}

func samsungExtraTerminalFields(entitlement Entitlement, op ODSAOperation, target Device) ODSAOperation {
	if entitlement.SamsungExtraTerminalParams {
		op.TargetTerminalIMEIs = terminalIMEIs(target)
		op.TargetTerminalSN = target.SerialNumber
		op.TargetTerminalModel = target.Model
	}
	return op
}
