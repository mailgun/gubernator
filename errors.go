package gubernator

type ClientError struct {
	errs []error
	err  error
}

func (ce *ClientError) Error() string {
	return ce.err.Error()
}

func (ce *ClientError) Err(err error) *ClientError {
	ce.err = err
	return ce
}

func (ce *ClientError) Add(err error) {
	ce.errs = append(ce.errs, err)
}

func (ce *ClientError) Size() int {
	return len(ce.errs)
}
