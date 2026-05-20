package route

import (
	"errors"
	"fmt"
)

type ConflictError struct {
	Message string
}

func (e ConflictError) Error() string {
	return e.Message
}

func NewConflictError(format string, args ...any) error {
	return ConflictError{Message: fmt.Sprintf(format, args...)}
}

func IsConflict(err error) bool {
	var conflict ConflictError
	return errors.As(err, &conflict)
}
