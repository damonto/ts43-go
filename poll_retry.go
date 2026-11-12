package ts43

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func configurationRetryDelay(entitlement Entitlement, err error) (time.Duration, bool) {
	httpErr, ok := errors.AsType[*HTTPError](err)
	if !entitlement.SupportRetryAfter || !ok || httpErr.StatusCode != http.StatusServiceUnavailable {
		return 0, false
	}
	if entitlement.UseServerRetryAfterDelay {
		// Pixel interprets Retry-After as seconds. HTTP dates and malformed
		// values fall back to the carrier interval, without refreshing tokens.
		seconds, err := strconv.ParseInt(strings.TrimSpace(httpErr.Header.Get("Retry-After")), 10, 64)
		const maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)
		if err == nil && seconds >= 0 && seconds <= maxRetryAfterSeconds {
			return time.Duration(seconds) * time.Second, true
		}
	}
	interval := entitlement.PollInterval
	return interval, interval > 0
}
