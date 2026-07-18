package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModelSearchTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Model{}, &Vendor{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Model{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Vendor{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Model{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Vendor{}).Error)
	})
	vendors := []*Vendor{
		{Id: 1, Name: "OpenAI"},
		{Id: 2, Name: "Anthropic"},
	}
	for _, item := range vendors {
		require.NoError(t, item.Insert())
	}
	models := []*Model{
		{Id: 1, ModelName: "alpha-openai", VendorID: 1, Status: 1, SyncOfficial: 1},
		{Id: 2, ModelName: "beta-openai", VendorID: 1, Status: 0, SyncOfficial: 0},
		{Id: 3, ModelName: "alpha-anthropic", VendorID: 2, Status: 1, SyncOfficial: 0},
		{Id: 4, ModelName: "gamma-anthropic", VendorID: 2, Status: 0, SyncOfficial: 1},
	}
	for _, item := range models {
		require.NoError(t, item.Insert())
	}
}

func TestSearchModelsFiltersAndKeepsUnpagedTotal(t *testing.T) {
	setupModelSearchTest(t)
	tests := []struct {
		name         string
		keyword      string
		vendor       string
		status       string
		syncOfficial string
		offset       int
		limit        int
		wantTotal    int64
		wantIDs      []int
	}{
		{name: "enabled", status: "enabled", limit: 10, wantTotal: 2, wantIDs: []int{3, 1}},
		{name: "disabled", status: "disabled", limit: 10, wantTotal: 2, wantIDs: []int{4, 2}},
		{name: "sync yes", syncOfficial: "yes", limit: 10, wantTotal: 2, wantIDs: []int{4, 1}},
		{name: "sync no", syncOfficial: "no", limit: 10, wantTotal: 2, wantIDs: []int{3, 2}},
		{name: "combined", status: "enabled", syncOfficial: "no", limit: 10, wantTotal: 1, wantIDs: []int{3}},
		{name: "numeric vendor", vendor: "1", limit: 10, wantTotal: 2, wantIDs: []int{2, 1}},
		{name: "textual vendor and keyword", keyword: "alpha", vendor: "OpenAI", limit: 10, wantTotal: 1, wantIDs: []int{1}},
		{name: "keyword and sync", keyword: "alpha", syncOfficial: "no", limit: 10, wantTotal: 1, wantIDs: []int{3}},
		{name: "pagination keeps unpaged total", offset: 1, limit: 2, wantTotal: 4, wantIDs: []int{3, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchModels(tt.keyword, tt.vendor, tt.status, tt.syncOfficial, tt.offset, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			ids := make([]int, 0, len(rows))
			for _, row := range rows {
				ids = append(ids, row.Id)
			}
			assert.Equal(t, tt.wantIDs, ids)
		})
	}
}

func TestGetVendorModelCountsByFilters(t *testing.T) {
	setupModelSearchTest(t)

	counts, err := GetVendorModelCountsByFilters("alpha", "", "enabled", "")

	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{1: 1, 2: 1}, counts)

	counts, err = GetVendorModelCountsByFilters("", "2", "", "yes")
	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{2: 1}, counts)

	counts, err = GetVendorModelCountsByFilters("alpha", "OpenAI", "", "")
	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{1: 1}, counts)
}
