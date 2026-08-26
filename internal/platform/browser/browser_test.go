package browser

import (
	"context"
	"errors"
	"testing"
)

func TestBrowserURLAdmissionAcceptsOnlyCanonicalRuntimeURL(t *testing.T) {
	if err := validateURL("http://127.0.0.1:31888"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"https://127.0.0.1:31888", "http://localhost:31888", "http://127.0.0.2:31888",
		"http://127.0.0.1", "http://127.0.0.1:0", "http://user@127.0.0.1:31888", "http://127.0.0.1:31888/?next=secret",
	} {
		if err := validateURL(value); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("value=%q err=%v", value, err)
		}
	}
}

func TestBrowserHonorsCanceledContextBeforePlatformCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Default{}).OpenBrowser(ctx, "http://127.0.0.1:31888"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
