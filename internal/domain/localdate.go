package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// LocalDate 是按展馆当地日历解释的日期（不含时区与时刻）。
// 权利声明的日期窗口必须用场地时区把时刻换算为当地日期后再比较，
// 不能直接用 UTC 日期。
type LocalDate struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseLocalDate 解析 YYYY-MM-DD。
func ParseLocalDate(s string) (LocalDate, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return LocalDate{}, fmt.Errorf("localdate %q: %w", s, err)
	}
	return LocalDate{t.Year(), t.Month(), t.Day()}, nil
}

// String 输出 YYYY-MM-DD。
func (d LocalDate) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// MarshalJSON 实现日期的 JSON 序列化。
func (d LocalDate) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON 实现日期的 JSON 反序列化。
func (d *LocalDate) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseLocalDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Before 报告 d 是否早于 other。
func (d LocalDate) Before(other LocalDate) bool {
	if d.Year != other.Year {
		return d.Year < other.Year
	}
	if d.Month != other.Month {
		return d.Month < other.Month
	}
	return d.Day < other.Day
}

// Equal 报告两个日期是否相同。
func (d LocalDate) Equal(other LocalDate) bool {
	return d == other
}

// After 报告 d 是否晚于 other。
func (d LocalDate) After(other LocalDate) bool {
	return other.Before(d)
}
