package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

// Read once at startup so the middleware and UI always use the same limit.
func uploadLimitFromEnv() (int64, error) {
	value := os.Getenv("MAX_UPLOAD_MB")
	if value == "" {
		return 100 << 20, nil
	}
	mb, err := strconv.ParseInt(value, 10, 64)
	if err != nil || mb <= 0 || mb > math.MaxInt64>>20 {
		return 0, fmt.Errorf("MAX_UPLOAD_MB must be a positive integer no greater than %d", int64(math.MaxInt64>>20))
	}
	return mb << 20, nil
}
