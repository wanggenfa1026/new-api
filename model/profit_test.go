package model

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func setupProfitTables(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&ChannelCost{}, &ProfitDaily{}))
	reset := func() {
		DB.Exec("DELETE FROM channel_costs")
		DB.Exec("DELETE FROM profit_daily")
		DB.Exec("DELETE FROM tokens")
		DB.Exec("DELETE FROM channels")
		DB.Exec("DELETE FROM logs")
		channelCostCache.Lock()
		channelCostCache.costs = nil
		channelCostCache.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func setChannelCost(t *testing.T, channelId int, modelName string, cost *float64) {
	t.Helper()
	require.NoError(t, SaveChannelCosts([]ChannelCostUpdate{{ChannelId: channelId, ModelName: modelName, UnitCost: cost}}))
}

// A request is costed with the channel cost at the time it is recorded: a
// later cost change, or removing the cost, applies to later requests only.
func TestRecordProfitFreezesTheCostOfEachRequest(t *testing.T) {
	setupProfitTables(t)
	const createdAt = 1794300000
	request := ProfitRecordParams{CreatedAt: createdAt, UserId: 1, TokenId: 11, TokenName: "customer", ChannelId: 2, ModelName: "gpt-image-2", Quota: 5500}

	setChannelCost(t, 2, "gpt-image-2", common.GetPointer(0.006))
	cost, costed := RecordProfit(request)
	assert.Equal(t, 3000, cost)
	assert.True(t, costed)

	twoImages := request
	twoImages.BilledCount, twoImages.ReturnedCount = 2, 3
	cost, _ = RecordProfit(twoImages)
	assert.Equal(t, 6000, cost, "the cost is multiplied by the billed quantity")

	setChannelCost(t, 2, "gpt-image-2", common.GetPointer(0.008))
	cost, _ = RecordProfit(request)
	assert.Equal(t, 4000, cost)

	setChannelCost(t, 2, "gpt-image-2", nil)
	cost, costed = RecordProfit(request)
	assert.Zero(t, cost)
	assert.False(t, costed)

	var rows []ProfitDaily
	require.NoError(t, DB.Find(&rows).Error)
	require.Len(t, rows, 1, "requests of one day, key, channel and model share a row")
	row := rows[0]
	assert.Equal(t, ProfitStatDay(createdAt), row.StatDay)
	assert.Equal(t, "customer", row.TokenName)
	assert.Equal(t, int64(4), row.RequestCount)
	assert.Equal(t, int64(1+3+1+1), row.ImageCount, "returned images are counted when the upstream reported them")
	assert.Equal(t, int64(4*5500), row.Revenue)
	assert.Equal(t, int64(3000+6000+4000), row.Cost)
	assert.Equal(t, int64(1), row.UncostedCount)
}

// Keys marked as excluded and requests without a key stay out of the report,
// whatever day they were made on and even after the key is deleted.
func TestProfitReportLeavesOutExcludedKeys(t *testing.T) {
	setupProfitTables(t)
	const dayOne, dayTwo = 1794300000, 1794300000 + 86400
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "main"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 6, Name: "backup"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-customer", Name: "customer"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 22, UserId: 1, Key: "sk-internal", Name: "internal"}).Error)
	setChannelCost(t, 2, "gpt-image-2", common.GetPointer(0.006))
	setChannelCost(t, 6, "gpt-image-2", common.GetPointer(0.011))

	for _, request := range []ProfitRecordParams{
		{CreatedAt: dayOne, TokenId: 11, TokenName: "customer", ChannelId: 2, ModelName: "gpt-image-2", Quota: 5500},
		{CreatedAt: dayOne, TokenId: 11, TokenName: "customer", ChannelId: 6, ModelName: "gpt-image-2", Quota: 5500},
		{CreatedAt: dayTwo, TokenId: 11, TokenName: "customer", ChannelId: 2, ModelName: "gpt-image-2", Quota: 5500},
		{CreatedAt: dayOne, TokenId: 22, TokenName: "internal", ChannelId: 2, ModelName: "gpt-image-2", Quota: 5500},
		{CreatedAt: dayTwo, TokenId: 0, ChannelId: 2, ModelName: "gpt-image-2", Quota: 5500},
	} {
		RecordProfit(request)
	}
	require.NoError(t, SetTokenProfitExcluded(22, true))
	require.NoError(t, DB.Delete(&Token{Id: 22}).Error)

	totals, err := GetProfitTotals(ProfitFilter{})
	require.NoError(t, err)
	assert.Equal(t, ProfitTotals{RequestCount: 3, ImageCount: 3, Revenue: 16500, Cost: 3000 + 5500 + 3000, Profit: 5000}, totals)

	everything, err := GetProfitTotals(ProfitFilter{IncludeExcluded: true})
	require.NoError(t, err)
	assert.Equal(t, int64(5), everything.RequestCount)

	oneDay, err := GetProfitTotals(ProfitFilter{StartDay: ProfitStatDay(dayTwo), EndDay: ProfitStatDay(dayTwo)})
	require.NoError(t, err)
	assert.Equal(t, int64(1), oneDay.RequestCount)

	byDay, err := GetProfitReport(ProfitFilter{}, ProfitGroupByDay)
	require.NoError(t, err)
	require.Len(t, byDay, 2)
	assert.Equal(t, ProfitStatDay(dayTwo), byDay[0].StatDay, "days come newest first")
	assert.Equal(t, int64(2500), byDay[0].Profit)
	assert.Equal(t, int64(2500), byDay[1].Profit, "a channel that costs what the key pays earns nothing")

	byChannel, err := GetProfitReport(ProfitFilter{}, ProfitGroupByChannel)
	require.NoError(t, err)
	require.Len(t, byChannel, 2)
	assert.Equal(t, 2, byChannel[0].ChannelId)
	assert.Equal(t, int64(5000), byChannel[0].Profit)
	assert.Equal(t, 6, byChannel[1].ChannelId)
	assert.Zero(t, byChannel[1].Profit)

	byToken, err := GetProfitReport(ProfitFilter{IncludeExcluded: true}, ProfitGroupByToken)
	require.NoError(t, err)
	require.Len(t, byToken, 3)
	info, err := GetProfitTokenInfo([]int{11, 22})
	require.NoError(t, err)
	assert.Equal(t, ProfitTokenInfo{Name: "customer"}, info[11])
	assert.Equal(t, ProfitTokenInfo{Name: "internal", ProfitExcluded: true, Deleted: true}, info[22])

	_, err = GetProfitReport(ProfitFilter{}, "user")
	require.Error(t, err)
	require.Error(t, SetTokenProfitExcluded(999, true), "an unknown key is reported")
}

// The consume log carries the cost and profit for administrators only: the
// log a key holder reads through the key must not reveal the upstream cost.
func TestConsumeLogHidesCostFromTheKeyHolder(t *testing.T) {
	setupProfitTables(t)
	setChannelCost(t, 2, "gpt-image-2", common.GetPointer(0.006))
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	RecordConsumeLog(c, 1, RecordConsumeLogParams{
		ChannelId: 2, ModelName: "gpt-image-2", TokenName: "customer", TokenId: 11, Quota: 5500,
		Other: NewLogOther(), ImageCount: 1, ReturnedImageCount: 2,
	})

	var stored Log
	require.NoError(t, DB.First(&stored, "token_id = ?", 11).Error)
	admin := gjson.Get(stored.Other, "admin_info")
	assert.Equal(t, int64(3000), admin.Get("upstream_cost").Int())
	assert.Equal(t, int64(2500), admin.Get("profit").Int())
	assert.Equal(t, int64(2), admin.Get("returned_image_count").Int())

	logs, err := GetLogByTokenId(11)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, 5500, logs[0].Quota)
	assert.NotContains(t, logs[0].Other, "upstream_cost")
	assert.NotContains(t, logs[0].Other, "profit")
}
