package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Logos/internal/service/platform/user/dao"
	"Logos/internal/service/platform/user/model"
	"Logos/pkg/cache"
	"Logos/pkg/es"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/outbox"
	"Logos/pkg/pagination"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrInvalidPassword   = errors.New("invalid password")
	ErrWrongPassword     = errors.New("wrong password")
	ErrTokenInvalid      = errors.New("invalid token")
	ErrInternalServer    = errors.New("internal server error")
	ErrOldPasswordWrong  = errors.New("old password wrong")
)

type UserService interface {
	Register(ctx context.Context, username, password, email, phone string) (*model.User, string, int64, error)
	Login(ctx context.Context, username, password string) (*model.User, string, int64, error)
	GetUserInfo(ctx context.Context, userID int64) (*model.User, error)
	GetUserInfoByUsername(ctx context.Context, username string) (*model.User, error)
	BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*model.User, error)
	UpdateUser(ctx context.Context, userID int64, email, phone, avatar string, preferences map[string]string, interests []string, oldPassword, newPassword string) error
	UpdateAvatar(ctx context.Context, userID int64, avatar string) error
	CheckUsername(ctx context.Context, username string) (bool, error)
	BatchCheckUsernames(ctx context.Context, usernames []string) (map[string]bool, error)
	GetUserStats(ctx context.Context, userID int64) (*model.UserStats, int64, error)
	SearchUsers(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error)
	VerifyToken(ctx context.Context, token string) (int64, error)
	WithTransaction(ctx context.Context, fn func(txService UserService) error) error
}

type userServiceImpl struct {
	repo       dao.UserRepository
	jwtManager *jwt.JWTManager
	cache      cache.Cache
	outboxRepo outbox.OutboxRepository
	esManager  *es.ESManager
}

func NewUserService(repo dao.UserRepository, jwtManager *jwt.JWTManager, cache cache.Cache, outboxRepo outbox.OutboxRepository, esManager *es.ESManager) UserService {
	return &userServiceImpl{
		repo:       repo,
		jwtManager: jwtManager,
		cache:      cache,
		outboxRepo: outboxRepo,
		esManager:  esManager,
	}
}

func (s *userServiceImpl) Register(ctx context.Context, username, password string, email, phone string) (*model.User, string, int64, error) {
	logger.Info("用户注册请求",
		logger.StringField("username", username))

	if err := s.checkRegisterRateLimit(ctx, username); err != nil {
		return nil, "", 0, err
	}

	exists, err := s.repo.CheckUsernameExists(ctx, username)
	if err != nil {
		logger.Error("检查用户名失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, "", 0, ErrInternalServer
	}
	if exists {
		logger.Warn("用户名已存在",
			logger.StringField("username", username))
		return nil, "", 0, ErrUserAlreadyExists
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("密码加密失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, "", 0, ErrInternalServer
	}

	u := &model.User{
		Username: username,
		Password: string(hashedPassword),
	}

	if email != "" {
		u.Email = &email
	}
	if phone != "" {
		u.Phone = &phone
	}

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.UserRepository) error {
		if err := txRepo.Create(ctx, u); err != nil {
			return err
		}
		eventData, err := json.Marshal(map[string]interface{}{
			"user_id":       u.ID,
			"username":      u.Username,
			"registered_at": time.Now(),
			"email":         u.Email,
			"phone":         u.Phone,
		})
		if err != nil {
			return fmt.Errorf("序列化用户注册事件失败: %w", err)
		}
		key := fmt.Sprintf("%d", u.ID)
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), "user_events", key, eventData)
	}); err != nil {
		logger.Error("创建用户失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, "", 0, ErrInternalServer
	}

	logger.Info("发送用户注册事件",
		logger.Int64Field("user_id", u.ID))

	token, err := s.jwtManager.GenerateToken(strconv.FormatInt(u.ID, 10), "user")
	if err != nil {
		logger.Error("生成令牌失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", u.ID))
		return nil, "", 0, ErrInternalServer
	}

	expireAt := time.Now().Add(time.Duration(s.jwtManager.GetTokenExpireHours()) * time.Hour).Unix()

	if s.esManager != nil {
		esUser := map[string]interface{}{
			"id":         u.ID,
			"username":   u.Username,
			"email":      u.Email,
			"phone":      u.Phone,
			"avatar":     u.Avatar,
			"created_at": u.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		err = s.esManager.AddDocumentWithContext(ctx, "users", fmt.Sprintf("%d", u.ID), esUser)
		if err != nil {
			logger.Error("同步用户到ES失败",
				logger.ErrorField(err),
				logger.Int64Field("user_id", u.ID))
		}
	}

	logger.Info("用户注册成功",
		logger.Int64Field("user_id", u.ID),
		logger.StringField("username", username))

	return u, token, expireAt, nil
}

func (s *userServiceImpl) Login(ctx context.Context, username, password string) (*model.User, string, int64, error) {
	logger.Info("用户登录请求",
		logger.StringField("username", username))

	if err := s.checkLoginRateLimit(ctx, username); err != nil {
		return nil, "", 0, err
	}

	var u *model.User
	var err error

	u, err = s.repo.FindByUsername(ctx, username)
	if err != nil {
		logger.Error("通过用户名查询用户失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, "", 0, ErrInternalServer
	}
	if u == nil {
		u, err = s.repo.FindByEmail(ctx, username)
		if err != nil {
			logger.Error("通过邮箱查询用户失败",
				logger.ErrorField(err),
				logger.StringField("username", username))
			return nil, "", 0, ErrInternalServer
		}
		if u == nil {
			u, err = s.repo.FindByPhone(ctx, username)
			if err != nil {
				logger.Error("通过手机号查询用户失败",
					logger.ErrorField(err),
					logger.StringField("username", username))
				return nil, "", 0, ErrInternalServer
			}
			if u == nil {
				logger.Warn("用户不存在",
					logger.StringField("username", username))
				return nil, "", 0, ErrUserNotFound
			}
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		logger.Warn("密码错误",
			logger.StringField("username", username))
		return nil, "", 0, ErrWrongPassword
	}

	token, err := s.jwtManager.GenerateToken(strconv.FormatInt(u.ID, 10), "user")
	if err != nil {
		logger.Error("生成令牌失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", u.ID))
		return nil, "", 0, ErrInternalServer
	}

	expireAt := time.Now().Add(time.Duration(s.jwtManager.GetTokenExpireHours()) * time.Hour).Unix()

	if s.outboxRepo != nil {
		eventData, err := json.Marshal(map[string]interface{}{
			"user_id":   u.ID,
			"username":  u.Username,
			"logged_at": time.Now(),
		})
		if err != nil {
			logger.Warn("序列化用户登录事件失败", logger.ErrorField(err))
		} else {
			key := fmt.Sprintf("%d", u.ID)
			if err := s.outboxRepo.Save(ctx, s.repo.DB(), "user_events", key, eventData); err != nil {
				// 登录事件是旁路审计，不应阻断登录本身；但必须留下错误日志，
				// 否则 Outbox 写入失败会变成无声的数据丢失。
				logger.Error("写入用户登录事件失败",
					logger.Int64Field("user_id", u.ID),
					logger.ErrorField(err))
			} else {
				logger.Info("发送用户登录事件",
					logger.Int64Field("user_id", u.ID))
			}
		}
	}

	logger.Info("用户登录成功",
		logger.Int64Field("user_id", u.ID),
		logger.StringField("username", username))

	return u, token, expireAt, nil
}

func (s *userServiceImpl) GetUserInfo(ctx context.Context, userID int64) (*model.User, error) {
	logger.Info("获取用户信息请求",
		logger.Int64Field("user_id", userID))

	if s.cache != nil {
		userKey := cache.GenerateUserKey(userID)
		cachedUser, err := s.cache.Get(ctx, userKey)
		if err == nil && cachedUser != "" {
			var user model.User
			if json.Unmarshal([]byte(cachedUser), &user) == nil {
				logger.Info("从缓存获取用户信息成功",
					logger.Int64Field("user_id", userID))
				return &user, nil
			}
		}
	}

	u, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		logger.Error("查询用户失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", userID))
		return nil, ErrInternalServer
	}
	if u == nil {
		logger.Warn("用户不存在",
			logger.Int64Field("user_id", userID))
		return nil, ErrUserNotFound
	}

	if s.cache != nil {
		userKey := cache.GenerateUserKey(userID)
		userData, err := json.Marshal(u)
		if err == nil {
			s.cache.Set(ctx, userKey, string(userData), 10*time.Minute)
			logger.Info("用户信息写入缓存成功",
				logger.Int64Field("user_id", userID))
		}
	}

	return u, nil
}

func (s *userServiceImpl) GetUserInfoByUsername(ctx context.Context, username string) (*model.User, error) {
	logger.Info("通过用户名获取用户信息请求",
		logger.StringField("username", username))

	u, err := s.repo.FindByUsername(ctx, username)
	if err != nil {
		logger.Error("通过用户名查询用户失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, ErrInternalServer
	}
	if u == nil {
		logger.Warn("用户不存在",
			logger.StringField("username", username))
		return nil, ErrUserNotFound
	}
	return u, nil
}

func (s *userServiceImpl) BatchGetUserInfo(ctx context.Context, userIDs []int64) (map[int64]*model.User, error) {
	logger.Info("批量获取用户信息请求",
		logger.AnyField("user_ids", userIDs))

	return s.repo.BatchGetByIDs(ctx, userIDs)
}

func (s *userServiceImpl) UpdateUser(ctx context.Context, userID int64, email, phone, avatar string, preferences map[string]string, interests []string, oldPassword, newPassword string) error {
	logger.Info("更新用户数据",
		logger.Int64Field("user_id", userID))

	u, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		logger.Error("查询用户失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", userID))
		return ErrInternalServer
	}
	if u == nil {
		logger.Warn("用户不存在",
			logger.Int64Field("user_id", userID))
		return ErrUserNotFound
	}

	if email != "" {
		u.Email = &email
	}
	if phone != "" {
		u.Phone = &phone
	}
	if avatar != "" {
		u.Avatar = &avatar
	}
	if preferences != nil {
		u.Preferences = model.JSONMap(preferences)
	}
	if interests != nil {
		u.Interests = model.StringSlice(interests)
	}

	if oldPassword != "" && newPassword != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(oldPassword)); err != nil {
			logger.Warn("旧密码错误",
				logger.Int64Field("user_id", userID))
			return ErrOldPasswordWrong
		}
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			logger.Error("密码加密失败",
				logger.ErrorField(err),
				logger.Int64Field("user_id", userID))
			return ErrInternalServer
		}
		u.Password = string(hashedPassword)
	}

	if err := s.repo.Update(ctx, u); err != nil {
		logger.Error("更新用户失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", userID))
		return ErrInternalServer
	}

	if s.cache != nil {
		userKey := cache.GenerateUserKey(userID)
		s.cache.Delete(ctx, userKey)
		logger.Info("删除用户缓存成功",
			logger.Int64Field("user_id", userID))
	}

	if s.esManager != nil {
		updatedUser, err := s.repo.FindByID(ctx, userID)
		if err == nil && updatedUser != nil {
			esUser := map[string]interface{}{
				"id":         updatedUser.ID,
				"username":   updatedUser.Username,
				"email":      updatedUser.Email,
				"phone":      updatedUser.Phone,
				"avatar":     updatedUser.Avatar,
				"created_at": updatedUser.CreatedAt.Format("2006-01-02 15:04:05"),
			}
			err = s.esManager.UpdateDocumentWithContext(ctx, "users", fmt.Sprintf("%d", userID), esUser)
			if err != nil {
				logger.Error("更新用户到ES失败",
					logger.ErrorField(err),
					logger.Int64Field("user_id", userID))
			}
		}
	}

	logger.Info("用户更新成功",
		logger.Int64Field("user_id", userID))

	return nil
}

func (s *userServiceImpl) UpdateAvatar(ctx context.Context, userID int64, avatar string) error {
	logger.Info("更新头像请求",
		logger.Int64Field("user_id", userID))

	if err := s.repo.UpdateAvatar(ctx, userID, avatar); err != nil {
		return err
	}

	// 清除缓存，确保后续查询能获取到新头像
	if s.cache != nil {
		userKey := cache.GenerateUserKey(userID)
		s.cache.Delete(ctx, userKey)
		logger.Info("更新头像后删除用户缓存",
			logger.Int64Field("user_id", userID))
	}

	return nil
}

func (s *userServiceImpl) CheckUsername(ctx context.Context, username string) (bool, error) {
	logger.Info("检查用户名请求",
		logger.StringField("username", username))

	exists, err := s.repo.CheckUsernameExists(ctx, username)
	if err != nil {
		logger.Error("检查用户名失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return false, ErrInternalServer
	}
	return !exists, nil
}

func (s *userServiceImpl) BatchCheckUsernames(ctx context.Context, usernames []string) (map[string]bool, error) {
	logger.Info("批量检查用户名请求",
		logger.AnyField("usernames", usernames))

	result, err := s.repo.BatchCheckUsername(ctx, usernames)
	if err != nil {
		return nil, ErrInternalServer
	}

	availableMap := make(map[string]bool)
	for username, exists := range result {
		availableMap[username] = !exists
	}

	return availableMap, nil
}

func (s *userServiceImpl) GetUserStats(ctx context.Context, userID int64) (*model.UserStats, int64, error) {
	logger.Info("获取用户统计信息请求",
		logger.Int64Field("user_id", userID))

	stats, err := s.repo.GetStats(ctx, userID)
	if err != nil {
		return nil, 0, ErrInternalServer
	}
	total, err := s.repo.Count(ctx)
	if err != nil {
		return nil, 0, ErrInternalServer
	}
	return stats, total, nil
}

func (s *userServiceImpl) SearchUsers(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error) {
	page, pageSize = pagination.Normalize(page, pageSize)
	logger.Info("搜索用户请求",
		logger.StringField("keyword", keyword),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if s.esManager != nil {
		// ES 默认 index.max_result_window=10000，from+size 超过会直接报
		// "Result window is too large"，因此在进入 ES 前拒绝深页，
		// 而不是把后端原始错误透传给客户端。
		from, size, err := pagination.Window(page, pageSize, pagination.MaxResultWindow)
		if err != nil {
			return nil, 0, err
		}
		query := es.SearchQuery{
			Query: map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":    keyword,
					"fields":   []string{"username", "email"},
					"type":     "best_fields",
					"operator": "and",
				},
			},
			From: from,
			Size: size,
			Sort: []map[string]interface{}{
				{
					"created_at": map[string]interface{}{
						"order": "desc",
					},
				},
			},
		}

		var searchResult es.SearchResult
		err = s.esManager.SearchWithContext(ctx, "users", query, &searchResult)
		if err == nil {
			var users []*model.User
			for _, hit := range searchResult.Hits.Hits {
				var esUser map[string]interface{}
				if json.Unmarshal(hit.Source, &esUser) == nil {
					user := &model.User{}
					if id, ok := esUser["id"].(float64); ok {
						user.ID = int64(id)
					}
					if username, ok := esUser["username"].(string); ok {
						user.Username = username
					}
					if email, ok := esUser["email"].(string); ok {
						user.Email = &email
					}
					if phone, ok := esUser["phone"].(string); ok {
						user.Phone = &phone
					}
					if avatar, ok := esUser["avatar"].(string); ok {
						user.Avatar = &avatar
					}
					users = append(users, user)
				}
			}
			return users, searchResult.Hits.Total.Value, nil
		}
	}

	return s.repo.Search(ctx, keyword, page, pageSize)
}

func (s *userServiceImpl) VerifyToken(ctx context.Context, token string) (int64, error) {
	logger.Debug("验证令牌请求")

	claims, err := s.jwtManager.ParseToken(token)
	if err != nil {
		logger.Warn("无效令牌",
			logger.ErrorField(err))
		return 0, ErrTokenInvalid
	}
	userID, err := strconv.ParseInt(claims.UserID, 10, 64)
	if err != nil {
		return 0, ErrTokenInvalid
	}
	return userID, nil
}

func (s *userServiceImpl) WithTransaction(ctx context.Context, fn func(txService UserService) error) error {
	return s.repo.WithTransaction(ctx, func(txRepo dao.UserRepository) error {
		txService := NewUserService(txRepo, s.jwtManager, s.cache, s.outboxRepo, s.esManager)
		return fn(txService)
	})
}

func (s *userServiceImpl) checkLoginRateLimit(ctx context.Context, username string) error {
	if s.cache == nil {
		return nil
	}

	key := fmt.Sprintf("ratelimit:login:%s", username)

	current, err := s.cache.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("登录频率检查失败，允许",
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		s.cache.Expire(ctx, key, time.Minute)
	}

	if current > 1000000 {
		logger.Warn("登录频率超出",
			logger.StringField("username", username),
			logger.Int64Field("count", current))
		return fmt.Errorf("登录频率过高，请%d秒后再试", 60)
	}

	return nil
}

func (s *userServiceImpl) checkRegisterRateLimit(ctx context.Context, username string) error {
	if s.cache == nil {
		return nil
	}

	key := fmt.Sprintf("ratelimit:register:%s", username)

	current, err := s.cache.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("注册频率检查失败，允许",
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		s.cache.Expire(ctx, key, time.Hour)
	}

	if current > 1000000 {
		logger.Warn("注册频率超出",
			logger.StringField("username", username),
			logger.Int64Field("count", current))
		return fmt.Errorf("注册过于频繁，请%v小时后再试", 1)
	}

	return nil
}
