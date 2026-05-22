//go:build !windows

package tray

func showFeedbackForm() (string, bool) {
	return "", false
}
