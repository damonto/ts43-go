package ts43

func temporaryTokenOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungTemporaryTokenOperation(source)
	}
	return googleTemporaryTokenOperation(entitlement, target)
}

func checkEligibilityOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		op := samsungCheckEligibilityOperation(source, target)
		return samsungExtraTerminalFields(entitlement, op, target)
	}
	return googleCheckEligibilityOperation(entitlement, target)
}

func manageSubscriptionRequestDevice(entitlement Entitlement, source, target Device) Device {
	if samsungDigitalizedTransfer(entitlement) {
		return target
	}
	if transferUsesSourceCredential(entitlement) || (entitlement.Channel != EntitlementChannelSamsung && entitlement.TransferType == TransferTypeTemporaryToken) {
		return source
	}
	return target
}

func manageSubscriptionOperation(entitlement Entitlement, req ManageSubscriptionRequest) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		op := samsungManageSubscriptionOperation(entitlement, req)
		return samsungExtraTerminalFields(entitlement, op, req.Target)
	}
	return googleManageSubscriptionOperation(entitlement, req)
}

func acquireConfigurationRequestDevice(entitlement Entitlement, source, target Device) Device {
	if transferUsesSourceCredential(entitlement) {
		return source
	}
	return target
}

func transferUsesSourceCredential(entitlement Entitlement) bool {
	return !samsungDigitalizedTransfer(entitlement) &&
		IsSourceCredentialTransferType(entitlement.TransferType)
}

func acquireConfigurationOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		op := samsungAcquireConfigurationOperation(entitlement, source, target)
		return samsungExtraTerminalFields(entitlement, op, target)
	}
	return googleAcquireConfigurationOperation(entitlement, source, target)
}

func updateSubscriptionOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		op := samsungUpdateSubscriptionOperation(source, target)
		return samsungExtraTerminalFields(entitlement, op, target)
	}
	return googleUpdateSubscriptionOperation(entitlement, source, target)
}

func activationConfigurationOperation(entitlement Entitlement, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		op := samsungActivationConfigurationOperation(target)
		return samsungExtraTerminalFields(entitlement, op, target)
	}
	return googleActivationConfigurationOperation(entitlement, target)
}

func baseManageSubscriptionOperation(operationType OperationType, answer UserInputResult) ODSAOperation {
	return ODSAOperation{
		Operation:        OperationManage,
		OperationType:    operationType,
		MessageResponse:  answer.Response,
		MessageButton:    answer.Button,
		MessageButtonSet: answer.Button != MessageButtonUnknown,
	}
}
