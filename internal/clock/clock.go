// Package clock 提供可注入的时间来源，生产用系统时钟，测试用固定时钟。
package clock

import "time"

// Clock 返回当前时刻。实现应返回 UTC time.Time。
type Clock interface {
	Now() time.Time
}

// System 是墙钟实现。
type System struct{}

// Now 返回当前 UTC 时刻。
func (System) Now() time.Time { return time.Now().UTC() }

// Fixed 始终返回同一时刻，用于测试“已发生记录不可改期”等规则。
type Fixed struct{ T time.Time }

// Now 返回固定时刻。
func (f Fixed) Now() time.Time { return f.T }
