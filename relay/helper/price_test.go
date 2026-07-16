package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

func TestModelPriceHelperTieredPreConsumeCompletionFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	savedConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		savedConfig[key] = value
		return nil
	}))
	oldQuotaPerUnit := common.QuotaPerUnit
	oldQuotaSetting := *operation_setting.GetQuotaSetting()
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		*operation_setting.GetQuotaSetting() = oldQuotaSetting
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfig))
	})

	common.QuotaPerUnit = 500_000
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-fallback-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"paid":1,"free":0}`,
	}))

	tests := []struct {
		name                    string
		group                   string
		maxTokens               int
		wantQuota               int
		wantEstimatedCompletion int
	}{
		{
			name:                    "paid group omitted max tokens uses fallback",
			group:                   "paid",
			wantQuota:               62_940,
			wantEstimatedCompletion: 8_192,
		},
		{
			name:                    "explicit max tokens is preserved",
			group:                   "paid",
			maxTokens:               100,
			wantQuota:               2_250,
			wantEstimatedCompletion: 100,
		},
		{
			name:                    "free group omitted max tokens stays zero",
			group:                   "free",
			wantQuota:               0,
			wantEstimatedCompletion: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("group", tt.group)

			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tt.group,
				UsingGroup:      tt.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}

			priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{MaxTokens: tt.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, priceData.QuotaToPreConsume)
			require.NotNil(t, info.TieredBillingSnapshot)
			require.Equal(t, tt.wantEstimatedCompletion, info.TieredBillingSnapshot.EstimatedCompletionTokens)
			require.Equal(t, float64(500_000), info.TieredBillingSnapshot.QuotaPerUnit)
		})
	}
}

func TestPriceHelpersSkipInvalidMultiplicationForFreeGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldQuotaSetting := *operation_setting.GetQuotaSetting()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		*operation_setting.GetQuotaSetting() = oldQuotaSetting
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
	})
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"free":0}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"overflow-price":1e308}`))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	newInfo := func() *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{OriginModelName: "overflow-price", UserGroup: "free", UsingGroup: "free"}
	}

	info := newInfo()
	tokenPrice, err := ModelPriceHelper(ctx, info, 1, &types.TokenCountMeta{ImagePriceRatio: 1e308})
	require.NoError(t, err)
	require.True(t, tokenPrice.FreeModel)
	require.Zero(t, tokenPrice.QuotaToPreConsume)
	require.Equal(t, 1e308, tokenPrice.ModelPrice)
	require.Equal(t, tokenPrice, info.PriceData)

	perCallPrice, err := ModelPriceHelperPerCall(ctx, newInfo())
	require.NoError(t, err)
	require.True(t, perCallPrice.FreeModel)
	require.Zero(t, perCallPrice.Quota)
	require.Equal(t, 1e308, perCallPrice.ModelPrice)
	require.True(t, perCallPrice.UsePrice)
}

func TestModelPriceHelperRequestBillingRatiosApplyOnlyToFixedPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)

	savedConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		savedConfig[key] = value
		return nil
	}))
	oldQuotaPerUnit := common.QuotaPerUnit
	oldQuotaSetting := *operation_setting.GetQuotaSetting()
	oldGroups := ratio_setting.GroupRatio2JSONString()
	oldPrices := ratio_setting.ModelPrice2JSONString()
	oldRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		*operation_setting.GetQuotaSetting() = oldQuotaSetting
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfig))
	})

	common.QuotaPerUnit = 500_000
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"paid":1,"free":0}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"fixed-image-count":0.02,"free-overflow-image-count":1e308}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ratio-image-count":1}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-image-count":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-image-count":"tier(\"base\", p * 3 + c * 15)"}`,
	}))

	newRequest := func(model, group string) (*gin.Context, *relaycommon.RelayInfo) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("group", group)
		return ctx, &relaycommon.RelayInfo{
			OriginModelName: model,
			UserGroup:       group,
			UsingGroup:      group,
			RequestHeaders:  map[string]string{"Content-Type": "application/json"},
			BillingRequestInput: &billingexpr.RequestInput{
				Headers: map[string]string{"Content-Type": "application/json"},
				Body:    []byte(`{}`),
			},
		}
	}

	t.Run("fixed price applies n once before strict conversion", func(t *testing.T) {
		ctx, info := newRequest("fixed-image-count", "paid")
		priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{
			ImagePriceRatio: 2,
			BillingRatios:   map[string]float64{"n": 3},
		})

		require.NoError(t, err)
		require.True(t, priceData.UsePrice)
		require.Equal(t, 60_000, priceData.QuotaToPreConsume)
		require.Equal(t, map[string]float64{"n": 3}, priceData.OtherRatios())
		require.Equal(t, priceData.OtherRatios(), info.PriceData.OtherRatios())
	})

	t.Run("ratio mode ignores request billing ratios", func(t *testing.T) {
		ctx, info := newRequest("ratio-image-count", "paid")
		priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{
			MaxTokens:       100,
			ImagePriceRatio: 2,
			BillingRatios:   map[string]float64{"n": 3},
		})

		require.NoError(t, err)
		require.False(t, priceData.UsePrice)
		require.Equal(t, 1_100, priceData.QuotaToPreConsume)
		require.Nil(t, priceData.OtherRatios())
	})

	t.Run("tiered expression ignores request billing ratios", func(t *testing.T) {
		ctx, info := newRequest("tiered-image-count", "paid")
		priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{
			MaxTokens:       100,
			ImagePriceRatio: 2,
			BillingRatios:   map[string]float64{"n": 3},
		})

		require.NoError(t, err)
		require.False(t, priceData.UsePrice)
		require.Equal(t, 2_250, priceData.QuotaToPreConsume)
		require.Nil(t, priceData.OtherRatios())
	})

	t.Run("free fixed price skips invalid multiplication", func(t *testing.T) {
		ctx, info := newRequest("free-overflow-image-count", "free")
		priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{
			ImagePriceRatio: 1e308,
			BillingRatios:   map[string]float64{"n": 3},
		})

		require.NoError(t, err)
		require.True(t, priceData.FreeModel)
		require.Zero(t, priceData.QuotaToPreConsume)
		require.Equal(t, 1e308, priceData.ModelPrice)
		require.Equal(t, map[string]float64{"n": 3}, priceData.OtherRatios())
	})
}
