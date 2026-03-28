package handler

import (
	"Noah/internal/user/model"
	"Noah/internal/user/service"
	"Noah/pkg/logger"
	common "Noah/kitex_gen/common"
	user "Noah/kitex_gen/user"
	"context"
	"time"
)

// UserServiceImpl implements the last service interface defined in the IDL.
type UserServiceImpl struct {
	UserService service.UserService
}

func convertModelUserToCommonUser(mu *model.User) *common.User {
	if mu == nil {
		return nil
	}
	cu := &common.User{
		Id:        mu.ID,
		Username:  mu.Username,
		CreatedAt: mu.CreatedAt.Unix(),
		UpdatedAt: mu.UpdatedAt.Unix(),
	}
	if mu.Email != nil {
		cu.Email = mu.Email
	}
	if mu.Phone != nil {
		cu.Phone = mu.Phone
	}
	if mu.Avatar != nil {
		cu.Avatar = mu.Avatar
	}
	if mu.Preferences != nil {
		cu.Preferences = map[string]string(mu.Preferences)
	}
	if mu.Interests != nil {
		cu.Interests = []string(mu.Interests)
	}
	return cu
}

func buildSuccessBaseResp() *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

// Register implements the UserServiceImpl interface.
func (s *UserServiceImpl) Register(ctx context.Context, req *user.RegisterReq) (resp *user.LoginRegisterResp, err error) {
	resp = user.NewLoginRegisterResp()
	email := req.GetEmail()
	phone := req.GetPhone()

	u, token, expireAt, err := s.UserService.Register(ctx, req.Username, req.Password, email, phone)
	if err != nil {
		logger.Error("注册失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	resp.Token = token
	resp.ExpireAt = expireAt
	return resp, nil
}

// Login implements the UserServiceImpl interface.
func (s *UserServiceImpl) Login(ctx context.Context, req *user.LoginReq) (resp *user.LoginRegisterResp, err error) {
	resp = user.NewLoginRegisterResp()

	u, token, expireAt, err := s.UserService.Login(ctx, req.Account, req.Password)
	if err != nil {
		logger.Error("登录失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	resp.Token = token
	resp.ExpireAt = expireAt
	return resp, nil
}

// GetUserInfo implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserInfo(ctx context.Context, req *user.UserInfoReq) (resp *user.UserInfoResp, err error) {
	resp = user.NewUserInfoResp()

	u, err := s.UserService.GetUserInfo(ctx, req.UserId)
	if err != nil {
		logger.Error("获取用户信息失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	return resp, nil
}

// GetUserInfoByUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserInfoByUsername(ctx context.Context, req *user.UserInfoByUsernameReq) (resp *user.UserInfoResp, err error) {
	resp = user.NewUserInfoResp()

	u, err := s.UserService.GetUserInfoByUsername(ctx, req.Username)
	if err != nil {
		logger.Error("通过用户名获取用户信息失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	return resp, nil
}

// BatchGetUserInfo implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchGetUserInfo(ctx context.Context, req *user.BatchUserInfoReq) (resp *user.BatchUserInfoResp, err error) {
	resp = user.NewBatchUserInfoResp()

	userMap, err := s.UserService.BatchGetUserInfo(ctx, req.UserIds)
	if err != nil {
		logger.Error("批量获取用户信息失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Users = make(map[int64]*common.User)
	for id, u := range userMap {
		resp.Users[id] = convertModelUserToCommonUser(u)
	}
	return resp, nil
}

// UpdateUser implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateUser(ctx context.Context, req *user.UpdateUserReq) (resp *common.BaseResp, err error) {
	resp = common.NewBaseResp()

	email := req.GetEmail()
	phone := req.GetPhone()
	avatar := req.GetAvatar()
	oldPassword := req.GetOldPassword()
	newPassword := req.GetNewPassword_()

	err = s.UserService.UpdateUser(ctx, req.UserId, email, phone, avatar, req.Preferences, req.Interests, oldPassword, newPassword)
	if err != nil {
		logger.Error("更新用户信息失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// UpdateAvatar implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateAvatar(ctx context.Context, req *user.UpdateAvatarReq) (resp *common.BaseResp, err error) {
	resp = common.NewBaseResp()

	avatarStr := string(req.AvatarData)
	err = s.UserService.UpdateAvatar(ctx, req.UserId, avatarStr)
	if err != nil {
		logger.Error("更新头像失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// CheckUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) CheckUsername(ctx context.Context, req *user.CheckUsernameReq) (resp *user.CheckUsernameResp, err error) {
	resp = user.NewCheckUsernameResp()

	available, err := s.UserService.CheckUsername(ctx, req.Username)
	if err != nil {
		logger.Error("检查用户名失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Available = available
	return resp, nil
}

// BatchCheckUsernames implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchCheckUsernames(ctx context.Context, req *user.BatchCheckUsernamesReq) (resp *user.BatchCheckUsernamesResp, err error) {
	resp = user.NewBatchCheckUsernamesResp()

	availableMap, err := s.UserService.BatchCheckUsernames(ctx, req.Usernames)
	if err != nil {
		logger.Error("批量检查用户名失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.AvailableMap = availableMap
	return resp, nil
}

// GetUserStats implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserStats(ctx context.Context, req *user.UserStatsReq) (resp *user.UserStatsResp, err error) {
	resp = user.NewUserStatsResp()

	stats, total, err := s.UserService.GetUserStats(ctx, req.UserId)
	if err != nil {
		logger.Error("获取用户统计信息失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Stats = &user.UserStats{
		UserId:              stats.UserID,
		QuestionCount:       stats.QuestionCount,
		AnswerCount:         stats.AnswerCount,
		RecommendationCount: stats.RecommendationCount,
	}
	resp.TotalUserCount = total
	return resp, nil
}

// SearchUsers implements the UserServiceImpl interface.
func (s *UserServiceImpl) SearchUsers(ctx context.Context, req *user.SearchUsersReq) (resp *user.SearchUsersResp, err error) {
	resp = user.NewSearchUsersResp()

	users, total, err := s.UserService.SearchUsers(ctx, req.Keyword, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("搜索用户失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Users = make([]*common.User, 0, len(users))
	for _, u := range users {
		resp.Users = append(resp.Users, convertModelUserToCommonUser(u))
	}
	resp.Total = total
	return resp, nil
}

// VerifyToken implements the UserServiceImpl interface.
func (s *UserServiceImpl) VerifyToken(ctx context.Context, token string) (resp bool, err error) {
	_, err = s.UserService.VerifyToken(ctx, token)
	return err == nil, nil
}
