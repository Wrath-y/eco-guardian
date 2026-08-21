package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLaunchOverridesUseDocumentedPrecedence(t *testing.T) {
	settings := Default()
	settings.Browser.AutoOpen = true
	updated, options, help, err := ApplyLaunchOverrides(settings, []string{"--browser-auto-open", "true", "--preferred-port", "48000"}, EnvironmentMap{
		"ECO_BROWSER_AUTO_OPEN": "false",
		"ECO_PREFERRED_PORT":    "47000",
	})
	if err != nil || help || !updated.Browser.AutoOpen || options.PreferredPort != 48000 {
		t.Fatalf("overrides = %#v %#v help=%v err=%v", updated, options, help, err)
	}
}

func TestLaunchOverridesRejectRemoteListenersAndDoNotAcceptSecrets(t *testing.T) {
	_, _, _, err := ApplyLaunchOverrides(Default(), []string{"--graph-endpoint", "http://example.test:8080"}, EnvironmentMap{})
	if err == nil {
		t.Fatal("remote graph endpoint was accepted")
	}
	settings, options, _, err := ApplyLaunchOverrides(Default(), nil, EnvironmentMap{
		"ECO_API_KEY":        "super-secret",
		"ECO_LISTEN_HOST":    "0.0.0.0",
		"ECO_PREFERRED_PORT": "49000",
	})
	if err != nil || options.PreferredPort != 49000 || strings.Contains(strings.ToLower(fmt.Sprintf("%#v", settings)), "secret") {
		t.Fatalf("unsafe environment result settings=%#v options=%#v err=%v", settings, options, err)
	}
}

func TestLaunchHelpListsOnlyBoundedOverrides(t *testing.T) {
	help := Help()
	if !strings.Contains(help, "command line > environment > settings.json > compiled defaults") || strings.Contains(strings.ToLower(help), "token") || strings.Contains(strings.ToLower(help), "secret") {
		t.Fatalf("help = %q", help)
	}
	for _, flag := range []string{"--browser-auto-open", "--package-mode", "--graph-endpoint", "--preferred-port"} {
		if !strings.Contains(help, flag) {
			t.Fatalf("help missing %s: %q", flag, help)
		}
	}
}
