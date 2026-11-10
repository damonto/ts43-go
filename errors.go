package ts43

// CarrierDeniedError retains an operation's carrier rejection, including
// terminal service states returned by AcquireConfiguration.
type CarrierDeniedError struct {
	Operation Operation
	Result    *ODSAResponse
}

func (e *CarrierDeniedError) Error() string {
	if e.Result != nil && e.Result.GeneralErrorText != "" {
		return e.Operation.String() + " denied: " + e.Result.GeneralErrorText
	}
	if e.Operation == OperationAcquireConf && e.Result != nil {
		switch e.Result.ServiceStatus {
		case ServiceStatusDeactivated:
			return e.Operation.String() + " denied: service is deactivated"
		case ServiceStatusDeactivatedNoReuse:
			return e.Operation.String() + " denied: service is deactivated with no ICCID reuse"
		}
	}
	return e.Operation.String() + " denied"
}
