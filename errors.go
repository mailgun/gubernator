package gubernator

type ClientError struct {
	Errs []error
	Err  error
}

func (ce *ClientError) Error() string {
	return ce.Err.Error()
}

func (ce *ClientError) Add(err error) {
	ce.Errs = append(ce.Errs, err)
}

func (ce *ClientError) IfErr(err error) error {
	if len(ce.Errs) == 0 {
		return nil
	}
	return err
}
