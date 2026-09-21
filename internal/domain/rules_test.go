package domain

import (
	"testing"
	"time"
)

func TestLocalDateParseAndOrder(t *testing.T) {
	d1, err := ParseLocalDate("2026-09-01")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d1.String() != "2026-09-01" {
		t.Fatalf("round trip = %s", d1.String())
	}
	d2 := LocalDate{Year: 2026, Month: time.September, Day: 2}
	if !d1.Before(d2) || d2.Before(d1) || d1.Equal(d2) {
		t.Fatalf("date ordering broken: %s %s", d1, d2)
	}
	yearEdge := LocalDate{Year: 2027, Month: time.January, Day: 1}
	if !d2.Before(yearEdge) {
		t.Fatalf("year boundary ordering broken")
	}
	if _, err := ParseLocalDate("2026/09/01"); err == nil {
		t.Fatalf("expected parse error for wrong shape")
	}
}

func TestOverlapsHalfOpen(t *testing.T) {
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	h := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	if !Overlaps(h(0), h(2), h(1), h(3)) {
		t.Fatalf("partial overlap should overlap")
	}
	if Overlaps(h(0), h(2), h(2), h(4)) {
		t.Fatalf("back-to-back [0,2) and [2,4) must not overlap")
	}
	if Overlaps(h(0), h(2), h(3), h(4)) {
		t.Fatalf("disjoint intervals must not overlap")
	}
	if !Overlaps(h(0), h(5), h(1), h(2)) {
		t.Fatalf("contained interval should overlap")
	}
}

func TestGrantAllows(t *testing.T) {
	g := &RightsGrant{
		AllowedCountries: []string{"CN"},
		AllowedMedia:     []Media{MediaPhoto, MediaVideo},
		ValidFrom:        LocalDate{Year: 2026, Month: time.September, Day: 20},
		ValidTo:          LocalDate{Year: 2026, Month: time.September, Day: 30},
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	in := time.Date(2026, 9, 25, 12, 0, 0, 0, loc)

	if !GrantAllows(g, "CN", MediaPhoto, in, "Asia/Shanghai") {
		t.Fatalf("matching grant should allow")
	}
	// 日期端点闭区间：起止当天均允许。
	if !GrantAllows(g, "CN", MediaPhoto, time.Date(2026, 9, 20, 9, 0, 0, 0, loc), "Asia/Shanghai") {
		t.Fatalf("valid_from day should allow")
	}
	if !GrantAllows(g, "CN", MediaPhoto, time.Date(2026, 9, 30, 23, 0, 0, 0, loc), "Asia/Shanghai") {
		t.Fatalf("valid_to day should allow")
	}
	if GrantAllows(g, "DE", MediaPhoto, in, "Asia/Shanghai") {
		t.Fatalf("country not covered must deny")
	}
	if GrantAllows(g, "CN", MediaBook, in, "Asia/Shanghai") {
		t.Fatalf("media not covered must deny")
	}
	if GrantAllows(g, "CN", MediaPhoto, time.Date(2026, 10, 1, 12, 0, 0, 0, loc), "Asia/Shanghai") {
		t.Fatalf("date out of window must deny")
	}
	// 空列表 = 不限地域/媒介。
	open := &RightsGrant{ValidFrom: g.ValidFrom, ValidTo: g.ValidTo}
	if !GrantAllows(open, "ZZ", MediaBook, in, "Asia/Shanghai") {
		t.Fatalf("empty restrictions should allow any country/media")
	}
	// 撤回后一律拒绝。
	withdrawn := *g
	withdrawn.Withdrawn = true
	if GrantAllows(&withdrawn, "CN", MediaPhoto, in, "Asia/Shanghai") {
		t.Fatalf("withdrawn grant must deny")
	}
	if GrantAllows(nil, "CN", MediaPhoto, in, "Asia/Shanghai") {
		t.Fatalf("nil grant must deny")
	}
}

func TestGrantAllowsUsesVenueLocalMidnight(t *testing.T) {
	// 授权窗口最后一天 9/25。UTC 16:30 在上海已是 9/26 -> 拒绝；
	// 同一瞬间在柏林仍是 9/25 -> 允许。
	g := &RightsGrant{
		ValidFrom: LocalDate{Year: 2026, Month: time.September, Day: 20},
		ValidTo:   LocalDate{Year: 2026, Month: time.September, Day: 25},
	}
	utcEvening := time.Date(2026, 9, 25, 16, 30, 0, 0, time.UTC)
	if GrantAllows(g, "CN", MediaPhoto, utcEvening, "Asia/Shanghai") {
		t.Fatalf("Shanghai local date 2026-09-26 must deny")
	}
	if !GrantAllows(g, "DE", MediaPhoto, utcEvening, "Europe/Berlin") {
		t.Fatalf("Berlin local date 2026-09-25 must allow")
	}
}
