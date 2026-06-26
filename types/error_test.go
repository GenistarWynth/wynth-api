package types

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAPIErrorUpstreamResponse(t *testing.T) {
	t.Run("nil receiver returns zero values", func(t *testing.T) {
		var e *NewAPIError
		assert.Nil(t, e.GetUpstreamHeader())
		assert.Nil(t, e.GetUpstreamBody())
		assert.Equal(t, 0, e.GetUpstreamStatusCode())
	})

	t.Run("SetUpstreamResponse on nil is a no-op", func(t *testing.T) {
		var e *NewAPIError
		require.NotPanics(t, func() {
			e.SetUpstreamResponse(http.Header{"X-Test": []string{"v"}}, []byte("body"), 429)
		})
	})

	t.Run("non-nil returns set values", func(t *testing.T) {
		e := &NewAPIError{}
		h := http.Header{"X-Codex-Primary-Reset-After-Seconds": []string{"30"}}
		body := []byte(`{"error":"rate limited"}`)
		e.SetUpstreamResponse(h, body, 429)

		assert.Equal(t, "30", e.GetUpstreamHeader().Get("X-Codex-Primary-Reset-After-Seconds"))
		assert.Equal(t, body, e.GetUpstreamBody())
		assert.Equal(t, 429, e.GetUpstreamStatusCode())
	})
}
