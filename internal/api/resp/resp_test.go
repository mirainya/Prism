package resp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseUintParamRejectsTrailingCharactersAndZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, value := range []string{"12x", "0", "-1", "1.0"} {
		t.Run(value, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: value}}
			if parsed, err := ParseUintParam(ctx, "id"); err == nil || parsed != 0 {
				t.Fatalf("parsed=%d err=%v", parsed, err)
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestParseUintParamAcceptsCanonicalPositiveValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "42"}}
	parsed, err := ParseUintParam(ctx, "id")
	if err != nil || parsed != 42 {
		t.Fatalf("parsed=%d err=%v", parsed, err)
	}
	if recorder.Code != 200 {
		t.Fatalf("status=%d, want untouched recorder", recorder.Code)
	}
}

func TestParseOptionalUintQueryRejectsZeroAndTrailingCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"0", "12x"} {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/?id="+raw, nil)
		if parsed, err := ParseOptionalUintQuery(ctx, "id"); err == nil || parsed != 0 {
			t.Fatalf("raw=%q parsed=%d err=%v", raw, parsed, err)
		}
	}
}
