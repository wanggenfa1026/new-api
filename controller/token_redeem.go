package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const (
	tokenRedeemMaxFailures   = 5
	tokenRedeemWindowSeconds = 1800
)

// tokenRedeemDelay blurs response timing on every attempt that is not already
// locked out, so a caller cannot tell a missing key from a wrong code by how
// long the reply takes. Tests replace it with a no-op.
var tokenRedeemDelay = common.RandomSleep

// tokenRedeemLocks counts failed attempts per submitted key. It is keyed by the
// HMAC of whatever key was sent, existing or not, so a made-up key locks out
// exactly like a real one and the lockout reveals nothing about which keys exist.
var tokenRedeemLocks = newTokenRedeemLimiter()

// tokenRedeemCountScript increments the failure counter and starts its window
// in one step, so a counter can never be left without an expiry.
const tokenRedeemCountScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count`

type tokenRedeemLimiter struct {
	mu      sync.Mutex
	entries map[string]tokenRedeemAttempts
}

type tokenRedeemAttempts struct {
	failures    int
	windowEnd   int64
	lockedUntil int64
}

func newTokenRedeemLimiter() *tokenRedeemLimiter {
	return &tokenRedeemLimiter{entries: make(map[string]tokenRedeemAttempts)}
}

// lockedFor returns how many seconds the key stays locked, or 0.
func (l *tokenRedeemLimiter) lockedFor(keyHash string) int64 {
	if common.RedisEnabled {
		ttl, err := common.RDB.TTL(context.Background(), "token_redeem:lock:"+keyHash).Result()
		if err != nil || ttl <= 0 {
			return 0
		}
		return max(1, int64(ttl/time.Second))
	}
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(0, l.entries[keyHash].lockedUntil-now)
}

func (l *tokenRedeemLimiter) fail(keyHash string) {
	if common.RedisEnabled {
		ctx := context.Background()
		failKey := "token_redeem:fail:" + keyHash
		count, err := common.RDB.Eval(ctx, tokenRedeemCountScript, []string{failKey}, tokenRedeemWindowSeconds).Int64()
		if err != nil || count < tokenRedeemMaxFailures {
			return
		}
		common.RDB.Set(ctx, "token_redeem:lock:"+keyHash, 1, tokenRedeemWindowSeconds*time.Second)
		common.RDB.Del(ctx, failKey)
		return
	}
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[keyHash]
	if entry.windowEnd <= now {
		entry = tokenRedeemAttempts{windowEnd: now + tokenRedeemWindowSeconds}
	}
	entry.failures++
	if entry.failures >= tokenRedeemMaxFailures {
		entry = tokenRedeemAttempts{windowEnd: now + tokenRedeemWindowSeconds, lockedUntil: now + tokenRedeemWindowSeconds}
	}
	l.entries[keyHash] = entry
	// Random keys can only arrive as fast as the per-IP limit allows; still drop
	// finished entries once the map grows.
	if len(l.entries) > 10000 {
		for hash, attempts := range l.entries {
			if attempts.windowEnd <= now && attempts.lockedUntil <= now {
				delete(l.entries, hash)
			}
		}
	}
}

func (l *tokenRedeemLimiter) reset(keyHash string) {
	if common.RedisEnabled {
		common.RDB.Del(context.Background(), "token_redeem:fail:"+keyHash)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, keyHash)
}

type tokenRedeemRequest struct {
	Code string `json:"code"`
}

// RedeemToToken credits a redemption code to the API key sent in the
// Authorization header, without a login session. Every failure gets the same
// reply; only a lockout differs (HTTP 429), and it applies to any submitted key.
func RedeemToToken(c *gin.Context) {
	// Same parsing as middleware.TokenAuth.
	key := c.Request.Header.Get("Authorization")
	if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	key = strings.TrimPrefix(key, "sk-")
	key, _, _ = strings.Cut(key, "-")

	keyHash := common.GenerateHMAC(key)
	if remaining := tokenRedeemLocks.lockedFor(keyHash); remaining > 0 {
		c.Header("Retry-After", strconv.FormatInt(remaining, 10))
		c.AbortWithStatus(http.StatusTooManyRequests)
		return
	}
	tokenRedeemDelay()

	if !operation_setting.IsPaymentComplianceConfirmed() {
		logger.LogWarn(c, common.LogText("token redemption rejected: payment compliance is not confirmed"))
		common.ApiErrorT(c, "Redemption failed, please try again later")
		return
	}
	var req tokenRedeemRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		req.Code = ""
	}
	result, err := model.RedeemToToken(req.Code, key)
	if err != nil {
		tokenRedeemLocks.fail(keyHash)
		// The reason never contains the key or the code.
		logger.LogWarn(c, common.LogText("token redemption failed: %s", err.Error()))
		common.ApiErrorT(c, "Redemption failed, please try again later")
		return
	}
	tokenRedeemLocks.reset(keyHash)

	data := gin.H{"quota": result.Quota, "token_unlimited": result.Unlimited}
	if !result.Unlimited {
		data["token_remain_quota"] = result.TokenRemainQuota
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}
