package handler

import (
	"Noah/internal/user/service"
	common "Noah/kitex_gen/common"
	user "Noah/kitex_gen/user"
	"context"
)

// UserServiceImpl implements the last service interface defined in the IDL.
type UserServiceImpl struct {
	UserService service.UserService
}

// Register implements the UserServiceImpl interface.
func (s *UserServiceImpl) Register(ctx context.Context, req *user.RegisterReq) (resp *user.LoginRegisterResp, err error) {
	return
}

// Login implements the UserServiceImpl interface.
func (s *UserServiceImpl) Login(ctx context.Context, req *user.LoginReq) (resp *user.LoginRegisterResp, err error) {
	return
}

// GetUserInfo implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserInfo(ctx context.Context, req *user.UserInfoReq) (resp *user.UserInfoResp, err error) {
	return
}

// GetUserInfoByUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserInfoByUsername(ctx context.Context, req *user.UserInfoByUsernameReq) (resp *user.UserInfoResp, err error) {
	return
}

// BatchGetUserInfo implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchGetUserInfo(ctx context.Context, req *user.BatchUserInfoReq) (resp *user.BatchUserInfoResp, err error) {
	return
}

// UpdateUser implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateUser(ctx context.Context, req *user.UpdateUserReq) (resp *common.BaseResp, err error) {
	return
}

// UpdateAvatar implements the UserServiceImpl interface.
func (s *UserServiceImpl) UpdateAvatar(ctx context.Context, req *user.UpdateAvatarReq) (resp *common.BaseResp, err error) {
	return
}

// CheckUsername implements the UserServiceImpl interface.
func (s *UserServiceImpl) CheckUsername(ctx context.Context, req *user.CheckUsernameReq) (resp *user.CheckUsernameResp, err error) {
	return
}

// BatchCheckUsernames implements the UserServiceImpl interface.
func (s *UserServiceImpl) BatchCheckUsernames(ctx context.Context, req *user.BatchCheckUsernamesReq) (resp *user.BatchCheckUsernamesResp, err error) {
	return
}

// GetUserStats implements the UserServiceImpl interface.
func (s *UserServiceImpl) GetUserStats(ctx context.Context, req *user.UserStatsReq) (resp *user.UserStatsResp, err error) {
	return
}

// SearchUsers implements the UserServiceImpl interface.
func (s *UserServiceImpl) SearchUsers(ctx context.Context, req *user.SearchUsersReq) (resp *user.SearchUsersResp, err error) {
	return
}

// VerifyToken implements the UserServiceImpl interface.
func (s *UserServiceImpl) VerifyToken(ctx context.Context, token string) (resp bool, err error) {
	return
}
