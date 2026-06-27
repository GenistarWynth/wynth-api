package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupGeminiCodeAssistTest overrides the package-level URL and poll delay for the
// duration of a test, resetting them via t.Cleanup.
func setupGeminiCodeAssistTest(t *testing.T, serverURL string) {
	t.Helper()
	origURL := geminiCodeAssistBaseURL
	origDelay := geminiCodeAssistOnboardPollDelay
	geminiCodeAssistBaseURL = serverURL
	geminiCodeAssistOnboardPollDelay = 0
	t.Cleanup(func() {
		geminiCodeAssistBaseURL = origURL
		geminiCodeAssistOnboardPollDelay = origDelay
	})
}

// TestDetectGeminiCodeAssistProject_LoadCodeAssistReturnsProject verifies that when
// loadCodeAssist returns a non-empty cloudaicompanionProject the function returns it
// directly without calling onboardUser.
func TestDetectGeminiCodeAssistProject_LoadCodeAssistReturnsProject(t *testing.T) {
	const token = "ya29.test-access-token"
	const expectedProject = "projects/my-gcp-project"

	loadCalled := 0
	onboardCalled := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"), "must send Bearer token")
		assert.Equal(t, geminiOAuthDefaultUserAgent, r.Header.Get("User-Agent"), "must send GeminiCLI UA")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		switch {
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			loadCalled++
			// Verify request body carries correct metadata.
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			meta, _ := body["metadata"].(map[string]any)
			assert.Equal(t, "IDE_UNSPECIFIED", meta["ideType"])
			assert.Equal(t, "GEMINI", meta["pluginType"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cloudaicompanionProject": expectedProject,
				"currentTier":             map[string]any{"id": "free-tier"},
				"paidTier":                map[string]any{"id": ""},
			})
		case strings.HasSuffix(r.URL.Path, ":onboardUser"):
			onboardCalled++
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"done": true, "response": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	setupGeminiCodeAssistTest(t, srv.URL)

	got, err := DetectGeminiCodeAssistProject(context.Background(), token, "")
	require.NoError(t, err)
	assert.Equal(t, expectedProject, got)
	assert.Equal(t, 1, loadCalled, "loadCodeAssist must be called once")
	assert.Equal(t, 0, onboardCalled, "onboardUser must NOT be called when project is present")
}

// TestDetectGeminiCodeAssistProject_OnboardUserDoneTrue verifies that when
// loadCodeAssist returns an empty project but onboardUser immediately returns
// done=true with a project, the function returns that project.
func TestDetectGeminiCodeAssistProject_OnboardUserDoneTrue(t *testing.T) {
	const token = "ya29.onboard-done-token"
	const expectedProject = "projects/onboarded-project"

	onboardCalled := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cloudaicompanionProject": "",
				"currentTier":             map[string]any{"id": "free-tier"},
				"paidTier":                map[string]any{"id": "paid-tier"},
			})
		case strings.HasSuffix(r.URL.Path, ":onboardUser"):
			onboardCalled++
			// Verify request body: tierId should prefer paidTier.id
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "paid-tier", body["tierId"])
			meta, _ := body["metadata"].(map[string]any)
			assert.Equal(t, "IDE_UNSPECIFIED", meta["ideType"])
			assert.Equal(t, "GEMINI", meta["pluginType"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"done":     true,
				"response": map[string]any{"cloudaicompanionProject": expectedProject},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	setupGeminiCodeAssistTest(t, srv.URL)

	got, err := DetectGeminiCodeAssistProject(context.Background(), token, "")
	require.NoError(t, err)
	assert.Equal(t, expectedProject, got)
	assert.Equal(t, 1, onboardCalled, "onboardUser must be called once")
}

// TestDetectGeminiCodeAssistProject_OnboardUserPollConverges verifies that when
// onboardUser initially returns done=false (long-running op) and eventually
// returns done=true with a project, the function polls and returns the project.
func TestDetectGeminiCodeAssistProject_OnboardUserPollConverges(t *testing.T) {
	const token = "ya29.poll-token"
	const expectedProject = "projects/eventually-here"

	onboardCalled := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cloudaicompanionProject": "",
				"currentTier":             map[string]any{"id": "free-tier"},
				"paidTier":                map[string]any{"id": ""},
			})
		case strings.HasSuffix(r.URL.Path, ":onboardUser"):
			onboardCalled++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if onboardCalled < 2 {
				// First call: long-running op in progress.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"done":     false,
					"response": map[string]any{"cloudaicompanionProject": ""},
				})
			} else {
				// Second call: done.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"done":     true,
					"response": map[string]any{"cloudaicompanionProject": expectedProject},
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	setupGeminiCodeAssistTest(t, srv.URL)
	// Confirm the poll delay is 0 (set by setupGeminiCodeAssistTest).
	require.Equal(t, time.Duration(0), geminiCodeAssistOnboardPollDelay)

	got, err := DetectGeminiCodeAssistProject(context.Background(), token, "")
	require.NoError(t, err)
	assert.Equal(t, expectedProject, got)
	assert.Equal(t, 2, onboardCalled, "onboardUser must be called twice (1 pending + 1 done)")
}

// TestDetectGeminiCodeAssistProject_NonTwoxxError verifies that a non-2xx
// response from loadCodeAssist is returned as an error.
func TestDetectGeminiCodeAssistProject_NonTwoxxError(t *testing.T) {
	const token = "ya29.bad-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthenticated"}`))
	}))
	defer srv.Close()

	setupGeminiCodeAssistTest(t, srv.URL)

	_, err := DetectGeminiCodeAssistProject(context.Background(), token, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401", "error must contain the status code")
}
