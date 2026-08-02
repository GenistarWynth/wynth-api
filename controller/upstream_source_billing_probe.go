package controller

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const upstreamSourceBillingProbeManualTimeout = 12 * time.Second

func GetUpstreamSourceBillingProbe(c *gin.Context) {
	sourceID, mappingID, ok := upstreamSourceBillingProbePathIDs(c)
	if !ok {
		return
	}
	response, err := service.NewUpstreamSourceBillingProbeService().GetMapping(
		c.Request.Context(),
		sourceID,
		mappingID,
		time.Now(),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

func UpdateUpstreamSourceBillingProbe(c *gin.Context) {
	sourceID, mappingID, ok := upstreamSourceBillingProbePathIDs(c)
	if !ok {
		return
	}
	var request dto.UpstreamSourceBillingProbeUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := service.UpdateUpstreamSourceBillingProbe(
		c.Request.Context(),
		sourceID,
		mappingID,
		request.Enabled,
		request.IntervalMinutes,
		request.AutoPriorityCostSource,
		time.Now(),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.billing_probe_configure", map[string]any{
		"id":              sourceID,
		"mappingId":       mappingID,
		"enabled":         request.Enabled,
		"intervalMinutes": request.IntervalMinutes,
		"costSource":      request.AutoPriorityCostSource,
	})
	common.ApiSuccess(c, response)
}

func RunUpstreamSourceBillingProbe(c *gin.Context) {
	sourceID, mappingID, ok := upstreamSourceBillingProbePathIDs(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamSourceBillingProbeManualTimeout)
	defer cancel()
	response, err := service.NewUpstreamSourceBillingProbeService().ProbeMapping(ctx, sourceID, mappingID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.billing_probe_run", map[string]any{
		"id":        sourceID,
		"mappingId": mappingID,
		"status":    response.Status,
		"errorCode": response.ErrorCode,
	})
	common.ApiSuccess(c, response)
}

func upstreamSourceBillingProbePathIDs(c *gin.Context) (int, int, bool) {
	if c == nil {
		return 0, 0, false
	}
	sourceID, sourceErr := strconv.Atoi(c.Param("id"))
	mappingID, mappingErr := strconv.Atoi(c.Param("mapping_id"))
	if sourceErr != nil || mappingErr != nil || sourceID <= 0 || mappingID <= 0 {
		common.ApiError(c, errors.New("valid source and mapping IDs are required"))
		return 0, 0, false
	}
	return sourceID, mappingID, true
}
