// Package errors re-exports the shared error contract from lib-agent-output so
// the rest of agent-slack keeps the internal/errors import path while the
// implementation lives in one place. (Migration shim — call sites can later be
// pointed at lib-agent-output directly and this package deleted.)
package errors

import (
	stderrors "errors"

	out "github.com/shhac/lib-agent-output"
)

type (
	FixableBy = out.FixableBy
	// APIError is the family name for the shared output.Error type.
	APIError = out.Error
)

const (
	FixableByAgent = out.FixableByAgent
	FixableByHuman = out.FixableByHuman
	FixableByRetry = out.FixableByRetry
)

var (
	New  = out.New
	Newf = out.Newf
	// Wrap is nil-safe in lib-agent-output v0.4.2+, matching the old local guard.
	Wrap = out.Wrap
)

// As keeps the loose target signature the rest of the package expects.
func As(err error, target any) bool { return stderrors.As(err, target) }
