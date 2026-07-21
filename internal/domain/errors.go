package domain

import "fmt"

// ValidationError maps to HTTP 400.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func Validationf(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}
