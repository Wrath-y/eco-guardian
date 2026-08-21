package config

import "errors"

// Problem is the RFC 9457-compatible public representation of invalid local
// settings. It deliberately identifies fields but never echoes paths or input
// values, which may contain user-specific information.
type Problem struct {
	Type   string         `json:"type"`
	Title  string         `json:"title"`
	Status int            `json:"status"`
	Errors []FieldProblem `json:"errors"`
}

type FieldProblem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func ProblemFor(err error) Problem {
	var validation ValidationError
	if errors.As(err, &validation) {
		return Problem{
			Type:   "https://eco-guardian.local/problems/invalid-settings",
			Title:  "Invalid settings",
			Status: 400,
			Errors: []FieldProblem{{Field: validation.Field, Message: validation.Message}},
		}
	}
	return Problem{Type: "https://eco-guardian.local/problems/settings-write-failed", Title: "Settings could not be saved", Status: 500}
}
