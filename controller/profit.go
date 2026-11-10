package controller

import (
	"math"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// maxChannelUnitCost bounds a channel cost so that a typing mistake cannot
// turn a day's profit into a number that no longer means anything.
const maxChannelUnitCost = 1000.0

func profitFilterFromQuery(c *gin.Context) (model.ProfitFilter, bool) {
	filter := model.ProfitFilter{IncludeExcluded: c.Query("include_excluded") == "true"}
	for name, target := range map[string]*int{"start_day": &filter.StartDay, "end_day": &filter.EndDay} {
		value := c.Query(name)
		if value == "" {
			continue
		}
		if _, err := time.Parse("20060102", value); err != nil {
			common.ApiErrorMsg(c, "invalid "+name)
			return filter, false
		}
		*target, _ = strconv.Atoi(value)
	}
	if filter.StartDay != 0 && filter.EndDay != 0 && filter.EndDay < filter.StartDay {
		common.ApiErrorMsg(c, "invalid day range")
		return filter, false
	}
	return filter, true
}

// GetProfitSummary returns the profit of today, yesterday and the current
// month, for the keys counted in profit statistics.
func GetProfitSummary(c *gin.Context) {
	now := time.Now()
	today := model.ProfitStatDay(now.Unix())
	yesterday := model.ProfitStatDay(now.AddDate(0, 0, -1).Unix())
	monthStart := today - today%100 + 1

	summary := make(map[string]model.ProfitTotals, 3)
	for name, filter := range map[string]model.ProfitFilter{
		"today":     {StartDay: today, EndDay: today},
		"yesterday": {StartDay: yesterday, EndDay: yesterday},
		"month":     {StartDay: monthStart, EndDay: today},
	} {
		totals, err := model.GetProfitTotals(filter)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		summary[name] = totals
	}
	common.ApiSuccess(c, summary)
}

type profitReportRow struct {
	model.ProfitReportRow
	ChannelName    string `json:"channel_name,omitempty"`
	ProfitExcluded bool   `json:"profit_excluded,omitempty"`
	TokenDeleted   bool   `json:"token_deleted,omitempty"`
}

// GetProfitReport returns the profit rows of a day range grouped by day, key,
// channel or model, together with their totals.
func GetProfitReport(c *gin.Context) {
	filter, ok := profitFilterFromQuery(c)
	if !ok {
		return
	}
	groupBy := c.DefaultQuery("group_by", model.ProfitGroupByDay)
	report, err := model.GetProfitReport(filter, groupBy)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	totals, err := model.GetProfitTotals(filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	rows := make([]profitReportRow, len(report))
	ids := make([]int, len(report))
	for i, row := range report {
		rows[i].ProfitReportRow = row
		ids[i] = row.TokenId + row.ChannelId
	}
	switch groupBy {
	case model.ProfitGroupByToken:
		tokens, err := model.GetProfitTokenInfo(ids)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		for i := range rows {
			token, found := tokens[rows[i].TokenId]
			if !found {
				rows[i].TokenDeleted = rows[i].TokenId != 0
				continue
			}
			rows[i].TokenName = token.Name
			rows[i].ProfitExcluded = token.ProfitExcluded
			rows[i].TokenDeleted = token.Deleted
		}
	case model.ProfitGroupByChannel:
		names, err := model.GetChannelNames(ids)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		for i := range rows {
			rows[i].ChannelName = names[rows[i].ChannelId]
		}
	}
	common.ApiSuccess(c, gin.H{"rows": rows, "totals": totals})
}

// GetProfitChannelCosts lists the upstream cost of every channel and model.
func GetProfitChannelCosts(c *gin.Context) {
	costs, err := model.GetProfitChannelCosts()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, costs)
}

// UpdateProfitChannelCosts sets or removes upstream costs. A new cost applies
// to requests from now on; days already recorded keep the cost they had.
func UpdateProfitChannelCosts(c *gin.Context) {
	var request struct {
		Items []model.ChannelCostUpdate `json:"items"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	for _, item := range request.Items {
		if item.ChannelId <= 0 || item.ModelName == "" || len(item.ModelName) > 191 {
			common.ApiErrorT(c, "Invalid channel cost")
			return
		}
		if item.UnitCost == nil {
			continue
		}
		if cost := *item.UnitCost; math.IsNaN(cost) || cost < 0 || cost > maxChannelUnitCost {
			common.ApiErrorT(c, "Invalid channel cost")
			return
		}
	}
	if err := model.SaveChannelCosts(request.Items); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// UpdateTokenProfitExcluded marks any user's API key as left out of, or
// counted in, profit statistics.
func UpdateTokenProfitExcluded(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		common.ApiErrorT(c, "Invalid token")
		return
	}
	var request struct {
		ProfitExcluded *bool `json:"profit_excluded"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.ProfitExcluded == nil {
		common.ApiErrorT(c, "Invalid token")
		return
	}
	if err := model.SetTokenProfitExcluded(tokenId, *request.ProfitExcluded); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
