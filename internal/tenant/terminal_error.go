package tenant

import "errors"

// TerminalProvisionError wraps a provisioning error that no retry can fix —
// invalid or conflicting user input (e.g. a Tenant DNS Suffix change against
// the immutable stored one). Device-login polling retries transient
// provisioning failures by design; a terminal error must instead deny the
// login so the client fails fast with the message rather than polling to
// timeout while the server re-attempts a provisioning that can never succeed.
type TerminalProvisionError struct{ Err error }

func (e TerminalProvisionError) Error() string { return e.Err.Error() }

func (e TerminalProvisionError) Unwrap() error { return e.Err }

// IsTerminalProvisionError reports whether err (anywhere in its chain) is a
// TerminalProvisionError.
func IsTerminalProvisionError(err error) bool {
	var terminal TerminalProvisionError
	return errors.As(err, &terminal)
}
