// Package browser opens only the canonical Eco Guardian loopback URL through
// the operating system's default browser handler.
package browser

import (
	"errors"
	"net"
	"net/url"
	"strconv"
)

var (
	ErrInvalidURL  = errors.New("browser URL must be canonical loopback HTTP")
	ErrUnsupported = errors.New("default browser launch is unsupported")
)

func validateURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return ErrInvalidURL
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Host != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		return ErrInvalidURL
	}
	return nil
}

type Default struct{}

func NewDefault() Default { return Default{} }
