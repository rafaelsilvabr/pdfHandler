package main

import (
	"fmt"
	"math"
	"testing"
)

func TestUploadLimitFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{"", 100 << 20},
		{"1", 1 << 20},
		{"250", 250 << 20},
		{fmt.Sprint(int64(math.MaxInt64 >> 20)), (math.MaxInt64 >> 20) << 20},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("MAX_UPLOAD_MB", tc.value)
			got, err := uploadLimitFromEnv()
			if err != nil || got != tc.want {
				t.Fatalf("uploadLimitFromEnv() = %d, %v; want %d, nil", got, err, tc.want)
			}
		})
	}
}

func TestInvalidUploadLimitPreventsStartup(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.5", "abc", "100MB", " 100 ", "8796093022208", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MAX_UPLOAD_MB", value)
			if _, err := newRouter(); err == nil {
				t.Fatalf("newRouter accepted invalid MAX_UPLOAD_MB=%q", value)
			}
		})
	}
}
