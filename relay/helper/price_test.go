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
