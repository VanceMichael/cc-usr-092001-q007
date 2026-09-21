package domain

import (
	"fmt"
	"time"
)

// InLocation 把时刻装入指定 IANA 时区，保留同一物理瞬间。
// zone 无效时返回错误，避免静默退回到 UTC 造成跨时区落位错误。
func InLocation(t time.Time, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("timezone %q: %w", zone, err)
	}
	return t.In(loc), nil
}

// LocalDateAt 把绝对时刻换算为某时区下的当地日历日期。
func LocalDateAt(t time.Time, zone string) (LocalDate, error) {
	local, err := InLocation(t, zone)
	if err != nil {
		return LocalDate{}, err
	}
	return LocalDate{local.Year(), local.Month(), local.Day()}, nil
}

// Overlaps 报告两个半开区间 [aStart,aEnd) 与 [bStart,bEnd) 是否重叠。
// 首尾相接（aEnd == bStart）不算重叠，因此同一实体可以背靠背使用。
func Overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// ContainsCountry 报告 code 是否在允许列表内；列表为空表示不限地域。
func ContainsCountry(allowed []string, code string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, c := range allowed {
		if c == code {
			return true
		}
	}
	return false
}

// MediaAllowed 报告媒介是否被允许；允许列表为空表示不限媒介。
func MediaAllowed(allowed []Media, m Media) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == m {
			return true
		}
	}
	return false
}

// GrantAllows 在给定国家、媒介与绝对时刻下判断授权是否允许公开展示。
// 时刻先按 venueZone 换算为当地日期，再与授权日期窗口比较。
// 已撤回的授权一律拒绝。
func GrantAllows(g *RightsGrant, country string, media Media, at time.Time, venueZone string) bool {
	if g == nil || g.Withdrawn {
		return false
	}
	if !ContainsCountry(g.AllowedCountries, country) {
		return false
	}
	if !MediaAllowed(g.AllowedMedia, media) {
		return false
	}
	day, err := LocalDateAt(at, venueZone)
	if err != nil {
		return false
	}
	return !day.Before(g.ValidFrom) && !day.After(g.ValidTo)
}
