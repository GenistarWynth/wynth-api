package common

import kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"

// SanitizeSecrets delegates to RelayKit so relay errors and application logs
// share one bounded credential-redaction implementation.
func SanitizeSecrets(text string) string {
	return kitutil.SanitizeSecrets(text)
}
