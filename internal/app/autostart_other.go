//go:build !windows

package app

func setAutoStart(enable bool) error {
	return nil
}
