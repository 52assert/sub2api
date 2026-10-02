//go:build linux && !amd64 && !arm64

package main

import "errors"

func installABI2Filter() error {
	return errors.New("Landlock ABI 2 requires the truncation filter on Linux amd64 or arm64")
}
