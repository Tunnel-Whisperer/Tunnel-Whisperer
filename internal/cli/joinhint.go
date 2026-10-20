package cli

import "fmt"

const joinHintException = "(Windows: run as Administrator, without sudo. Neither is needed when TW_CONFIG_DIR points to a directory you own.)"

// joinCommandHint is the copy-paste line issuers show the enrollee. The default
// config dir is system-owned, so a bare `tw join` fails its writability
// preflight and the enrollee wrongly assumes the one-time code is burned.
func joinCommandHint(relayHost, code string) string {
	return fmt.Sprintf("\n  sudo tw join %s %s\n\n  %s\n\n", relayHost, code, joinHintException)
}

// joinPreflight runs the config-dir writability check before any relay
// contact, so a failure here leaves the invite code unused.
func joinPreflight(relayHost, code string) error {
	if err := requireWritableConfig(); err != nil {
		return fmt.Errorf("%w — the invite code is still unused (nothing was sent to the relay); retry as: sudo tw join %s %s %s", err, relayHost, code, joinHintException)
	}
	return nil
}
