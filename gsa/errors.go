package gsa

import "fmt"

// AuthenticationError distinguishes transport rejection from a GSA error.
// Codes are reported without claiming a protocol failure is a wrong password.
// The raw server message is available to callers but is omitted from Error logs.
type AuthenticationError struct {
	Operation     string
	HTTPStatus    int
	Code          int64
	Protocol      string
	ServerMessage string
}

func (e *AuthenticationError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("gsa: %s HTTP status %d", e.Operation, e.HTTPStatus)
	}
	if e.Protocol != "" {
		return fmt.Sprintf("gsa: %s rejected (protocol=%s, ec=%d)", e.Operation, e.Protocol, e.Code)
	}
	return fmt.Sprintf("gsa: %s rejected (ec=%d)", e.Operation, e.Code)
}

// Explain appends Apple's own message for the rejection. Error() leaves it out
// because it can quote account details, but a diagnostic run needs it: an
// "init rejected (ec=-20101)" says nothing about whether the cause is a
// throttled account, a rejected device identity, or a bad request.
func (e *AuthenticationError) Explain() string {
	if e.ServerMessage == "" {
		return e.Error()
	}
	return e.Error() + " — server said: " + e.ServerMessage
}

func checkStatus(response map[string]any, operation, protocol string) (map[string]any, error) {
	status, ok := response["Status"].(map[string]any)
	if !ok {
		status = response
	}
	code, ok := asInt64(status["ec"])
	if !ok {
		return nil, fmt.Errorf("gsa: %s response missing integer error code", operation)
	}
	if code != 0 {
		message, _ := status["em"].(string)
		return nil, &AuthenticationError{Operation: operation, Code: code, Protocol: protocol, ServerMessage: message}
	}
	return status, nil
}
