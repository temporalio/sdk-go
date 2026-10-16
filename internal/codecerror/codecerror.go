// Package codecerror tags Workflow Task failure requests made by a PayloadCodec.
// It is shared by converter and internal, so it must not import either.
package codecerror

import "errors"

type tag struct {
	err error
}

func (e *tag) Error() string { return e.err.Error() }
func (e *tag) Unwrap() error { return e.err }

// Tag marks a non-nil err as coming from a PayloadCodec.
func Tag(err error) error {
	return &tag{err: err}
}

// Has reports whether err was marked by Tag.
func Has(err error) bool {
	var t *tag
	return errors.As(err, &t)
}
