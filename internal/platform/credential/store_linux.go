//go:build linux

package credential

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

// LinuxStore delegates persistence to the freedesktop Secret Service through
// secret-tool. Credentials are passed only on stdin, never in argv or files.
// When Secret Service is absent, CredentialResolver can still use its
// non-persistent OPENAI_API_KEY environment fallback.
type LinuxStore struct{}

func NewLinuxStore() *LinuxStore   { return &LinuxStore{} }
func NewDefaultStore() *LinuxStore { return NewLinuxStore() }
func NewWindowsStore() *LinuxStore { return NewLinuxStore() }

func (*LinuxStore) Put(ctx context.Context, provider string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil || !validLinuxCredential(value) {
		return aiprovider.ErrCredentialInvalid
	}
	launcher, err := exec.LookPath("secret-tool")
	if err != nil {
		return errors.Join(ErrUnsupported, err)
	}
	input := append(append([]byte(nil), value...), '\n')
	defer clear(input)
	command := exec.CommandContext(ctx, launcher, "store", "--label=EcoGuardian AI Provider", "service", "EcoGuardian", "account", target)
	command.Stdin = bytes.NewReader(input)
	if err = command.Run(); err != nil {
		return fmt.Errorf("Linux Secret Service write failed: %w", err)
	}
	return nil
}

func (*LinuxStore) Get(ctx context.Context, provider string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := targetName(provider)
	if err != nil {
		return nil, aiprovider.ErrCredentialInvalid
	}
	launcher, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil, aiprovider.ErrCredentialNotFound
	}
	output, err := exec.CommandContext(ctx, launcher, "lookup", "service", "EcoGuardian", "account", target).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, aiprovider.ErrCredentialNotFound
		}
		return nil, fmt.Errorf("Linux Secret Service read failed: %w", err)
	}
	value := []byte(strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r"))
	clear(output)
	if !validLinuxCredential(value) {
		clear(value)
		return nil, aiprovider.ErrCredentialNotFound
	}
	return value, nil
}

func (*LinuxStore) Delete(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := targetName(provider)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	launcher, err := exec.LookPath("secret-tool")
	if err != nil {
		return errors.Join(ErrUnsupported, err)
	}
	if err = exec.CommandContext(ctx, launcher, "clear", "service", "EcoGuardian", "account", target).Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return aiprovider.ErrCredentialNotFound
		}
		return fmt.Errorf("Linux Secret Service delete failed: %w", err)
	}
	return nil
}

func validLinuxCredential(value []byte) bool {
	return len(value) > 0 && len(value) <= aiprovider.MaxCredentialBytes && bytes.IndexAny(value, "\x00\r\n") < 0
}
