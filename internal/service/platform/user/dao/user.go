package dao

import (
	"context"
	"errors"

	"Logos/internal/service/platform/user/model"

	"gorm.io/gorm"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByID(ctx context.Context, id int64) (*model.User, error)
	FindByUsername(ctx context.Context, username string) (*model.User, error)
	FindByEmail(ctx context.Context, email string) (*model.User, error)
	FindByPhone(ctx context.Context, phone string) (*model.User, error)
	Update(ctx context.Context, user *model.User) error
	UpdatePassword(ctx context.Context, id int64, password string) error
	UpdateAvatar(ctx context.Context, id int64, avatar string) error
	Delete(ctx context.Context, id int64) error
	ListByIDs(ctx context.Context, ids []int64) ([]*model.User, error)
	BatchGetByIDs(ctx context.Context, ids []int64) (map[int64]*model.User, error)
	CheckUsernameExists(ctx context.Context, username string) (bool, error)
	BatchCheckUsername(ctx context.Context, usernames []string) (map[string]bool, error)
	Search(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error)
	Count(ctx context.Context) (int64, error)
	GetStats(ctx context.Context, userID int64) (*model.UserStats, error)
	WithTransaction(ctx context.Context, fn func(txRepo UserRepository) error) error
}

type userRepositoryImpl struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepositoryImpl{db: db}
}

func (r *userRepositoryImpl) Create(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *userRepositoryImpl) FindByID(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).First(&user, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func (r *userRepositoryImpl) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func (r *userRepositoryImpl) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func (r *userRepositoryImpl) FindByPhone(ctx context.Context, phone string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &user, err
}

func (r *userRepositoryImpl) Update(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

func (r *userRepositoryImpl) UpdatePassword(ctx context.Context, id int64, password string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Update("password", password).Error
}

func (r *userRepositoryImpl) UpdateAvatar(ctx context.Context, id int64, avatar string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Update("avatar", avatar).Error
}

func (r *userRepositoryImpl) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.User{}, id).Error
}

func (r *userRepositoryImpl) ListByIDs(ctx context.Context, ids []int64) ([]*model.User, error) {
	var users []*model.User
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error
	return users, err
}

func (r *userRepositoryImpl) BatchGetByIDs(ctx context.Context, ids []int64) (map[int64]*model.User, error) {
	var users []*model.User
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error
	if err != nil {
		return nil, err
	}

	result := make(map[int64]*model.User, len(users))
	for _, u := range users {
		result[u.ID] = u
	}
	return result, nil
}

func (r *userRepositoryImpl) CheckUsernameExists(ctx context.Context, username string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("username = ?", username).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *userRepositoryImpl) BatchCheckUsername(ctx context.Context, usernames []string) (map[string]bool, error) {
	var users []model.User
	result := make(map[string]bool)

	for _, username := range usernames {
		result[username] = false
	}

	err := r.db.WithContext(ctx).Select("username").Where("username IN ?", usernames).Find(&users).Error
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		result[user.Username] = true
	}

	return result, nil
}

func (r *userRepositoryImpl) Search(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error) {
	var users []*model.User
	var total int64
	offset := (page - 1) * pageSize

	db := r.db.WithContext(ctx)
	if keyword != "" {
		db = db.Where("username LIKE ? OR email LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}

	if err := db.Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := db.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&users).Error
	return users, total, err
}

func (r *userRepositoryImpl) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Count(&count).Error
	return count, err
}

func (r *userRepositoryImpl) GetStats(ctx context.Context, userID int64) (*model.UserStats, error) {
	stats := &model.UserStats{
		UserID:              userID,
		QuestionCount:       0,
		AnswerCount:         0,
		RecommendationCount: 0,
	}
	return stats, nil
}

func (r *userRepositoryImpl) WithTransaction(ctx context.Context, fn func(txRepo UserRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &userRepositoryImpl{db: tx}
		return fn(txRepo)
	})
}
