// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"net/http"

	"github.com/pkg/errors"
)

// apiError is an error with the HTTP status to answer with. Its message is shown to the user.
type apiError struct {
	status int
	err    error
}

func (e *apiError) Error() string {
	return e.err.Error()
}

func newAPIError(status int, msg string) *apiError {
	return &apiError{status: status, err: errors.New(msg)}
}

// statusFor returns the HTTP status for an error returned by the plugin's operations.
func statusFor(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.status
	}
	return http.StatusInternalServerError
}
