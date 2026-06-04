package ts43

type CarrierDeniedError struct {
	Operation Operation
	Result    *ODSAResponse
}

func (e *CarrierDeniedError) Error() string {
	if e.Result != nil && e.Result.GeneralErrorText != "" {
		return e.Operation.String() + " denied: " + e.Result.GeneralErrorText
	}
	return e.Operation.String() + " denied"
}
