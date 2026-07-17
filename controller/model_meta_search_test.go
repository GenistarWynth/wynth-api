package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelMetaListResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items        []model.Model    `json:"items"`
		Total        int64            `json:"total"`
		VendorCounts map[string]int64 `json:"vendor_counts"`
	} `json:"data"`
}

func setupModelMetaSearchControllerTest(t *testing.T) *gin.Engine {
	t.Helper()
	setupModelListControllerTestDB(t)
	for _, item := range []*model.Vendor{
		{Id: 1, Name: "OpenAI"},
		{Id: 2, Name: "Anthropic"},
	} {
		require.NoError(t, item.Insert())
	}
	items := []*model.Model{
		{Id: 1, ModelName: "alpha-openai", VendorID: 1, Status: 1, SyncOfficial: 1},
		{Id: 2, ModelName: "beta-openai", VendorID: 1, Status: 0, SyncOfficial: 0},
		{Id: 3, ModelName: "alpha-anthropic", VendorID: 2, Status: 1, SyncOfficial: 0},
		{Id: 4, ModelName: "gamma-anthropic", VendorID: 2, Status: 0, SyncOfficial: 1},
	}
	for _, item := range items {
		require.NoError(t, item.Insert())
	}
	engine := gin.New()
	engine.GET("/models", GetAllModelsMeta)
	engine.GET("/models/search", SearchModelsMeta)
	return engine
}

func requestModelMetaList(t *testing.T, engine *gin.Engine, target string) modelMetaListResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var response modelMetaListResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func TestSearchModelsMetaReturnsFilteredVendorCounts(t *testing.T) {
	engine := setupModelMetaSearchControllerTest(t)

	response := requestModelMetaList(t, engine, "/models/search?keyword=alpha&status=enabled&page_size=10")

	assert.EqualValues(t, 2, response.Data.Total)
	require.Len(t, response.Data.Items, 2)
	assert.Equal(t, map[string]int64{"1": 1, "2": 1}, response.Data.VendorCounts)
}

func TestGetAllModelsMetaUsesStatusAndSyncFiltersForCounts(t *testing.T) {
	engine := setupModelMetaSearchControllerTest(t)

	response := requestModelMetaList(t, engine, "/models?status=disabled&sync_official=yes&page_size=10")

	assert.EqualValues(t, 1, response.Data.Total)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 4, response.Data.Items[0].Id)
	assert.Equal(t, map[string]int64{"2": 1}, response.Data.VendorCounts)
}

func TestSearchModelsMetaCombinesTextualVendorAndKeyword(t *testing.T) {
	engine := setupModelMetaSearchControllerTest(t)

	response := requestModelMetaList(t, engine, "/models/search?keyword=alpha&vendor=OpenAI&page_size=10")

	assert.EqualValues(t, 1, response.Data.Total)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, 1, response.Data.Items[0].Id)
	assert.Equal(t, map[string]int64{"1": 1}, response.Data.VendorCounts)
}
