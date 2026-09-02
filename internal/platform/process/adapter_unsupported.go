//go:build !windows && !darwin && !linux

package process

import "context"

type unsupportedAdapter struct{}

func newHostAdapter() Adapter { return unsupportedAdapter{} }

func (unsupportedAdapter) CreateSuspended(context.Context, Command, LaunchGeneration, OutputSink) (OwnedProcess, error) {
	return nil, ErrUnsupportedPlatform
}

func (unsupportedAdapter) NewKillOnCloseJob() (JobObject, error) {
	return nil, ErrUnsupportedPlatform
}
