package errs

type ErrorCode int

type BizError struct {
	Code ErrorCode
	Msg  string
}

func (e *BizError) Error() string {
	return e.Msg
}

func New(code ErrorCode, msg string, err error) error {
	return &BizError{
		Code: code,
		Msg:  msg,
	}
}
