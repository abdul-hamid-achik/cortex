// Package httpx holds small HTTP helpers shared by the clients in this module.
package httpx

import "net/http"

// IsSafe reports whether method is "safe" in the RFC 9110 sense: it is
// read-only and asks the server to change nothing. Safe methods are a subset
// of the idempotent ones, so this is not the right test for "may this request
// be sent twice".
func IsSafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}
