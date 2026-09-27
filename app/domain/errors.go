package domain

import "errors"

// Sentinel domain errors. Every transport adapter maps these to its own
// wire format independently (HTTP status codes, JSON envelopes, CLI exit
// codes) via errors.Is / errors.As — no layer inspects error strings.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalid      = errors.New("invalid input")
	ErrRateLimited  = errors.New("rate limited")
	ErrTooLarge     = errors.New("too large")
)
