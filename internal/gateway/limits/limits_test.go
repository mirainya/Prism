package limits

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestApplyModelMaxOutputTokens(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:gateway_limits_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.GwModelMeta{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	if err := db.Create(&model.GwModelMeta{ModelName: "doubao", MaxTokens: 131072}).Error; err != nil {
		t.Fatal(err)
	}

	requested := 200000
	request := ApplyModelMaxOutputTokens(canonical.Request{Model: "doubao", MaxOutputTokens: &requested}, "doubao")
	if request.MaxOutputTokens == nil || *request.MaxOutputTokens != 131072 {
		t.Fatalf("max output = %#v, want 131072", request.MaxOutputTokens)
	}

	withinLimit := 1024
	unchanged := ApplyModelMaxOutputTokens(canonical.Request{Model: "doubao", MaxOutputTokens: &withinLimit}, "doubao")
	if unchanged.MaxOutputTokens == nil || *unchanged.MaxOutputTokens != withinLimit {
		t.Fatalf("within-limit output = %#v, want %d", unchanged.MaxOutputTokens, withinLimit)
	}
}
