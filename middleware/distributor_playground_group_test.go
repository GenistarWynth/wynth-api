package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newPlaygroundGroupContext builds a /pg/chat/completions request that explicitly
// targets the non-selectable "admin_only" group. The caller's user group is
// "default", which does NOT include "admin_only" in the user-selectable set.
func newPlaygroundGroupContext(t *testing.T, role int) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/pg/chat/completions",
		strings.NewReader(`{"model":"gpt-test","group":"admin_only"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	// Playground requests pass through UserAuth, which sets the role in context.
	ctx.Set("role", role)
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	return ctx
}

// TestDistributePlaygroundGroupAdminBypass verifies that, in the playground, an
// administrator may route through a group that is not user-selectable, while a
// regular user is denied. This mirrors the documented behavior that non-selectable
// groups remain usable by administrators.
func TestDistributePlaygroundGroupAdminBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The regular-user rejection path renders an i18n message; load the bundle so
	// abortWithOpenAiMessage -> i18n.T does not dereference a nil localizer.
	require.NoError(t, i18n.Init())
	withDistributorStrictPriorityDB(t)

	prevUsable := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"default group"}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(prevUsable))
	})

	insertDistributorStrictPriorityCandidate(t, 1, "admin_only")
	model.InitChannelCache()

	// Administrator: the explicit non-selectable group is accepted and routed.
	adminCtx := newPlaygroundGroupContext(t, common.RoleAdminUser)
	Distribute()(adminCtx)
	require.False(t, adminCtx.IsAborted(),
		"administrators must be able to use a non-selectable group in the playground")
	require.Equal(t, 1, common.GetContextKeyInt(adminCtx, constant.ContextKeyChannelId))

	// Regular user: the explicit non-selectable group is rejected.
	userCtx := newPlaygroundGroupContext(t, common.RoleCommonUser)
	Distribute()(userCtx)
	require.True(t, userCtx.IsAborted(),
		"regular users must not be able to use a non-selectable group in the playground")
	require.Equal(t, http.StatusForbidden, userCtx.Writer.Status())
}
