package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUpstreamSourceTurnstileError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"newapi own turnstile 200", newAPIRequestError{StatusCode: http.StatusOK, Message: "Turnstile token 为空"}, true},
		{"newapi turnstile verify failed", newAPIRequestError{StatusCode: http.StatusOK, Message: "Turnstile 校验失败，请刷新重试！"}, true},
		{"cf edge just a moment", errors.New("decode upstream response failed: just a moment... cf-ray"), true},
		{"cf challenge platform", errors.New("challenge-platform blocked"), true},
		{"ordinary auth error", newAPIRequestError{StatusCode: http.StatusUnauthorized, Message: "invalid access token"}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isUpstreamSourceTurnstileError(tc.err))
		})
	}
}
