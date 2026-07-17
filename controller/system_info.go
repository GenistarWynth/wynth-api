package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func ListSystemInstances(c *gin.Context) {
	instances, err := model.ListSystemInstances()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	now := common.GetTimestamp()
	responses := make([]model.SystemInstanceResponse, 0, len(instances))
	for _, instance := range instances {
		responses = append(responses, instance.ToResponse(now))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responses,
	})
}

func DeleteStaleSystemInstances(c *gin.Context) {
	deletedCount, err := model.DeleteStaleSystemInstances(common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "system_instance.delete_stale_all", map[string]interface{}{
		"deleted_count": deletedCount,
	})
	common.ApiSuccess(c, gin.H{"deleted_count": deletedCount})
}

func DeleteStaleSystemInstance(c *gin.Context) {
	nodeName := strings.TrimSpace(strings.TrimPrefix(c.Param("node_name"), "/"))
	if nodeName == "" {
		common.ApiErrorMsg(c, "node name is required")
		return
	}
	deleted, err := model.DeleteStaleSystemInstance(nodeName, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !deleted {
		common.ApiErrorMsg(c, "instance is not stale or no longer exists")
		return
	}
	recordManageAudit(c, "system_instance.delete_stale", map[string]interface{}{
		"node_name": nodeName,
	})
	common.ApiSuccess(c, gin.H{"deleted_count": 1})
}
