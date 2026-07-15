package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/cache"
)

func TestUserSecurityChangesIncrementPersistentSessionVersion(t *testing.T) {
	setupTestDB(t)
	previousCacheClient := cache.Client
	cache.Client = nil
	t.Cleanup(func() { cache.Client = previousCacheClient })

	oldPassword, err := auth.HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	user := &model.User{
		Username:       "session-version-user",
		Password:       oldPassword,
		Role:           model.UserRoleUser,
		Status:         1,
		SessionVersion: 5,
	}
	if err := model.DB().Create(user).Error; err != nil {
		t.Fatal(err)
	}

	service := NewUserService()
	if err := service.ChangePassword(user.ID, &ChangePasswordRequest{
		OldPassword: "old-password",
		NewPassword: "new-password",
	}); err != nil {
		t.Fatal(err)
	}

	var current model.User
	if err := model.DB().First(&current, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.SessionVersion != 6 {
		t.Fatalf("session version after password change=%d, want 6", current.SessionVersion)
	}
	if !auth.CheckPassword("new-password", current.Password) || auth.CheckPassword("old-password", current.Password) {
		t.Fatal("password was not replaced")
	}

	if err := service.UpdateUserRole(user.ID, model.UserRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateUserStatus(user.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := model.DB().First(&current, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.SessionVersion != 8 || current.Role != model.UserRoleAdmin || current.Status != 0 {
		t.Fatalf("current user=%#v", current)
	}
}
