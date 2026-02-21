// Package api implements the HTTP REST API layer for the Arkeep server.
// It uses Chi as the router and exposes all resources under /api/v1.
// Authentication is enforced via JWT middleware on all routes except the
// public auth endpoints. Role-based access (admin vs user) is applied at
// the route level via the RequireRole middleware.
package api

import (
	"encoding/json"
	"net/http"
)

// envelope is the standard JSON response wrapper for all API responses.
// Successful responses wrap the payload in a "data" key; error responses
// use an "error" key with a human-readable message and an optional code.
//
// Success:  {"data": <payload>}
// Error:    {"error": {"message": "...", "code": "..."}}
type envelope map[string]any

// JSON writes a JSON-encoded response with the given status code.
// It sets Content-Type to application/json automatically.
func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// Ok writes a 200 OK response with the payload wrapped in {"data": payload}.
func Ok(w http.ResponseWriter, payload any) {
	JSON(w, http.StatusOK, envelope{"data": payload})
}

// Created writes a 201 Created response with the payload wrapped in {"data": payload}.
func Created(w http.ResponseWriter, payload any) {
	JSON(w, http.StatusCreated, envelope{"data": payload})
}

// NoContent writes a 204 No Content response with no body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// errorResponse is the shape of the "error" object in error responses.
type errorResponse struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// errJSON writes a JSON error response with the given status, message and
// optional error code. Code is a machine-readable string (e.g. "not_found",
// "validation_error") that the frontend can use for i18n or logic branching.
func errJSON(w http.ResponseWriter, status int, message, code string) {
	JSON(w, status, envelope{
		"error": errorResponse{
			Message: message,
			Code:    code,
		},
	})
}

// ErrBadRequest writes a 400 Bad Request error response.
func ErrBadRequest(w http.ResponseWriter, message string) {
	errJSON(w, http.StatusBadRequest, message, "bad_request")
}

// ErrUnauthorized writes a 401 Unauthorized error response.
func ErrUnauthorized(w http.ResponseWriter) {
	errJSON(w, http.StatusUnauthorized, "authentication required", "unauthorized")
}

// ErrForbidden writes a 403 Forbidden error response.
func ErrForbidden(w http.ResponseWriter) {
	errJSON(w, http.StatusForbidden, "insufficient permissions", "forbidden")
}

// ErrNotFound writes a 404 Not Found error response.
func ErrNotFound(w http.ResponseWriter) {
	errJSON(w, http.StatusNotFound, "resource not found", "not_found")
}

// ErrConflict writes a 409 Conflict error response.
func ErrConflict(w http.ResponseWriter, message string) {
	errJSON(w, http.StatusConflict, message, "conflict")
}

// ErrUnprocessable writes a 422 Unprocessable Entity error response.
// Used when the request is well-formed but fails business validation.
func ErrUnprocessable(w http.ResponseWriter, message string) {
	errJSON(w, http.StatusUnprocessableEntity, message, "validation_error")
}

// ErrInternal writes a 500 Internal Server Error response.
// The internal error detail is intentionally not exposed to the client.
func ErrInternal(w http.ResponseWriter) {
	errJSON(w, http.StatusInternalServerError, "an internal error occurred", "internal_error")
}

// decodeJSON decodes the request body into dst. Returns false and writes an
// appropriate error response if decoding fails, so callers can early-return.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		ErrBadRequest(w, "invalid request body: "+err.Error())
		return false
	}
	return true
}