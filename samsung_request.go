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

func samsungManageSubscriptionOperation(entitlement Entitlement, source, target Device, answer UserInputResult) ODSAOperation {
	op := baseManageSubscriptionOperation(samsungManageSubscriptionOperationType(entitlement), answer)
	if transferUsesSourceCredential(entitlement) {
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalEID = target.EID
		op.TerminalICCID = source.ICCID
		op.TargetTerminalICCID = target.ICCID
	} else {
		op.TerminalEID = target.EID
		op.TerminalICCID = target.ICCID
		op.TargetTerminalID = target.IMEI
		op.TargetTerminalICCID = target.ICCID
		if samsungDigitalizedTempToken4FF(entitlement) {
			op.TerminalICCID = ""
		}
	}
	addSamsungOldTerminalFields(&op, source)
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
	setSamsungAcquireConfigurationEID(&op, entitlement, target)
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
	return SamsungSubscriptionD2DTransfer
}

func setSamsungAcquireConfigurationEID(op *ODSAOperation, entitlement Entitlement, target Device) {
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

func addSamsungOldTerminalFields(op *ODSAOperation, source Device) {
	op.OldTerminalID = source.IMEI
	op.OldTerminalICCID = source.ICCID
}
