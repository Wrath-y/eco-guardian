package credential

import "testing"

func TestCredentialTargetIsStableAndRejectsPathInjection(t *testing.T) {
	target, err := targetName("openai-compatible")
	if err != nil || target != "EcoGuardian/AI/openai-compatible" {
		t.Fatalf("target=%q err=%v", target, err)
	}
	for _, provider := range []string{"", "../secret", "bad\\name", "bad\nname"} {
		if _, err := targetName(provider); err == nil {
			t.Fatalf("provider %q was accepted", provider)
		}
	}
}
