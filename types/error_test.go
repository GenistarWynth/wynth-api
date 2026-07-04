package types

import (
	"errors"
	"net/http"
	"strings"
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

func TestNewUpstreamBodyDecodeError(t *testing.T) {
	decodeErr := errors.New("invalid character '<' looking for beginning of value")
	makeResp := func(status int, contentType string) *http.Response {
		header := http.Header{}
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		return &http.Response{StatusCode: status, Header: header}
	}

	t.Run("html challenge page surfaces status content-type and title", func(t *testing.T) {
		body := []byte("<!DOCTYPE html><html><head>\n<title>  Just a moment... </title></head><body>...</body></html>")
		e := NewUpstreamBodyDecodeError(decodeErr, makeResp(http.StatusServiceUnavailable, "text/html; charset=utf-8"), body)
		require.NotNil(t, e)
		msg := e.Error()
		assert.Contains(t, msg, "HTTP 503")
		assert.Contains(t, msg, "text/html")
		assert.Contains(t, msg, "page title: Just a moment...")
		// classification must stay unchanged: internal error, bad_response_body
		assert.Equal(t, http.StatusInternalServerError, e.StatusCode)
		assert.Equal(t, ErrorCodeBadResponseBody, e.GetErrorCode())
	})

	t.Run("non-html body is collapsed and truncated", func(t *testing.T) {
		long := strings.Repeat("plain  text ", 40)
		e := NewUpstreamBodyDecodeError(decodeErr, makeResp(http.StatusOK, ""), []byte(long))
		require.NotNil(t, e)
		msg := e.Error()
		assert.Contains(t, msg, "HTTP 200")
		assert.Contains(t, msg, "Content-Type: unknown")
		assert.Contains(t, msg, "…")
		assert.NotContains(t, msg, "  ") // whitespace collapsed
	})

	t.Run("empty body falls back to the decode error", func(t *testing.T) {
		e := NewUpstreamBodyDecodeError(decodeErr, makeResp(http.StatusBadGateway, "text/html"), nil)
		require.NotNil(t, e)
		assert.Contains(t, e.Error(), "HTTP 502")
		assert.Contains(t, e.Error(), decodeErr.Error())
	})

	t.Run("nil response keeps the plain decode error", func(t *testing.T) {
		e := NewUpstreamBodyDecodeError(decodeErr, nil, nil)
		require.NotNil(t, e)
		assert.Equal(t, decodeErr.Error(), e.Error())
	})
}
