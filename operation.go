package ts43

func temporaryTokenOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungTemporaryTokenOperation(source)
	}
	return googleTemporaryTokenOperation(entitlement, target)
}

func checkEligibilityOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungCheckEligibilityOperation(source, target)
	}
	return googleCheckEligibilityOperation(entitlement, target)
}

func manageSubscriptionRequestDevice(entitlement Entitlement, source, target Device) Device {
	if samsungDigitalizedTransfer(entitlement) {
		return target
	}
	if transferUsesSourceCredential(entitlement) || entitlement.TransferType == TransferTypeTemporaryToken {
		return source
	}
	return target
}

func manageSubscriptionOperation(entitlement Entitlement, source, target Device, answer UserInputResult) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungManageSubscriptionOperation(entitlement, source, target, answer)
	}
	return googleManageSubscriptionOperation(entitlement, source, target, answer)
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
		return samsungAcquireConfigurationOperation(entitlement, source, target)
	}
	return googleAcquireConfigurationOperation(entitlement, source, target)
}

func updateSubscriptionOperation(entitlement Entitlement, source, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungUpdateSubscriptionOperation(source, target)
	}
	return googleUpdateSubscriptionOperation(entitlement, source, target)
}

func activationConfigurationOperation(entitlement Entitlement, target Device) ODSAOperation {
	if entitlement.Channel == EntitlementChannelSamsung {
		return samsungActivationConfigurationOperation(target)
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
