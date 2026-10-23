package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tokenRedeemControllerKey = "ctrlredeemkey0000000000000000000000000000000001"

// setupTokenRedeemController boots a migrated SQLite database the way the
// server does, so the new table must be registered in the real migration list.
func setupTokenRedeemController(t *testing.T, withRedis bool) *gin.Engine {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath, previousMaster := common.SQLitePath, common.IsMasterNode
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	payment := operation_setting.GetPaymentSetting()
	previousConfirmed, previousVersion := payment.ComplianceConfirmed, payment.ComplianceTermsVersion
	previousDelay, previousLocks := tokenRedeemDelay, tokenRedeemLocks
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.SQLitePath, common.IsMasterNode = previousPath, previousMaster
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		payment.ComplianceConfirmed, payment.ComplianceTermsVersion = previousConfirmed, previousVersion
		tokenRedeemDelay, tokenRedeemLocks = previousDelay, previousLocks
	})

	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.SQLitePath = filepath.Join(t.TempDir(), "token-redeem.db")
	common.IsMasterNode = true
	common.RedisEnabled = false
	if withRedis {
		server := miniredis.RunT(t)
		common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
		common.RedisEnabled = true
	}
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, i18n.Init())

	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	tokenRedeemDelay = func() {}
	tokenRedeemLocks = newTokenRedeemLimiter()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/token/redeem", RedeemToToken)
	return engine
}

func createTokenRedeemControllerToken(t *testing.T, key string, unlimited bool, status int) model.Token {
	t.Helper()
	user := model.User{Username: "redeem-" + key[len(key)-4:], Password: "password", Status: common.UserStatusEnabled, Quota: 1000, AffCode: "aff-" + key[len(key)-4:]}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: key, Name: "customer", Status: status, RemainQuota: 100, UnlimitedQuota: unlimited, ExpiredTime: -1}
	require.NoError(t, model.DB.Create(&token).Error)
	return token
}

func createTokenRedeemControllerCode(t *testing.T, code string, status int, expiredTime int64) {
	t.Helper()
	redemption := model.Redemption{Name: "ctrl", Key: code, Status: status, Quota: 500, CreatedTime: common.GetTimestamp(), ExpiredTime: expiredTime}
	require.NoError(t, model.DB.Create(&redemption).Error)
}

func postTokenRedeem(engine *gin.Engine, authorization string, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/token/redeem", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func tokenRedeemCodeBody(code string) string {
	return fmt.Sprintf(`{"code":%q}`, code)
}

func TestRedeemToTokenParsesKeyLikeTokenAuth(t *testing.T) {
	engine := setupTokenRedeemController(t, false)
	token := createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)

	accepted := []string{
		"Bearer sk-" + tokenRedeemControllerKey,
		"sk-" + tokenRedeemControllerKey,
		"Bearer " + tokenRedeemControllerKey,
		"bearer sk-" + tokenRedeemControllerKey + "-12",
	}
	for i, header := range accepted {
		code := fmt.Sprintf("3000000000000000000000000000000%d", i)
		createTokenRedeemControllerCode(t, code, common.RedemptionCodeStatusEnabled, 0)

		recorder := postTokenRedeem(engine, header, tokenRedeemCodeBody(code))
		require.Equal(t, http.StatusOK, recorder.Code, header)
		assert.Contains(t, recorder.Body.String(), `"success":true`, header)
	}
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, "id = ?", token.Id).Error)
	assert.Equal(t, 100+500*len(accepted), stored.RemainQuota)

	for _, header := range []string{"", "Bearer "} {
		createTokenRedeemControllerCode(t, "30000000000000000000000000000099", common.RedemptionCodeStatusEnabled, 0)
		recorder := postTokenRedeem(engine, header, tokenRedeemCodeBody("30000000000000000000000000000099"))
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"success":false`)
		require.NoError(t, model.DB.Unscoped().Where("name = ?", "ctrl").Where("status = ?", common.RedemptionCodeStatusEnabled).Delete(&model.Redemption{}).Error)
	}
}

func TestRedeemToTokenSuccessResponse(t *testing.T) {
	engine := setupTokenRedeemController(t, false)
	createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)
	createTokenRedeemControllerToken(t, "ctrlredeemkey0000000000000000000000000000000002", true, common.TokenStatusEnabled)
	createTokenRedeemControllerCode(t, "31000000000000000000000000000001", common.RedemptionCodeStatusEnabled, 0)
	createTokenRedeemControllerCode(t, "31000000000000000000000000000002", common.RedemptionCodeStatusEnabled, 0)

	limited := postTokenRedeem(engine, "Bearer sk-"+tokenRedeemControllerKey, tokenRedeemCodeBody("31000000000000000000000000000001"))
	require.Equal(t, http.StatusOK, limited.Code)
	assert.JSONEq(t, `{"success":true,"message":"","data":{"quota":500,"token_unlimited":false,"token_remain_quota":600}}`, limited.Body.String())

	unlimited := postTokenRedeem(engine, "Bearer sk-ctrlredeemkey0000000000000000000000000000000002", tokenRedeemCodeBody("31000000000000000000000000000002"))
	require.Equal(t, http.StatusOK, unlimited.Code)
	assert.JSONEq(t, `{"success":true,"message":"","data":{"quota":500,"token_unlimited":true}}`, unlimited.Body.String())
}

func TestRedeemToTokenUniformFailure(t *testing.T) {
	engine := setupTokenRedeemController(t, false)
	createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)
	createTokenRedeemControllerToken(t, "ctrlredeemkey0000000000000000000000000000000003", false, common.TokenStatusDisabled)
	createTokenRedeemControllerCode(t, "32000000000000000000000000000001", common.RedemptionCodeStatusEnabled, 0)
	createTokenRedeemControllerCode(t, "32000000000000000000000000000002", common.RedemptionCodeStatusUsed, 0)
	createTokenRedeemControllerCode(t, "32000000000000000000000000000003", common.RedemptionCodeStatusEnabled, common.GetTimestamp()-10)

	cases := []struct {
		name          string
		authorization string
		body          string
	}{
		{"unknown key", "Bearer sk-ctrlredeemkey0000000000000000000000000000000999", tokenRedeemCodeBody("32000000000000000000000000000001")},
		{"disabled key", "Bearer sk-ctrlredeemkey0000000000000000000000000000000003", tokenRedeemCodeBody("32000000000000000000000000000001")},
		{"wrong code", "Bearer sk-" + tokenRedeemControllerKey, tokenRedeemCodeBody("32000000000000000000000000000009")},
		{"used code", "Bearer sk-" + tokenRedeemControllerKey, tokenRedeemCodeBody("32000000000000000000000000000002")},
		{"expired code", "Bearer sk-" + tokenRedeemControllerKey, tokenRedeemCodeBody("32000000000000000000000000000003")},
		{"bad body", "Bearer sk-" + tokenRedeemControllerKey, `{"code":`},
	}
	var reference string
	for _, tc := range cases {
		tokenRedeemLocks = newTokenRedeemLimiter()
		recorder := postTokenRedeem(engine, tc.authorization, tc.body)
		require.Equal(t, http.StatusOK, recorder.Code, tc.name)
		if reference == "" {
			reference = recorder.Body.String()
			assert.Contains(t, reference, `"success":false`)
			continue
		}
		assert.Equal(t, reference, recorder.Body.String(), tc.name)
	}
	var code model.Redemption
	require.NoError(t, model.DB.Where("status = ? AND expired_time = 0", common.RedemptionCodeStatusEnabled).First(&code).Error)
	assert.Equal(t, 0, code.UsedUserId, "a failed attempt must not consume a valid code")
}

func TestRedeemToTokenLockoutDoesNotRevealKeyExistence(t *testing.T) {
	for _, withRedis := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%v", withRedis), func(t *testing.T) {
			engine := setupTokenRedeemController(t, withRedis)
			createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)
			createTokenRedeemControllerCode(t, "33000000000000000000000000000001", common.RedemptionCodeStatusEnabled, 0)

			for _, key := range []string{tokenRedeemControllerKey, "ctrlredeemkey0000000000000000000000000000000888"} {
				for range tokenRedeemMaxFailures {
					recorder := postTokenRedeem(engine, "Bearer sk-"+key, tokenRedeemCodeBody("33000000000000000000000000000009"))
					require.Equal(t, http.StatusOK, recorder.Code)
				}
				locked := postTokenRedeem(engine, "Bearer sk-"+key, tokenRedeemCodeBody("33000000000000000000000000000009"))
				assert.Equal(t, http.StatusTooManyRequests, locked.Code)
				assert.Empty(t, locked.Body.String())
				assert.NotEmpty(t, locked.Header().Get("Retry-After"))
			}

			locked := postTokenRedeem(engine, "Bearer sk-"+tokenRedeemControllerKey, tokenRedeemCodeBody("33000000000000000000000000000001"))
			assert.Equal(t, http.StatusTooManyRequests, locked.Code)
			var code model.Redemption
			require.NoError(t, model.DB.Where("name = ?", "ctrl").First(&code).Error)
			assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status, "a locked key must not consume the code")
		})
	}
}

func TestRedeemToTokenSuccessResetsFailures(t *testing.T) {
	engine := setupTokenRedeemController(t, false)
	createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)
	createTokenRedeemControllerCode(t, "34000000000000000000000000000001", common.RedemptionCodeStatusEnabled, 0)
	header := "Bearer sk-" + tokenRedeemControllerKey

	for range tokenRedeemMaxFailures - 1 {
		require.Equal(t, http.StatusOK, postTokenRedeem(engine, header, tokenRedeemCodeBody("34000000000000000000000000000009")).Code)
	}
	success := postTokenRedeem(engine, header, tokenRedeemCodeBody("34000000000000000000000000000001"))
	require.Contains(t, success.Body.String(), `"success":true`)
	for range tokenRedeemMaxFailures {
		assert.Equal(t, http.StatusOK, postTokenRedeem(engine, header, tokenRedeemCodeBody("34000000000000000000000000000009")).Code)
	}
}

func TestRedeemToTokenComplianceGate(t *testing.T) {
	engine := setupTokenRedeemController(t, false)
	createTokenRedeemControllerToken(t, tokenRedeemControllerKey, false, common.TokenStatusEnabled)
	createTokenRedeemControllerCode(t, "35000000000000000000000000000001", common.RedemptionCodeStatusEnabled, 0)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false

	recorder := postTokenRedeem(engine, "Bearer sk-"+tokenRedeemControllerKey, tokenRedeemCodeBody("35000000000000000000000000000001"))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	var code model.Redemption
	require.NoError(t, model.DB.Where("name = ?", "ctrl").First(&code).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
}
