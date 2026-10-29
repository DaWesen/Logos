package auth

import (
	"context"
	"strconv"
	"time"

	"Logos/pkg/cache"
	pkgJwt "Logos/pkg/jwt"
	"Logos/pkg/logger"
)

const (
	// revokedJTIPrefix 单令牌黑名单：key 带剩余 TTL，令牌自然过期后由 Redis 自动回收
	revokedJTIPrefix = "auth:revoked:jti:"
	// revokedUserPrefix 用户级撤销水位线：签发时间早于水位线的令牌全部失效
	revokedUserPrefix = "auth:revoked:user:"
)

// Revoker 维护 JWT 的撤销状态，解决"无状态令牌签发后无法作废"的问题：
//
//   - 单令牌撤销（登出当前设备）：按 jti 拉黑，TTL = 令牌剩余有效期；
//   - 用户级撤销（改密、登出所有设备）：写入"撤销水位线"，判断 iat 是否早于水位线。
//
// 为什么不给每个用户维护 token 列表：一次登出全部设备要为该用户所有历史令牌建索引，
// 写入放大且需要清理；水位线是一个 key、一次写入，判断也不需要扫描。
//
// 故障策略：Redis 不可用时按 fail-open 放行并告警。撤销查询在认证热路径上，
// fail-closed 会让一次缓存抖动直接变成全站 401；这里用"可用性优先 + 告警"换取，
// 单令牌黑名单本身也有过期时间，缓存恢复后立即重新生效。
type Revoker struct {
	cache cache.Cache
}

func NewRevoker(c cache.Cache) *Revoker {
	return &Revoker{cache: c}
}

// RevokeToken 拉黑单个令牌。ttl 传令牌剩余有效期即可（<=0 时给最小 1 秒，避免 Set 永久键）。
func (r *Revoker) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if r == nil || r.cache == nil || jti == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Second
	}
	return r.cache.Set(ctx, revokedJTIPrefix+jti, "1", ttl)
}

// RevokeAll 设置用户撤销水位线：此刻之前签发的令牌全部失效。
// ttl 建议为访问令牌有效期 + 余量，保证最长寿命的旧令牌也覆盖到。
func (r *Revoker) RevokeAll(ctx context.Context, userID string, ttl time.Duration) error {
	if r == nil || r.cache == nil || userID == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return r.cache.Set(ctx, revokedUserPrefix+userID, strconv.FormatInt(time.Now().Unix(), 10), ttl)
}

// IsRevoked 判断令牌是否已被撤销。返回 true 表示应立即拒绝。
// 缓存异常时 fail-open 返回 false，并记录告警便于发现缓存故障。
func (r *Revoker) IsRevoked(ctx context.Context, claims *pkgJwt.Claims) bool {
	if r == nil || r.cache == nil || claims == nil {
		return false
	}

	if claims.ID != "" {
		revoked, err := r.cache.Exists(ctx, revokedJTIPrefix+claims.ID)
		if err != nil {
			logger.Warn("查询令牌撤销状态失败，按 fail-open 处理", logger.ErrorField(err))
		} else if revoked {
			return true
		}
	}

	if claims.IssuedAt == nil || claims.UserID == "" {
		return false
	}
	// 先 Exists 再用 Get：缓存实现里 missing key 与故障都表现为 error，分两步才能区分
	exists, err := r.cache.Exists(ctx, revokedUserPrefix+claims.UserID)
	if err != nil {
		logger.Warn("查询用户撤销水位线失败，按 fail-open 处理", logger.ErrorField(err))
		return false
	}
	if !exists {
		return false
	}
	raw, err := r.cache.Get(ctx, revokedUserPrefix+claims.UserID)
	if err != nil {
		logger.Warn("读取用户撤销水位线失败，按 fail-open 处理", logger.ErrorField(err))
		return false
	}
	watermark, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		logger.Warn("用户撤销水位线格式异常", logger.StringField("value", raw))
		return false
	}
	// 用 < 而不是 <=：撤销后同一秒内重新登录拿到的令牌 iat 与水位线相等，不应被误杀
	return claims.IssuedAt.Unix() < watermark
}
