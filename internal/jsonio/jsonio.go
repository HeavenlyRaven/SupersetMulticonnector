// Package jsonio implements the stdin request / stdout response contract
// shared between fedctl's --json mode and the Python extension shim.
package jsonio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Error codes shared with the Python shim, which maps each to an HTTP status.
const (
	CodeInvalidRequest    = "INVALID_REQUEST"
	CodeInvalidName       = "INVALID_NAME"
	CodeHostNotAllowed    = "HOST_NOT_ALLOWED"
	CodeInvalidPort       = "INVALID_PORT"
	CodeInvalidPath       = "INVALID_PATH"
	CodeCredentialInArgs  = "CREDENTIAL_IN_ARGS"
	CodeSourceExists      = "SOURCE_EXISTS"
	CodeFileExists        = "FILE_EXISTS"
	CodeSourceNotFound    = "SOURCE_NOT_FOUND"
	CodeUnknownSourceType = "UNKNOWN_SOURCE_TYPE"
	CodeConnectionFailed  = "CONNECTION_FAILED"
	CodeHubUnavailable    = "HUB_UNAVAILABLE"
	CodeInternal          = "INTERNAL_ERROR"
)

// Exit codes. 0 is success; JSON mode always writes a Response either way,
// but a non-JSON caller can branch on these without parsing stdout.
const (
	ExitOK          = 0
	ExitUserError   = 1
	ExitInternalErr = 2
)

// Response is the envelope every --json command writes to stdout, always
// exactly one of Data or Error.
type Response struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorInfo `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Error adapts ErrorInfo to the error interface so it can travel through
// normal Go error-handling paths and still be turned back into a Response.
type Error struct {
	Info ErrorInfo
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Info.Code, e.Info.Message) }

func NewError(code, message, hint string) *Error {
	return &Error{Info: ErrorInfo{Code: code, Message: message, Hint: hint}}
}

// AsError extracts an *Error from err, wrapping unrecognized errors as
// internal errors. It never leaks err's message verbatim when it might
// contain a credential; callers that construct errors from user input must
// use NewError explicitly instead of letting a raw driver error surface.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var je *Error
	if ok := asError(err, &je); ok {
		return je
	}
	return NewError(CodeInternal, "internal error", "")
}

func asError(err error, target **Error) bool {
	type unwrapper interface{ Unwrap() error }
	for e := err; e != nil; {
		if je, ok := e.(*Error); ok {
			*target = je
			return true
		}
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// ReadRequest decodes exactly one JSON object from r into v. It rejects
// trailing garbage so a malformed or multi-document stdin is caught early
// rather than silently reading only the first object.
func ReadRequest(r io.Reader, v any) error {
	dec := json.NewDecoder(bufio.NewReader(r))
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return NewError(CodeInvalidRequest, "empty request body", "send a JSON object on stdin")
		}
		return NewError(CodeInvalidRequest, "malformed JSON request: "+err.Error(), "")
	}
	if dec.More() {
		return NewError(CodeInvalidRequest, "trailing data after JSON request", "send exactly one JSON object")
	}
	return nil
}

// WriteOK writes a successful envelope to w.
func WriteOK(w io.Writer, data any) error {
	return json.NewEncoder(w).Encode(Response{OK: true, Data: data})
}

// WriteError writes a failure envelope to w.
func WriteError(w io.Writer, code, message, hint string) error {
	return json.NewEncoder(w).Encode(Response{OK: false, Error: &ErrorInfo{Code: code, Message: message, Hint: hint}})
}

// WriteErr writes err as a failure envelope, converting it via AsError first.
func WriteErr(w io.Writer, err error) error {
	je := AsError(err)
	return json.NewEncoder(w).Encode(Response{OK: false, Error: &je.Info})
}
