package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "00000000000000000000000000000004", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{
			name:      "no filters returns all rows",
			num:       10,
			wantTotal: 5,
			wantIds:   []int{5, 4, 3, 2, 1},
		},
		{
			name:      "keyword filters by name prefix",
			keyword:   "alpha",
			num:       10,
			wantTotal: 3,
			wantIds:   []int{3, 2, 1},
		},
		{
			name:      "enabled status excludes expired rows",
			status:    "1",
			num:       10,
			wantTotal: 2,
			wantIds:   []int{2, 1},
		},
		{
			name:      "expired status returns enabled expired rows",
			status:    "expired",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{3},
		},
		{
			name:      "disabled status",
			status:    "2",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "used status",
			status:    "3",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{5},
		},
		{
			name:      "pagination keeps unpaged total",
			startIdx:  1,
			num:       2,
			wantTotal: 5,
			wantIds:   []int{4, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}
}

func setupRedeemFixture(t *testing.T, quota int) (userId int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{Username: "redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)

	key = "10000000000000000000000000000001"
	redemption := &Redemption{
		Name:        "redeem-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userId, key := setupRedeemFixture(t, 500)

	quota, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "name = ?", "redeem-test").Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userId, redemption.UsedUserId)

	// Redeeming the same code again must fail and must not credit quota.
	_, err = Redeem(key, userId)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
}

func TestRedeemRejectsWalletOverflow(t *testing.T) {
	userId, key := setupRedeemFixture(t, 11)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).Update("quota", common.MaxWalletQuota-10).Error)

	_, err := Redeem(key, userId)
	require.ErrorIs(t, err, ErrRedeemFailed)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, common.MaxWalletQuota-10, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
}

func TestRedemptionQuotaRejectsWalletOverflow(t *testing.T) {
	setupRedeemFixture(t, 500)

	redemption := &Redemption{
		Name:        "overflow-redemption",
		Key:         "10000000000000000000000000000002",
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       common.MaxWalletQuota + 1,
		CreatedTime: common.GetTimestamp(),
	}
	require.Error(t, redemption.Insert())
}

// Exactly one of several concurrent redeems of the same code may win, and
// quota must be credited exactly once.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userId, key := setupRedeemFixture(t, 300)

	const goroutines = 5
	successes := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			if _, err := Redeem(key, userId); err == nil {
				successes[idx] = true
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redeem should succeed")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}

const tokenRedeemTestKey = "tokenredeemkey000000000000000000000000000000001"

type tokenRedeemSnapshot struct {
	userQuota   int
	tokenRemain int
	tokenStatus int
	codeStatus  int
	rows        int64
}

func setupTokenRedeemFixture(t *testing.T, tokenRemain int, unlimited bool, tokenStatus int) (User, Token) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &TokenRedemption{}))
	cleanup := func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&TokenRedemption{}).Error)
		DB.Exec("DELETE FROM tokens")
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	}
	cleanup()
	t.Cleanup(cleanup)
	oldDelay := tokenRedeemDelay
	tokenRedeemDelay = func() {}
	t.Cleanup(func() { tokenRedeemDelay = oldDelay })

	user := User{Username: "token-redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 1000}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: tokenRedeemTestKey, Name: "customer", Status: tokenStatus, RemainQuota: tokenRemain, UnlimitedQuota: unlimited, ExpiredTime: -1}
	require.NoError(t, DB.Create(&token).Error)
	return user, token
}

func createTokenRedeemCode(t *testing.T, code string, quota int, status int, expiredTime int64) Redemption {
	t.Helper()
	redemption := Redemption{Name: "token-redeem", Key: code, Status: status, Quota: quota, CreatedTime: common.GetTimestamp(), ExpiredTime: expiredTime}
	require.NoError(t, DB.Create(&redemption).Error)
	return redemption
}

func takeTokenRedeemSnapshot(t *testing.T, userId int, tokenId int, code string) tokenRedeemSnapshot {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	var token Token
	require.NoError(t, DB.First(&token, "id = ?", tokenId).Error)
	snap := tokenRedeemSnapshot{userQuota: user.Quota, tokenRemain: token.RemainQuota, tokenStatus: token.Status, codeStatus: -1}
	var redemption Redemption
	if err := DB.Unscoped().Where(commonKeyCol+" = ?", code).First(&redemption).Error; err == nil {
		snap.codeStatus = redemption.Status
	}
	require.NoError(t, DB.Model(&TokenRedemption{}).Count(&snap.rows).Error)
	return snap
}

func TestRedeemToToken(t *testing.T) {
	const code = "20000000000000000000000000000001"
	now := common.GetTimestamp()

	t.Run("limited token", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)

		result, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		assert.Equal(t, TokenRedeemResult{Quota: 500, TokenId: token.Id, Unlimited: false, TokenRemainQuota: 600, Replayed: false}, *result)
		assert.Equal(t, tokenRedeemSnapshot{userQuota: 1500, tokenRemain: 600, tokenStatus: common.TokenStatusEnabled, codeStatus: common.RedemptionCodeStatusUsed, rows: 1},
			takeTokenRedeemSnapshot(t, user.Id, token.Id, code))
		var redemption Redemption
		require.NoError(t, DB.Where(commonKeyCol+" = ?", code).First(&redemption).Error)
		assert.Equal(t, user.Id, redemption.UsedUserId)
		var row TokenRedemption
		require.NoError(t, DB.First(&row).Error)
		assert.Equal(t, TokenRedemption{Id: row.Id, RedemptionId: redemption.Id, UserId: user.Id, TokenId: token.Id, Quota: 500, CreatedAt: row.CreatedAt}, row)
	})

	t.Run("unlimited token", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 0, true, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)

		result, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		assert.True(t, result.Unlimited)
		assert.Equal(t, 500, result.Quota)
		snap := takeTokenRedeemSnapshot(t, user.Id, token.Id, code)
		assert.Equal(t, 1500, snap.userQuota)
		assert.Equal(t, 0, snap.tokenRemain)
	})

	t.Run("exhausted token revived", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 0, false, common.TokenStatusExhausted)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)

		_, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		snap := takeTokenRedeemSnapshot(t, user.Id, token.Id, code)
		assert.Equal(t, common.TokenStatusEnabled, snap.tokenStatus)
		assert.Equal(t, 500, snap.tokenRemain)
	})

	t.Run("negative remain stays unusable but credited", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, -300, false, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 100, common.RedemptionCodeStatusEnabled, 0)

		result, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		assert.Equal(t, -200, result.TokenRemainQuota)
		assert.Equal(t, -200, takeTokenRedeemSnapshot(t, user.Id, token.Id, code).tokenRemain)
	})

	t.Run("code with surrounding spaces", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)

		_, err := RedeemToToken("  "+code+"\n", tokenRedeemTestKey)
		require.NoError(t, err)
		assert.Equal(t, 600, takeTokenRedeemSnapshot(t, user.Id, token.Id, code).tokenRemain)
	})

	failures := []struct {
		name  string
		setup func(t *testing.T) (User, Token)
		key   string
	}{
		{"unknown key", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, "tokenredeemkey000000000000000000000000000000999"},
		{"disabled token", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusDisabled)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"expired token", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			require.NoError(t, DB.Model(&Token{}).Where("id = ?", tk.Id).Update("expired_time", now-10).Error)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"disabled user", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Update("status", common.UserStatusDisabled).Error)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"unknown code", func(t *testing.T) (User, Token) {
			return setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
		}, tokenRedeemTestKey},
		{"used code", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusUsed, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"expired code", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, now-10)
			return u, tk
		}, tokenRedeemTestKey},
		{"disabled code", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusDisabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"soft-deleted code", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			redemption := createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
			require.NoError(t, DB.Delete(&redemption).Error)
			return u, tk
		}, tokenRedeemTestKey},
		{"token ceiling exceeded", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, common.MaxWalletQuota-10, false, common.TokenStatusEnabled)
			createTokenRedeemCode(t, code, 11, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
		{"wallet ceiling exceeded", func(t *testing.T) (User, Token) {
			u, tk := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
			require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Update("quota", common.MaxWalletQuota-10).Error)
			createTokenRedeemCode(t, code, 11, common.RedemptionCodeStatusEnabled, 0)
			return u, tk
		}, tokenRedeemTestKey},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			user, token := tc.setup(t)
			before := takeTokenRedeemSnapshot(t, user.Id, token.Id, code)

			result, err := RedeemToToken(code, tc.key)
			require.ErrorIs(t, err, ErrTokenRedeemFailed)
			assert.Nil(t, result)
			assert.Equal(t, before, takeTokenRedeemSnapshot(t, user.Id, token.Id, code))
		})
	}

	t.Run("replay same token", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)

		_, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		result, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		assert.Equal(t, TokenRedeemResult{Quota: 500, TokenId: token.Id, Unlimited: false, TokenRemainQuota: 600, Replayed: true}, *result)
		snap := takeTokenRedeemSnapshot(t, user.Id, token.Id, code)
		assert.Equal(t, 1500, snap.userQuota)
		assert.Equal(t, 600, snap.tokenRemain)
		assert.Equal(t, int64(1), snap.rows)
	})

	t.Run("replay other token of same user", func(t *testing.T) {
		user, token := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
		createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
		other := Token{UserId: user.Id, Key: "tokenredeemkey000000000000000000000000000000002", Name: "other", Status: common.TokenStatusEnabled, RemainQuota: 100, ExpiredTime: -1}
		require.NoError(t, DB.Create(&other).Error)

		_, err := RedeemToToken(code, tokenRedeemTestKey)
		require.NoError(t, err)
		result, err := RedeemToToken(code, other.Key)
		require.ErrorIs(t, err, ErrTokenRedeemFailed)
		assert.Nil(t, result)
		assert.Equal(t, 1500, takeTokenRedeemSnapshot(t, user.Id, token.Id, code).userQuota)
		assert.Equal(t, 100, takeTokenRedeemSnapshot(t, user.Id, other.Id, code).tokenRemain)
	})
}

func TestRedeemToTokenSyncsCaches(t *testing.T) {
	const code = "20000000000000000000000000000002"
	user, token := setupTokenRedeemFixture(t, 100, false, common.TokenStatusEnabled)
	createTokenRedeemCode(t, code, 500, common.RedemptionCodeStatusEnabled, 0)
	useUserCacheMiniRedis(t)

	cachedToken, err := GetTokenByKey(tokenRedeemTestKey, false)
	require.NoError(t, err)
	require.Equal(t, 100, cachedToken.RemainQuota)
	cachedQuota, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, 1000, cachedQuota)

	_, err = RedeemToToken(code, tokenRedeemTestKey)
	require.NoError(t, err)

	cachedToken, err = GetTokenByKey(tokenRedeemTestKey, false)
	require.NoError(t, err)
	assert.Equal(t, 600, cachedToken.RemainQuota)
	assert.Equal(t, token.Id, cachedToken.Id)
	cachedQuota, err = GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1500, cachedQuota)
}
