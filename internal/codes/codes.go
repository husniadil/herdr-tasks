// Package codes holds the shared error-code vocabulary and exit statuses of
// the shared plugin contract (§6.3). The codes are semver-bound: a shipped
// code is never repurposed or removed, only new ones appended.
package codes

import "fmt"

// The §6.3 vocabulary. Every error that crosses a door carries one of these.
const (
	Usage       = "USAGE"
	NotFound    = "NOT_FOUND"
	Unavailable = "UNAVAILABLE"
	Timeout     = "TIMEOUT"
	Conflict    = "CONFLICT"
	Unsupported = "UNSUPPORTED"
	Forbidden   = "FORBIDDEN"
	Denied      = "DENIED"
	Unexpected  = "UNEXPECTED"
)

// exits maps a code to the process exit status the contract fixes for it.
var exits = map[string]int{
	Usage:       2,
	NotFound:    3,
	Unavailable: 4,
	Timeout:     5,
	Conflict:    6,
	Unsupported: 7,
	Forbidden:   8,
	Denied:      9,
	Unexpected:  1,
}

// Exit is the §6.3 exit status for a code. An unknown code is UNEXPECTED's 1,
// which is also what the contract says anything else is.
func Exit(code string) int {
	if e, ok := exits[code]; ok {
		return e
	}
	return exits[Unexpected]
}

// Error is an error carrying a contract code. Every layer below the doors
// returns one of these so a door never has to guess a code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds a coded error.
func New(code, message string) *Error { return &Error{Code: code, Message: message} }

// Errorf builds a coded error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
