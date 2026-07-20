package claude

import (
	"github.com/QuantumNous/new-api/relay/channel/clientidentity"
)

// AnthropicOAuthBetaFeatures is the anthropic-beta value required for OAuth tokens.
// Shared with the channel client-identity preset so both emit the exact same flags.
const AnthropicOAuthBetaFeatures = clientidentity.AnthropicOAuthBetaFeatures

// mergeAnthropicBetaFlags merges required OAuth bundle flags with client-supplied beta
// flags into a single deduplicated, comma-separated string.
func mergeAnthropicBetaFlags(bundle, clientBeta string) string {
	return clientidentity.MergeAnthropicBetaFlags(bundle, clientBeta)
}
