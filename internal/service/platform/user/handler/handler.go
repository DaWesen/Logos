package handler

import (
	"context"
	"time"

	"Logos/internal/service/platform/user/model"
	"Logos/internal/service/platform/user/service"
	"Logos/pkg/logger"
	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/user"
)

// UserServiceImpl implements the UserService interface.
type UserServiceImpl struct {
	pb.UnimplementedUserServiceServer
	UserService service.UserService
}

func convertModelUserToCommonUser(mu *model.User) *pbCommon.User {
	if mu == nil {
		return nil
	}
	cu := &pbCommon.User{
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

func buildSuccessBaseResp() *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

// Register implements the UserServiceImpl interface.
func (s *UserServiceImpl) Register(ctx context.Context, req *pb.RegisterReq) (*pb.LoginRegisterResp, error) {
	resp := &pb.LoginRegisterResp{}
	email := req.GetEmail()
	phone := req.GetPhone()

	u, token, expireAt, err := s.UserService.Register(ctx, req.Username, req.Password, email, phone)
	if err != nil {
		logger.Error("register failed: %w", logger.ErrorField(err))
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
func (s *UserServiceImpl) Login(ctx context.Context, req *pb.LoginReq) (*pb.LoginRegisterResp, error) {
	resp := &pb.LoginRegisterResp{}

	u, token, expireAt, err := s.UserService.Login(ctx, req.Username, req.Password)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
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
func (s *UserServiceImpl) GetUserInfo(ctx context.Context, req *pb.UserInfoReq) (*pb.UserInfoResp, error) {
	resp := &pb.UserInfoResp{}

	u, err := s.UserService.GetUserInfo(ctx, req.UserId)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	return resp, nil
}

// GetUserInfoByUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserInfoByUsername(ctx context.Context, req *pb.UserInfoByUsernameReq) (*pb.UserInfoResp, error) {
	resp := &pb.UserInfoResp{}

	u, err := s.UserService.GetUserInfoByUsername(ctx, req.Username)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.User = convertModelUserToCommonUser(u)
	return resp, nil
}

// BatchGetUserInfo implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchGetUserInfo(ctx context.Context, req *pb.BatchUserInfoReq) (*pb.BatchUserInfoResp, error) {
	resp := &pb.BatchUserInfoResp{}

	userMap, err := s.UserService.BatchGetUserInfo(ctx, req.UserIds)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Users = make(map[int64]*pbCommon.User)
	for id, u := range userMap {
		resp.Users[id] = convertModelUserToCommonUser(u)
	}
	return resp, nil
}

// UpdateUser implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateUser(ctx context.Context, req *pb.UpdateUserReq) (*pbCommon.BaseResp, error) {
	email := req.GetEmail()
	phone := req.GetPhone()
	avatar := req.GetAvatar()
	oldPassword := req.GetOldPassword()
	newPassword := req.GetNewPassword()

	err := s.UserService.UpdateUser(ctx, req.UserId, email, phone, avatar, req.Preferences, req.Interests, oldPassword, newPassword)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// UpdateAvatar implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateAvatar(ctx context.Context, req *pb.UpdateAvatarReq) (*pbCommon.BaseResp, error) {
	avatarStr := string(req.AvatarData)
	err := s.UserService.UpdateAvatar(ctx, req.UserId, avatarStr)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// CheckUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) CheckUsername(ctx context.Context, req *pb.CheckUsernameReq) (*pb.CheckUsernameResp, error) {
	resp := &pb.CheckUsernameResp{}

	available, err := s.UserService.CheckUsername(ctx, req.Username)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Available = available
	return resp, nil
}

// BatchCheckUsernames implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchCheckUsernames(ctx context.Context, req *pb.BatchCheckUsernamesReq) (*pb.BatchCheckUsernamesResp, error) {
	resp := &pb.BatchCheckUsernamesResp{}

	availableMap, err := s.UserService.BatchCheckUsernames(ctx, req.Usernames)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.AvailableMap = availableMap
	return resp, nil
}

// GetUserStats implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserStats(ctx context.Context, req *pb.UserStatsReq) (*pb.UserStatsResp, error) {
	resp := &pb.UserStatsResp{}

	stats, total, err := s.UserService.GetUserStats(ctx, req.UserId)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Stats = &pb.UserStats{
		UserId:              stats.UserID,
		QuestionCount:       stats.QuestionCount,
		AnswerCount:         stats.AnswerCount,
		RecommendationCount: stats.RecommendationCount,
	}
	resp.TotalUserCount = total
	return resp, nil
}

// SearchUsers implements the UserServiceImpl interface.
func (s *UserServiceImpl) SearchUsers(ctx context.Context, req *pb.SearchUsersReq) (*pb.SearchUsersResp, error) {
	resp := &pb.SearchUsersResp{}

	users, total, err := s.UserService.SearchUsers(ctx, req.Keyword, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Users = make([]*pbCommon.User, 0, len(users))
	for _, u := range users {
		resp.Users = append(resp.Users, convertModelUserToCommonUser(u))
	}
	resp.Total = total
	return resp, nil
}

// VerifyToken implements the UserServiceImpl interface.
func (s *UserServiceImpl) VerifyToken(ctx context.Context, req *pb.VerifyTokenReq) (*pb.VerifyTokenResp, error) {
	_, err := s.UserService.VerifyToken(ctx, req.Token)
	return &pb.VerifyTokenResp{Valid: err == nil}, nil
}
