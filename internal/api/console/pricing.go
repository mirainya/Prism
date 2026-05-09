package console

import (
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

var pricingService = service.NewPricingService()

// GetPricing 获取公开的价格列表
func GetPricing(c *gin.Context) {
	result, err := pricingService.GetPricing()
	if err != nil {
		resp.ErrorMsg(c, 500, 500, err.Error())
		return
	}
	resp.Success(c, result)
}
