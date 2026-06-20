package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withUserSelectableGroupFixture configures a non-selectable group ("admin_only")
// that exists in the group ratio table but is intentionally absent from the
// user-selectable set. State is restored after the test.
func withUserSelectableGroupFixture(t *testing.T) {
	t.Helper()
	prevRatio := ratio_setting.GroupRatio2JSONString()
	prevUsable := setting.UserUsableGroups2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"admin_only":2}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(prevRatio))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(prevUsable))
	})
}

func invokeGetUserGroups(t *testing.T, userId, role int) map[string]map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/groups", nil)
	ctx.Set("id", userId)
	ctx.Set("role", role)

	GetUserGroups(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Success bool                              `json:"success"`
		Data    map[string]map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	return resp.Data
}

// TestGetUserGroupsAdminSeesNonSelectableGroups locks in the documented contract:
// "Users only see groups marked as user selectable. Non-selectable groups can
// still be assigned by administrators." An administrator must be offered every
// configured group (so they can assign it to a token), while a regular user is
// limited to the user-selectable set.
func TestGetUserGroupsAdminSeesNonSelectableGroups(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id: 1, Username: "admin", AffCode: "aff-admin",
		Group: "default", Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id: 2, Username: "common", AffCode: "aff-common",
		Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error)
	withUserSelectableGroupFixture(t)

	adminGroups := invokeGetUserGroups(t, 1, common.RoleAdminUser)
	assert.Contains(t, adminGroups, "default")
	assert.Contains(t, adminGroups, "admin_only",
		"administrators must be offered non-selectable groups so they can assign them")

	commonGroups := invokeGetUserGroups(t, 2, common.RoleCommonUser)
	assert.Contains(t, commonGroups, "default")
	assert.NotContains(t, commonGroups, "admin_only",
		"regular users must not see groups that are not user-selectable")
}
