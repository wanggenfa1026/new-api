package model

import (
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ChannelCost is what one billed unit (an image, or a request for models that
// are not billed per image) of a model costs at an upstream channel, in USD.
// ModelName is the model as the client requested it, before channel mapping.
type ChannelCost struct {
	Id        int     `json:"id"`
	ChannelId int     `json:"channel_id" gorm:"uniqueIndex:idx_channel_cost,priority:1"`
	ModelName string  `json:"model_name" gorm:"type:varchar(191);uniqueIndex:idx_channel_cost,priority:2;default:''"`
	UnitCost  float64 `json:"unit_cost"`
	UpdatedAt int64   `json:"updated_at" gorm:"bigint"`
}

// ProfitDaily accumulates, per local day, API key, channel and model, what was
// charged to the key (Revenue) and what the upstream cost (Cost), both in
// quota units. The cost is the channel cost at the time of each request, so a
// later price change does not rewrite earlier days.
type ProfitDaily struct {
	Id        int    `json:"id"`
	StatDay   int    `json:"stat_day" gorm:"uniqueIndex:idx_profit_daily,priority:1"` // YYYYMMDD, server local time
	TokenId   int    `json:"token_id" gorm:"uniqueIndex:idx_profit_daily,priority:2;index"`
	ChannelId int    `json:"channel_id" gorm:"uniqueIndex:idx_profit_daily,priority:3"`
	ModelName string `json:"model_name" gorm:"type:varchar(191);uniqueIndex:idx_profit_daily,priority:4;default:''"`
	UserId    int    `json:"user_id"`
	TokenName string `json:"token_name" gorm:"type:varchar(191);default:''"`
	// RequestCount counts charged requests; ImageCount counts the images the
	// upstream returned for them (the billed quantity when it reported none).
	RequestCount int64 `json:"request_count" gorm:"bigint"`
	ImageCount   int64 `json:"image_count" gorm:"bigint"`
	Revenue      int64 `json:"revenue" gorm:"bigint"`
	Cost         int64 `json:"cost" gorm:"bigint"`
	// UncostedCount counts requests served by a channel with no cost set for
	// the model; their cost is recorded as zero.
	UncostedCount int64 `json:"uncosted_count" gorm:"bigint"`
}

func (ProfitDaily) TableName() string {
	return "profit_daily"
}

const channelCostCacheTTL = time.Minute

var channelCostCache struct {
	sync.RWMutex
	costs    map[int]map[string]float64
	loadedAt time.Time
}

func loadChannelCosts() (map[int]map[string]float64, error) {
	var rows []ChannelCost
	if err := DB.Find(&rows).Error; err != nil {
		return nil, err
	}
	costs := make(map[int]map[string]float64)
	for _, row := range rows {
		if costs[row.ChannelId] == nil {
			costs[row.ChannelId] = make(map[string]float64)
		}
		costs[row.ChannelId][row.ModelName] = row.UnitCost
	}
	return costs, nil
}

// GetChannelUnitCost returns the cost set for a model at a channel. The costs
// are cached briefly so that recording a request does not read the table.
func GetChannelUnitCost(channelId int, modelName string) (float64, bool) {
	channelCostCache.RLock()
	costs, fresh := channelCostCache.costs, time.Since(channelCostCache.loadedAt) < channelCostCacheTTL
	channelCostCache.RUnlock()
	if costs == nil || !fresh {
		loaded, err := loadChannelCosts()
		if err != nil {
			common.SysError(common.LogText("failed to load channel costs: %s", err.Error()))
		} else {
			costs = loaded
			channelCostCache.Lock()
			channelCostCache.costs, channelCostCache.loadedAt = loaded, time.Now()
			channelCostCache.Unlock()
		}
	}
	cost, ok := costs[channelId][modelName]
	return cost, ok
}

func GetAllChannelCosts() ([]ChannelCost, error) {
	var rows []ChannelCost
	err := DB.Order("channel_id, model_name").Find(&rows).Error
	return rows, err
}

// ChannelCostUpdate sets the cost of a model at a channel; a nil UnitCost
// removes it, which makes later requests count as uncosted.
type ChannelCostUpdate struct {
	ChannelId int      `json:"channel_id"`
	ModelName string   `json:"model_name"`
	UnitCost  *float64 `json:"unit_cost"`
}

func SaveChannelCosts(updates []ChannelCostUpdate) error {
	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, update := range updates {
			match := tx.Where("channel_id = ? AND model_name = ?", update.ChannelId, update.ModelName)
			if update.UnitCost == nil {
				if err := match.Delete(&ChannelCost{}).Error; err != nil {
					return err
				}
				continue
			}
			result := match.Model(&ChannelCost{}).Updates(map[string]any{"unit_cost": *update.UnitCost, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				continue
			}
			row := ChannelCost{ChannelId: update.ChannelId, ModelName: update.ModelName, UnitCost: *update.UnitCost, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	channelCostCache.Lock()
	channelCostCache.costs = nil
	channelCostCache.Unlock()
	return err
}

// ProfitStatDay is the local calendar day of a timestamp as YYYYMMDD.
func ProfitStatDay(timestamp int64) int {
	t := time.Unix(timestamp, 0).In(time.Local)
	return t.Year()*10000 + int(t.Month())*100 + t.Day()
}

type ProfitRecordParams struct {
	CreatedAt int64
	UserId    int
	TokenId   int
	TokenName string
	ChannelId int
	ModelName string
	// Quota is what the request was charged.
	Quota int
	// BilledCount is the quantity the request was billed for and the upstream
	// cost is multiplied by; ReturnedCount is how many images came back.
	BilledCount   int
	ReturnedCount int
}

// RecordProfit adds one charged request to the day's profit row and returns
// its upstream cost in quota units, and whether a cost is set for the channel
// and model. A failure to store the row is logged and does not fail the
// request, which is already charged.
func RecordProfit(params ProfitRecordParams) (cost int, costed bool) {
	if DB == nil {
		return 0, false
	}
	billed := max(params.BilledCount, 1)
	unitCost, costed := GetChannelUnitCost(params.ChannelId, params.ModelName)
	if costed {
		cost = common.QuotaRound(unitCost * common.QuotaPerUnit * float64(billed))
	}
	images := params.ReturnedCount
	if images <= 0 {
		images = billed
	}
	uncosted := 0
	if !costed {
		uncosted = 1
	}

	key := ProfitDaily{StatDay: ProfitStatDay(params.CreatedAt), TokenId: params.TokenId, ChannelId: params.ChannelId, ModelName: params.ModelName}
	add := func() (int64, error) {
		result := DB.Model(&ProfitDaily{}).
			Where("stat_day = ? AND token_id = ? AND channel_id = ? AND model_name = ?", key.StatDay, key.TokenId, key.ChannelId, key.ModelName).
			Updates(map[string]any{
				"request_count":  gorm.Expr("request_count + ?", 1),
				"image_count":    gorm.Expr("image_count + ?", images),
				"revenue":        gorm.Expr("revenue + ?", params.Quota),
				"cost":           gorm.Expr("cost + ?", cost),
				"uncosted_count": gorm.Expr("uncosted_count + ?", uncosted),
			})
		return result.RowsAffected, result.Error
	}

	affected, err := add()
	if err == nil && affected == 0 {
		row := key
		row.UserId, row.TokenName = params.UserId, params.TokenName
		row.RequestCount, row.ImageCount = 1, int64(images)
		row.Revenue, row.Cost, row.UncostedCount = int64(params.Quota), int64(cost), int64(uncosted)
		if err = DB.Create(&row).Error; err != nil {
			// Another request created the day's row first; add to it.
			affected, err = add()
			if err == nil && affected == 0 {
				err = errors.New("profit row is missing after a conflicting insert")
			}
		}
	}
	if err != nil {
		common.SysError(common.LogText("failed to record profit: %s", err.Error()))
	}
	return cost, costed
}

// ProfitTotals are the sums every profit report row carries, in quota units.
type ProfitTotals struct {
	RequestCount  int64 `json:"request_count"`
	ImageCount    int64 `json:"image_count"`
	Revenue       int64 `json:"revenue"`
	Cost          int64 `json:"cost"`
	Profit        int64 `json:"profit"`
	UncostedCount int64 `json:"uncosted_count"`
}

// ProfitReportRow is one group of a profit report. Only the field the report
// is grouped by is set.
type ProfitReportRow struct {
	StatDay   int    `json:"stat_day,omitempty"`
	TokenId   int    `json:"token_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`
	ChannelId int    `json:"channel_id,omitempty"`
	ModelName string `json:"model_name,omitempty"`
	ProfitTotals
}

const (
	ProfitGroupByDay     = "day"
	ProfitGroupByToken   = "token"
	ProfitGroupByChannel = "channel"
	ProfitGroupByModel   = "model"
)

// ProfitFilter selects the days of a report. Unless IncludeExcluded is set,
// requests of keys marked as excluded from profit statistics, and requests
// made without a key (channel tests), are left out; the mark applies to the
// key's whole history.
type ProfitFilter struct {
	StartDay        int
	EndDay          int
	IncludeExcluded bool
}

func (f ProfitFilter) apply(tx *gorm.DB) (*gorm.DB, error) {
	if f.StartDay != 0 {
		tx = tx.Where("stat_day >= ?", f.StartDay)
	}
	if f.EndDay != 0 {
		tx = tx.Where("stat_day <= ?", f.EndDay)
	}
	if f.IncludeExcluded {
		return tx, nil
	}
	var excluded []int
	if err := DB.Unscoped().Model(&Token{}).Where("profit_excluded = ?", true).Pluck("id", &excluded).Error; err != nil {
		return nil, err
	}
	tx = tx.Where("token_id <> ?", 0)
	if len(excluded) > 0 {
		tx = tx.Where("token_id NOT IN ?", excluded)
	}
	return tx, nil
}

const profitSumColumns = "COALESCE(SUM(request_count), 0) AS request_count, COALESCE(SUM(image_count), 0) AS image_count, " +
	"COALESCE(SUM(revenue), 0) AS revenue, COALESCE(SUM(cost), 0) AS cost, COALESCE(SUM(uncosted_count), 0) AS uncosted_count"

// GetProfitReport sums the profit rows matching filter by day, key, channel or
// model. Days come newest first, the other groupings by profit, highest first.
func GetProfitReport(filter ProfitFilter, groupBy string) ([]ProfitReportRow, error) {
	var groupColumn string
	switch groupBy {
	case ProfitGroupByDay:
		groupColumn = "stat_day"
	case ProfitGroupByToken:
		groupColumn = "token_id"
	case ProfitGroupByChannel:
		groupColumn = "channel_id"
	case ProfitGroupByModel:
		groupColumn = "model_name"
	default:
		return nil, errors.New("invalid profit report grouping")
	}
	tx, err := filter.apply(DB.Model(&ProfitDaily{}))
	if err != nil {
		return nil, err
	}
	selectColumns := groupColumn + ", " + profitSumColumns
	if groupBy == ProfitGroupByToken {
		selectColumns += ", MAX(token_name) AS token_name"
	}
	order := "SUM(revenue) - SUM(cost) DESC"
	if groupBy == ProfitGroupByDay {
		order = "stat_day DESC"
	}
	var rows []ProfitReportRow
	if err := tx.Select(selectColumns).Group(groupColumn).Order(order).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Profit = rows[i].Revenue - rows[i].Cost
	}
	return rows, nil
}

// GetProfitTotals sums the profit rows matching filter.
func GetProfitTotals(filter ProfitFilter) (ProfitTotals, error) {
	var totals ProfitTotals
	tx, err := filter.apply(DB.Model(&ProfitDaily{}))
	if err != nil {
		return totals, err
	}
	if err := tx.Select(profitSumColumns).Scan(&totals).Error; err != nil {
		return totals, err
	}
	totals.Profit = totals.Revenue - totals.Cost
	return totals, nil
}

// SetTokenProfitExcluded marks an API key as left out of profit statistics.
func SetTokenProfitExcluded(tokenId int, excluded bool) error {
	var token Token
	if err := DB.Select("id").First(&token, "id = ?", tokenId).Error; err != nil {
		return err
	}
	return DB.Model(&token).Update("profit_excluded", excluded).Error
}

// ProfitChannelCost is a model served by a channel together with the cost set
// for it; UnitCost is nil while no cost is set.
type ProfitChannelCost struct {
	ChannelId     int      `json:"channel_id"`
	ChannelName   string   `json:"channel_name"`
	ChannelStatus int      `json:"channel_status"`
	ModelName     string   `json:"model_name"`
	UnitCost      *float64 `json:"unit_cost"`
}

// GetProfitChannelCosts lists every model of every channel with its cost, and
// costs still stored for a model the channel no longer lists so they can be
// removed.
func GetProfitChannelCosts() ([]ProfitChannelCost, error) {
	var channels []Channel
	if err := DB.Select("id", "name", "status", "models").Order("id").Find(&channels).Error; err != nil {
		return nil, err
	}
	costs, err := loadChannelCosts()
	if err != nil {
		return nil, err
	}
	rows := make([]ProfitChannelCost, 0)
	for _, channel := range channels {
		remaining := costs[channel.Id]
		for _, modelName := range channel.GetModels() {
			row := ProfitChannelCost{ChannelId: channel.Id, ChannelName: channel.Name, ChannelStatus: channel.Status, ModelName: modelName}
			if cost, ok := remaining[modelName]; ok {
				row.UnitCost = &cost
				delete(remaining, modelName)
			}
			rows = append(rows, row)
		}
		for modelName, cost := range remaining {
			rows = append(rows, ProfitChannelCost{ChannelId: channel.Id, ChannelName: channel.Name, ChannelStatus: channel.Status, ModelName: modelName, UnitCost: &cost})
		}
	}
	return rows, nil
}

// ProfitTokenInfo is the current state of an API key shown next to its profit.
type ProfitTokenInfo struct {
	Name           string `json:"name"`
	ProfitExcluded bool   `json:"profit_excluded"`
	Deleted        bool   `json:"deleted"`
}

// GetProfitTokenInfo returns the keys with the given ids, deleted ones included.
func GetProfitTokenInfo(ids []int) (map[int]ProfitTokenInfo, error) {
	info := make(map[int]ProfitTokenInfo, len(ids))
	if len(ids) == 0 {
		return info, nil
	}
	var tokens []Token
	if err := DB.Unscoped().Select("id", "name", "profit_excluded", "deleted_at").Where("id IN ?", ids).Find(&tokens).Error; err != nil {
		return nil, err
	}
	for _, token := range tokens {
		info[token.Id] = ProfitTokenInfo{Name: token.Name, ProfitExcluded: token.ProfitExcluded, Deleted: token.DeletedAt.Valid}
	}
	return info, nil
}

// GetChannelNames returns the names of the channels with the given ids.
func GetChannelNames(ids []int) (map[int]string, error) {
	names := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	var channels []Channel
	if err := DB.Select("id", "name").Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		names[channel.Id] = channel.Name
	}
	return names, nil
}
