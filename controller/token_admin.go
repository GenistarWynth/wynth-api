package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// resolveManageableTargetUser parses the :id route param, loads that user, and
// enforces the role hierarchy (canManageTargetRole). On any failure it writes
// the response and returns (nil, false). AdminAuth/RootAuth middleware already
// guarantees the actor is at least an admin; this adds the per-target check.
func resolveManageableTargetUser(c *gin.Context) (*model.User, bool) {
	targetUserId, err := strconv.Atoi(c.Param("id"))
	if err != nil || targetUserId == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return nil, false
	}
	targetUser, err := model.GetUserById(targetUserId, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return nil, false
	}
	return targetUser, true
}

func AdminGetUserTokens(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(targetUser.Id, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserTokens(targetUser.Id)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func AdminSearchUserTokens(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	keyword := c.Query("keyword")
	token := c.Query("token")
	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.SearchUserTokens(targetUser.Id, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func AdminGetUserToken(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(tid, targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}
