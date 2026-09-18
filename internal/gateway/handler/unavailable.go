package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/routing"
)

// describeModelUnavailable turns a 503 into something a caller can act on.
//
// All three downstream protocols answered ErrNoRoute with the same fixed
// sentence, which cannot distinguish "this model has no working route at all"
// from "every route is resting after upstream rejections and will come back in
// four minutes". When the selector managed to diagnose the latter, say so and
// set Retry-After; otherwise return the caller's base message unchanged.
//
// Nothing here names a credential, pool or channel: the client learns that the
// outage is temporary and how long it lasts, and no more.
func describeModelUnavailable(c *gin.Context, err error, base string) string {
	detail, ok := routing.NoRouteDetailFrom(err)
	if !ok {
		return base
	}
	c.Header("Retry-After", strconv.FormatInt(detail.RetryAfterSeconds, 10))
	return base + detail.PublicSuffix()
}
