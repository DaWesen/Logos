package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"Noah/internal/user/dao"
	"Noah/internal/user/model"
	"Noah/pkg/cache"
	"Noah/pkg/es"
	"Noah/pkg/jwt"
	"Noah/pkg/logger"
	"Noah/pkg/mq"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("用户不存在")
	ErrUserAlreadyExists = errors.New("用户已存在")
	ErrInvalidPassword   = errors.New("无效密码")
	ErrWrongPassword     = errors.New("密码错误")
	ErrTokenInvalid      = errors.New("令牌无效")
	ErrInternalServer    = errors.New("服务器内部错误")
	ErrOldPasswordWrong  = errors.New("旧密码错误")
)

type UserService interface {
	Register(ctx context.Context, username, password, email, phone string) (*model.User, string, int64, error)
	Login(ctx context.Context, account, password string) (*model.User, string, int64, error)
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
	producer   *mq.Producer
	esManager  *es.ESManager
}

func NewUserService(repo dao.UserRepository, jwtManager *jwt.JWTManager, cache cache.Cache, producer *mq.Producer, esManager *es.ESManager) UserService {
	return &userServiceImpl{
		repo:       repo,
		jwtManager: jwtManager,
		cache:      cache,
		producer:   producer,
		esManager:  esManager,
	}
}

func (s *userServiceImpl) Register(ctx context.Context, username, password, email, phone string) (*model.User, string, int64, error) {
	logger.Info("用户注册请求",
		logger.StringField("username", username),
		logger.StringField("email", email))

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

	if err := s.repo.Create(ctx, u); err != nil {
		logger.Error("创建用户失败",
			logger.ErrorField(err),
			logger.StringField("username", username))
		return nil, "", 0, ErrInternalServer
	}

	token, err := s.jwtManager.GenerateToken(strconv.FormatInt(u.ID, 10), "user")
	if err != nil {
		logger.Error("生成令牌失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", u.ID))
		return nil, "", 0, ErrInternalServer
	}

	expireAt := time.Now().Add(time.Hour * 24).Unix()

	if s.producer != nil {
		eventData, _ := json.Marshal(map[string]interface{}{
			"user_id":       u.ID,
			"username":      u.Username,
			"registered_at": time.Now(),
			"email":         u.Email,
			"phone":         u.Phone,
		})
		s.producer.SendUserEvent(ctx, fmt.Sprintf("%d", u.ID), eventData)
		logger.Info("发送用户注册事件",
			logger.Int64Field("user_id", u.ID))
	}

	if s.esManager != nil {
		esUser := map[string]interface{}{
			"id":         u.ID,
			"username":   u.Username,
			"email":      u.Email,
			"phone":      u.Phone,
			"avatar":     u.Avatar,
			"created_at": u.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		err = s.esManager.AddDocument("users", fmt.Sprintf("%d", u.ID), esUser)
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

func (s *userServiceImpl) Login(ctx context.Context, account, password string) (*model.User, string, int64, error) {
	logger.Info("用户登录请求",
		logger.StringField("account", account))

	var u *model.User
	var err error

	u, err = s.repo.FindByUsername(ctx, account)
	if err != nil {
		logger.Error("通过用户名查询用户失败",
			logger.ErrorField(err),
			logger.StringField("account", account))
		return nil, "", 0, ErrInternalServer
	}
	if u == nil {
		u, err = s.repo.FindByEmail(ctx, account)
		if err != nil {
			logger.Error("通过邮箱查询用户失败",
				logger.ErrorField(err),
				logger.StringField("account", account))
			return nil, "", 0, ErrInternalServer
		}
		if u == nil {
			u, err = s.repo.FindByPhone(ctx, account)
			if err != nil {
				logger.Error("通过手机号查询用户失败",
					logger.ErrorField(err),
					logger.StringField("account", account))
				return nil, "", 0, ErrInternalServer
			}
			if u == nil {
				logger.Warn("用户不存在",
					logger.StringField("account", account))
				return nil, "", 0, ErrUserNotFound
			}
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		logger.Warn("密码错误",
			logger.StringField("account", account))
		return nil, "", 0, ErrWrongPassword
	}

	token, err := s.jwtManager.GenerateToken(strconv.FormatInt(u.ID, 10), "user")
	if err != nil {
		logger.Error("生成令牌失败",
			logger.ErrorField(err),
			logger.Int64Field("user_id", u.ID))
		return nil, "", 0, ErrInternalServer
	}

	expireAt := time.Now().Add(time.Hour * 24).Unix()

	if s.producer != nil {
		eventData, _ := json.Marshal(map[string]interface{}{
			"user_id":   u.ID,
			"username":  u.Username,
			"logged_at": time.Now(),
		})
		s.producer.SendUserEvent(ctx, fmt.Sprintf("%d", u.ID), eventData)
		logger.Info("发送用户登录事件",
			logger.Int64Field("user_id", u.ID))
	}

	logger.Info("用户登录成功",
		logger.Int64Field("user_id", u.ID),
		logger.StringField("account", account))

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
			logger.Info("用户信息存入缓存成功",
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
	logger.Info("更新用户请求",
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
			logger.Error("新密码加密失败",
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
			err = s.esManager.UpdateDocument("users", fmt.Sprintf("%d", userID), esUser)
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

	return s.repo.UpdateAvatar(ctx, userID, avatar)
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
	logger.Info("搜索用户请求",
		logger.StringField("keyword", keyword),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if s.esManager != nil {
		query := es.SearchQuery{
			Query: map[string]interface{}{
				"multi_match": map[string]interface{}{
					"query":    keyword,
					"fields":   []string{"username", "email"},
					"type":     "best_fields",
					"operator": "and",
				},
			},
			From: (page - 1) * pageSize,
			Size: pageSize,
			Sort: []map[string]interface{}{
				{
					"created_at": map[string]interface{}{
						"order": "desc",
					},
				},
			},
		}

		var searchResult es.SearchResult
		err := s.esManager.Search("users", query, &searchResult)
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
		txService := NewUserService(txRepo, s.jwtManager, s.cache, s.producer, s.esManager)
		return fn(txService)
	})
}
