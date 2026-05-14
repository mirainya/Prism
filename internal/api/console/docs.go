package console

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

// DocsListModels 文档页 - 获取所有模型（含 param_schema）
func DocsListModels(c *gin.Context) {
	svc := service.NewQueryService()
	result, err := svc.ListAvailableCapabilities("", "")
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, result)
}
