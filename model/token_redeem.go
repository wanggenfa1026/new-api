package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// TokenRedemption records which API key a redemption code was credited to.
// The unique redemption_id is a second guard against crediting one code twice,
// and lets a client that retries after a dropped response get the original
// result instead of a failure.
type TokenRedemption struct {
	Id           int   `json:"id"`
	RedemptionId int   `json:"redemption_id" gorm:"uniqueIndex"`
	UserId       int   `json:"user_id" gorm:"index"`
	TokenId      int   `json:"token_id" gorm:"index"`
	Quota        int   `json:"quota"`
	CreatedAt    int64 `json:"created_at" gorm:"bigint"`
}

type TokenRedeemResult struct {
	Quota            int
	TokenId          int
	Unlimited        bool
	TokenRemainQuota int
	Replayed         bool
}

// ErrTokenRedeemFailed covers every failure of RedeemToToken. Callers must not
// tell clients which check failed: that would reveal whether a key or a code
// exists.
var ErrTokenRedeemFailed = errors.New("token_redeem.failed")

func tokenRedeemFailure(reason string) error {
	return fmt.Errorf("%w: %s", ErrTokenRedeemFailed, reason)
}

// RedeemToToken credits a redemption code to the API key's owner and, unless
// the key has unlimited quota, to the key itself. It needs no login: holding
// the key is the credential. A code already credited to this same key returns
// the original result again without crediting anything. Callers facing
// clients should blur response timing before calling it.
func RedeemToToken(code string, key string) (*TokenRedeemResult, error) {
	code = strings.TrimSpace(code)
	if code == "" || key == "" {
		return nil, tokenRedeemFailure("missing code or key")
	}

	token, err := GetTokenByKey(key, true)
	if err != nil {
		return nil, tokenRedeemFailure("token not found")
	}
	if token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted {
		return nil, tokenRedeemFailure("token not usable")
	}
	now := common.GetTimestamp()
	if token.ExpiredTime != -1 && token.ExpiredTime < now {
		return nil, tokenRedeemFailure("token expired")
	}
	var owner User
	if err := DB.Select("id", "status").First(&owner, "id = ?", token.UserId).Error; err != nil || owner.Status != common.UserStatusEnabled {
		return nil, tokenRedeemFailure("token owner not usable")
	}

	result := &TokenRedeemResult{TokenId: token.Id, Unlimited: token.UnlimitedQuota}
	redemptionId := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		var redemption Redemption
		if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", code).First(&redemption).Error; err != nil {
			return errors.New("code not found")
		}
		if redemption.Status == common.RedemptionCodeStatusUsed {
			var record TokenRedemption
			if err := tx.Where("redemption_id = ?", redemption.Id).First(&record).Error; err != nil || record.TokenId != token.Id {
				return errors.New("code already used")
			}
			result.Quota = record.Quota
			result.Replayed = true
			return nil
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("code not enabled")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < now {
			return errors.New("code expired")
		}
		if redemption.Quota <= 0 {
			return errors.New("code quota not positive")
		}

		claimed := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]any{
				"redeemed_time": now,
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  token.UserId,
			})
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			return errors.New("code already used")
		}
		if err := creditTopUpQuota(tx, token.UserId, redemption.Quota, nil); err != nil {
			return err
		}
		if !token.UnlimitedQuota {
			var current Token
			if err := lockForUpdate(tx).Select("id", "remain_quota").First(&current, "id = ?", token.Id).Error; err != nil {
				return err
			}
			if err := common.ValidateWalletQuota(current.RemainQuota + redemption.Quota); err != nil {
				return err
			}
			// Only now that the code is claimed: raise the fence and drop the cached
			// key so no reader republishes the old balance. A failed attempt must not
			// disturb the cache of a key it cannot credit.
			if err := invalidateTokenCacheForMutation(token.Key); err != nil {
				return err
			}
			// Not increaseTokenQuota: that also lowers used_quota, which is refund semantics.
			credited := tx.Model(&Token{}).
				Where("id = ? AND user_id = ? AND status IN ?", token.Id, token.UserId, []int{common.TokenStatusEnabled, common.TokenStatusExhausted}).
				Updates(map[string]any{
					"remain_quota":  gorm.Expr("remain_quota + ?", redemption.Quota),
					"status":        gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END", common.TokenStatusExhausted, common.TokenStatusEnabled),
					"accessed_time": now,
				})
			if credited.Error != nil {
				return credited.Error
			}
			if credited.RowsAffected != 1 {
				return errors.New("token changed during redemption")
			}
		}
		result.Quota = redemption.Quota
		redemptionId = redemption.Id
		return tx.Create(&TokenRedemption{RedemptionId: redemption.Id, UserId: token.UserId, TokenId: token.Id, Quota: redemption.Quota}).Error
	})
	if err != nil {
		return nil, tokenRedeemFailure(err.Error())
	}

	if !result.Replayed {
		if !token.UnlimitedQuota {
			// The fence lasts 10 seconds; a transaction that waited longer on row
			// locks could let a reader cache the pre-commit balance, so drop it again.
			if err := invalidateTokenCacheForMutation(token.Key); err != nil {
				common.SysError(common.LogText("failed to invalidate token cache after redemption: %s", err.Error()))
			}
		}
		syncCreditUserQuotaCache(token.UserId, result.Quota, common.LogText("token redemption"))
		RecordLog(token.UserId, LogTypeTopup, common.NewMessage("Topped up {{quota}} with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(result.Quota), "id": redemptionId}))
	}
	var fresh Token
	if err := DB.Select("remain_quota").First(&fresh, "id = ?", token.Id).Error; err == nil {
		result.TokenRemainQuota = fresh.RemainQuota
	}
	return result, nil
}
