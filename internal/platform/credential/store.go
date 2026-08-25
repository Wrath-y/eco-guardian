// Package credential adapts platform credential storage to the AI Provider
// credential port. It never persists credential material in settings or a
// project database.
package credential

import (
	"errors"
	"strings"
)

var ErrUnsupported = errors.New("platform credential manager is unsupported")

func targetName(provider string) (string, error) {
	if provider == "" || strings.ContainsAny(provider, "\\/\r\n\x00") {
		return "", errors.New("invalid credential provider")
	}
	return "EcoGuardian/AI/" + provider, nil
}
