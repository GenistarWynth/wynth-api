package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The override cache is a process-wide singleton, so each test resets the token ids it
// touches before and after running to stay independent of source order.
func withCleanOverrideCache(t *testing.T, tokenIds ...int) {
	t.Helper()
	clear := func() {
		for _, id := range tokenIds {
			_ = service.ClearTokenChannelOverride(id)
		}
	}
	clear()
	t.Cleanup(clear)
}

func seedForceChannelChannels(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&model.Channel{Id: 100, Type: constant.ChannelTypeOpenAI, Key: "sk-x", Status: common.ChannelStatusEnabled, Name: "def", Group: "default", Models: "gpt-test"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 101, Type: constant.ChannelTypeOpenAI, Key: "sk-x", Status: common.ChannelStatusEnabled, Name: "vip", Group: "vip", Models: "gpt-test"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 102, Type: constant.ChannelTypeOpenAI, Key: "sk-x", Status: common.ChannelStatusManuallyDisabled, Name: "dis", Group: "default", Models: "gpt-test"}).Error)
}

func forceChannelCtx(t *testing.T, targetId, tid, actorRole int, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	raw, err := common.Marshal(body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/"+itoa(targetId)+"/tokens/"+itoa(tid)+"/force-channel", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: itoa(targetId)}, {Key: "tid", Value: itoa(tid)}}
	c.Set("id", 1)
	c.Set("role", actorRole)
	return c, rec
}

func forceChannelNoBodyCtx(method string, targetId, tid, actorRole int) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/api/user/"+itoa(targetId)+"/tokens/"+itoa(tid)+"/force-channel", nil)
	c.Params = gin.Params{{Key: "id", Value: itoa(targetId)}, {Key: "tid", Value: itoa(tid)}}
	c.Set("id", 1)
	c.Set("role", actorRole)
	return c, rec
}

func decodeSuccess(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	var resp struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.Success
}

func TestAdminForceTokenChannel_SetsValidSameGroup(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)

	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 100, TTLSeconds: 600})
	AdminForceTokenChannel(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, decodeSuccess(t, rec))

	ov, found := service.GetTokenChannelOverride(10)
	require.True(t, found)
	assert.Equal(t, 100, ov.ChannelId)
	assert.Equal(t, 1, ov.SetByUserId, "override records the acting admin id")
}

func TestAdminForceTokenChannel_RejectsUnknownChannel(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)

	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 999})
	AdminForceTokenChannel(c)

	assert.False(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(10)
	assert.False(t, found, "cache must be untouched on validation failure")
}

func TestAdminForceTokenChannel_RejectsDisabledChannel(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)

	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 102})
	AdminForceTokenChannel(c)

	assert.False(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(10)
	assert.False(t, found)
}

func TestAdminForceTokenChannel_RejectsWrongGroup(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)

	// alice's token has no group override, so effective group is her user group "default";
	// channel 101 is in "vip" only.
	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 101})
	AdminForceTokenChannel(c)

	assert.False(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(10)
	assert.False(t, found)
}

func TestAdminForceTokenChannel_DeniesSameLevelTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 2).Update("role", common.RoleAdminUser).Error)

	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 100})
	AdminForceTokenChannel(c)

	assert.False(t, decodeSuccess(t, rec), "an admin must not manage a same-level user's token")
	_, found := service.GetTokenChannelOverride(10)
	assert.False(t, found)
}

func TestAdminForceTokenChannel_ScopedToTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 20)

	// bob's token (20) reached via alice's target id (2) must not resolve.
	c, rec := forceChannelCtx(t, 2, 20, common.RoleAdminUser, ForceChannelRequest{ChannelId: 100})
	AdminForceTokenChannel(c)

	assert.False(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(20)
	assert.False(t, found, "a cross-user token id must not be overridden")
}

func TestAdminForceTokenChannel_NoExpiryWhenTTLOmitted(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)

	// TTLSeconds omitted (0) must store a no-expiry override, not clear it: the forced switch
	// sticks until an admin clears it rather than auto-expiring.
	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 100})
	AdminForceTokenChannel(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(10)
	assert.True(t, found, "an omitted TTL must store a no-expiry override, not clear it")
}

func withControllerAutoGroups(t *testing.T) {
	t.Helper()
	prevAuto := setting.AutoGroups2JsonString()
	prevUsable := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","vip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"default group","vip":"vip group"}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(prevAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(prevUsable))
	})
}

func TestAdminForceTokenChannel_AutoTokenGroupValidation(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	require.NoError(t, db.Create(&model.Channel{Id: 103, Type: constant.ChannelTypeOpenAI, Key: "sk-x", Status: common.ChannelStatusEnabled, Name: "prem", Group: "premium", Models: "gpt-test"}).Error)
	withControllerAutoGroups(t)
	// Make alice's token an "auto" token so the effective-group == "auto" branch is exercised.
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 10).Update("group", "auto").Error)
	withCleanOverrideCache(t, 10)

	// channel 101 is in "vip", one of the user's auto sub-groups -> accepted.
	c, rec := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 101})
	AdminForceTokenChannel(c)
	require.True(t, decodeSuccess(t, rec), "a channel in an auto sub-group must be accepted for an auto token")
	_, found := service.GetTokenChannelOverride(10)
	assert.True(t, found)

	// channel 103 is in "premium", outside all auto sub-groups -> rejected.
	_ = service.ClearTokenChannelOverride(10)
	c2, rec2 := forceChannelCtx(t, 2, 10, common.RoleAdminUser, ForceChannelRequest{ChannelId: 103})
	AdminForceTokenChannel(c2)
	assert.False(t, decodeSuccess(t, rec2), "a channel outside all auto sub-groups must be rejected")
	_, found2 := service.GetTokenChannelOverride(10)
	assert.False(t, found2)
}

func TestAdminClearTokenForceChannel(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)
	require.NoError(t, service.SetTokenChannelOverride(10, 100, 1, time.Minute))

	c, rec := forceChannelNoBodyCtx(http.MethodDelete, 2, 10, common.RoleAdminUser)
	AdminClearTokenForceChannel(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, decodeSuccess(t, rec))
	_, found := service.GetTokenChannelOverride(10)
	assert.False(t, found, "clear must remove the override")
}

func TestAdminGetTokenForceChannel(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	seedForceChannelChannels(t, db)
	withCleanOverrideCache(t, 10)
	require.NoError(t, service.SetTokenChannelOverride(10, 100, 1, time.Minute))

	c, rec := forceChannelNoBodyCtx(http.MethodGet, 2, 10, common.RoleAdminUser)
	AdminGetTokenForceChannel(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Active    bool `json:"active"`
			ChannelId int  `json:"channel_id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	assert.True(t, resp.Data.Active)
	assert.Equal(t, 100, resp.Data.ChannelId)
}
