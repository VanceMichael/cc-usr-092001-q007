package domain

import (
	"testing"
	"time"
)

func tz(hour int) time.Time {
	return time.Date(2026, 9, 19, hour, 0, 0, 0, time.UTC)
}

func TestOverlapsHalfOpen(t *testing.T) {
	cases := []struct {
		name           string
		aS, aE, bS, bE time.Time
		want           bool
	}{
		{"包含", tz(1), tz(5), tz(2), tz(3), true},
		{"部分重叠", tz(1), tz(3), tz(2), tz(4), true},
		{"首尾相接不重叠", tz(1), tz(3), tz(3), tz(5), false},
		{"完全分离", tz(1), tz(2), tz(3), tz(4), false},
		{"边界相接反向", tz(3), tz(5), tz(1), tz(3), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Overlaps(tc.aS, tc.aE, tc.bS, tc.bE); got != tc.want {
				t.Fatalf("Overlaps=%v want %v", got, tc.want)
			}
		})
	}
}

func TestRightAllowsAt(t *testing.T) {
	start := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	r := Right{
		Countries: []string{"CN"}, Media: []string{MediumPhoto},
		NotBefore: &start, NotAfter: &end,
	}
	cases := []struct {
		country, medium string
		at              time.Time
		want            bool
	}{
		{"CN", MediumPhoto, start.Add(time.Hour), true},
		{"FR", MediumPhoto, start.Add(time.Hour), false},  // 地域
		{"CN", MediumVideo, start.Add(time.Hour), false},  // 媒介
		{"CN", MediumPhoto, start.Add(-time.Hour), false}, // 早于窗口
		{"CN", MediumPhoto, end, false},                   // NotAfter 为半开边界
		{"CN", MediumPhoto, end.Add(-time.Nanosecond), true},
	}
	for _, tc := range cases {
		if got := r.AllowsAt(tc.country, tc.medium, tc.at); got != tc.want {
			t.Fatalf("AllowsAt(%s,%s,%s)=%v want %v", tc.country, tc.medium, tc.at, got, tc.want)
		}
	}
	if !r.CoversInterval("CN", MediumPhoto, start, end) {
		t.Fatal("区间终点前一瞬仍在窗口内，应视为覆盖")
	}

	revoked := r
	revoked.Revoked = true
	if revoked.AllowsAt("CN", MediumPhoto, start.Add(time.Hour)) {
		t.Fatal("撤回的声明不得再授权")
	}

	wildcard := Right{Countries: []string{CountryAll}, Media: []string{MediumAll}}
	if !wildcard.AllowsAt("FR", MediumExpFilm, tz(12)) {
		t.Fatal("通配地域与媒介应放行")
	}
}

func TestInVenueTimezone(t *testing.T) {
	// 以欧洲中部 02:00（UTC+2）提交，平遥应为当天 08:00。
	submitted := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC).Add(2 * time.Hour)
	loc, _ := time.LoadLocation("Europe/Paris")
	submitted = time.Date(2026, 9, 19, 2, 0, 0, 0, loc)

	local, clock, err := InVenueTimezone(submitted, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if clock != "2026-09-19T08:00:00 Asia/Shanghai" {
		t.Fatalf("当地落位=%q", clock)
	}
	if _, offset := local.Zone(); offset != 8*3600 {
		t.Fatalf("落位偏移=%d", offset)
	}
	if _, _, err := InVenueTimezone(submitted, "Mars/Olympus"); err == nil {
		t.Fatal("未知时区应报错")
	}
}

func TestPayloadDigestCanonical(t *testing.T) {
	a := map[string]any{"x": 1, "y": "z"}
	b := map[string]any{"y": "z", "x": 1}
	d1, _ := PayloadDigest(a)
	d2, _ := PayloadDigest(b)
	if d1 != d2 {
		t.Fatal("键序不同的同一负载必须产生相同摘要")
	}
	if DigestBytes([]byte("hello")) == d1 {
		t.Fatal("不同内容摘要意外相同")
	}
}
