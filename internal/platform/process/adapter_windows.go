//go:build windows

package process

func newHostAdapter() Adapter { return newWindowsAdapter() }
