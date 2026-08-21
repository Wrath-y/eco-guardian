package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Environment is deliberately narrow so launch parsing cannot enumerate or
// serialize process environment values, which may include credentials.
type Environment interface {
	LookupEnv(string) (string, bool)
}

type LaunchOptions struct {
	PreferredPort int
}

type overrideDefinition struct {
	Flag string
	Env  string
	Use  string
}

var launchOverrides = []overrideDefinition{
	{Flag: "--browser-auto-open", Env: "ECO_BROWSER_AUTO_OPEN", Use: "true or false"},
	{Flag: "--package-mode", Env: "ECO_PACKAGE_MODE", Use: "development, complete, or lightweight"},
	{Flag: "--graph-endpoint", Env: "ECO_GRAPH_ENDPOINT", Use: "unauthenticated loopback http(s) URL"},
	{Flag: "--preferred-port", Env: "ECO_PREFERRED_PORT", Use: "loopback TCP port (1-65535)"},
}

// Help documents the fixed precedence: command line, then environment, then
// validated settings, then compiled defaults. No secret override exists.
func Help() string {
	var lines []string
	lines = append(lines, "Precedence: command line > environment > settings.json > compiled defaults.")
	for _, definition := range launchOverrides {
		lines = append(lines, definition.Flag+" ("+definition.Env+"): "+definition.Use)
	}
	return strings.Join(lines, "\n")
}

// ApplyLaunchOverrides applies only allowlisted non-secret overrides. The
// listener host is intentionally absent and therefore cannot become remote.
func ApplyLaunchOverrides(settings Settings, args []string, environment Environment) (Settings, LaunchOptions, bool, error) {
	options := LaunchOptions{}
	for _, definition := range launchOverrides {
		if value, ok := environment.LookupEnv(definition.Env); ok {
			if err := applyOverride(&settings, &options, definition.Flag, value); err != nil {
				return Settings{}, LaunchOptions{}, false, err
			}
		}
	}
	for index := 0; index < len(args); index++ {
		if args[index] == "--help" || args[index] == "-h" {
			return settings, options, true, nil
		}
		if index+1 == len(args) {
			return Settings{}, LaunchOptions{}, false, ValidationError{Field: "launch.arguments", Message: "is missing an option value"}
		}
		if err := applyOverride(&settings, &options, args[index], args[index+1]); err != nil {
			return Settings{}, LaunchOptions{}, false, err
		}
		index++
	}
	if err := Validate(settings); err != nil {
		return Settings{}, LaunchOptions{}, false, err
	}
	return settings, options, false, nil
}

func applyOverride(settings *Settings, options *LaunchOptions, flag, value string) error {
	switch flag {
	case "--browser-auto-open":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return ValidationError{Field: "launch.browser_auto_open", Message: "must be true or false"}
		}
		settings.Browser.AutoOpen = parsed
	case "--package-mode":
		settings.Package.Mode = PackageMode(value)
	case "--graph-endpoint":
		settings.Graph.Endpoint = value
		settings.Graph.Mode = GraphExternal
	case "--preferred-port":
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return ValidationError{Field: "launch.preferred_port", Message: "must be between 1 and 65535"}
		}
		options.PreferredPort = port
	default:
		return ValidationError{Field: "launch.arguments", Message: "contains an unsupported option"}
	}
	return nil
}

type EnvironmentMap map[string]string

func (e EnvironmentMap) LookupEnv(key string) (string, bool) {
	value, ok := e[key]
	return value, ok
}

func (o LaunchOptions) String() string {
	return fmt.Sprintf("preferred_port=%d", o.PreferredPort)
}
