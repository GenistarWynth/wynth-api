package gemini

import (
	"fmt"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// vertexDefaultLocation is the default Vertex AI region used when the runtime
// location is empty. It mirrors the service-layer default.
const vertexDefaultLocation = "us-central1"

// isGeminiVertexServiceAccount reports whether the runtime account routes through
// the Vertex AI endpoint via a service-account credential. This is a strict no-op
// for every other Gemini path (api-key, OAuth, code_assist, antigravity), so the
// standard routing/auth is preserved byte-for-byte when this is false.
func isGeminiVertexServiceAccount(info *relaycommon.RelayInfo) bool {
	return info != nil && info.RuntimeVertexServiceAccount
}

// vertexAILocation normalizes the Vertex AI region, defaulting when empty.
func vertexAILocation(location string) string {
	loc := strings.TrimSpace(location)
	if loc == "" {
		return vertexDefaultLocation
	}
	return loc
}

// vertexAIBaseURL returns the regional Vertex AI base URL for the given location.
// The special "global" location maps to the non-regional host, mirroring the
// upstream Vertex behavior.
func vertexAIBaseURL(location string) string {
	loc := vertexAILocation(location)
	if loc == "global" {
		return "https://aiplatform.googleapis.com"
	}
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com", loc)
}
