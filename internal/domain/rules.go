package domain

import (
	"errors"
	"fmt"
	"time"
)

// 规则校验返回的可判定错误，服务层直接转成 4xx 响应。
var (
	ErrBadInterval      = errors.New("时段结束必须晚于开始")
	ErrOverlap          = errors.New("实体组成件或设备在重叠时段被重复占用")
	ErrRightDenied      = errors.New("公开展示限制不允许该排期")
	ErrLoanWindow       = errors.New("排期超出借展协议允许的在展窗口")
	ErrVenueMedium      = errors.New("场地不接受该媒介")
	ErrVenueRequirement = errors.New("场地条件未被分配设备满足")
	ErrPlaybackMismatch = errors.New("设备无法播放组成件要求的播放规格")
	ErrVenueClosed      = errors.New("场地已关闭")
	ErrPastImmutable    = errors.New("已经发生的运输与展出记录不能被重排覆盖")
	ErrComponentInUse   = errors.New("组成件不属于该作品或不存在")
)

// Overlaps 按半开区间 [aStart,aEnd) 与 [bStart,bEnd) 判断重叠。
// 端点相接（aEnd == bStart）不算冲突。
func Overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// InVenueTimezone 把一次跨时区提交落位到展馆当地时间。
// 输入可以携带任意偏移量；返回的时间 Location 固定为展馆 IANA 时区，
// 同时返回当地挂钟的 RFC3339（不含偏移）表示，便于留痕与人工核对。
func InVenueTimezone(t time.Time, venueTimezone string) (time.Time, string, error) {
	loc, err := time.LoadLocation(venueTimezone)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("未知展馆时区 %q: %w", venueTimezone, err)
	}
	local := t.In(loc)
	return local, local.Format("2006-01-02T15:04:05") + " " + loc.String(), nil
}

// LocalClock 只返回展馆当地挂钟字符串。
func LocalClock(t time.Time) string {
	return t.Format("2006-01-02T15:04:05")
}

// ValidationIssue 汇总一次排期校验中发现的全部问题，便于调用方一次性展示。
type ValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Subject string `json:"subject,omitempty"`
}

// SlotContext 是校验一条排期所需的完整事实集合。
type SlotContext struct {
	Work       Work
	Venue      Venue
	Loan       *Loan
	Components []Component
	Equipment  []Equipment
	Rights     []Right
}

// ValidateSlot 对一条尚未确认的排期执行全部静态规则检查（不含占用冲突与不可变性，
// 那两类需要台账状态，由服务层补充）。检查覆盖区间每一秒所适用的：
// 地域（展馆所在国）、媒介（作品媒介）、日期（声明窗口）。
func ValidateSlot(cx SlotContext, start, end time.Time) []ValidationIssue {
	var issues []ValidationIssue
	if !end.After(start) {
		return []ValidationIssue{{Code: "bad_interval", Message: ErrBadInterval.Error()}}
	}

	if cx.Venue.Closed {
		issues = append(issues, ValidationIssue{Code: "venue_closed", Message: ErrVenueClosed.Error(), Subject: cx.Venue.Ref})
	}
	if !contains(cx.Venue.AcceptedMedia, MediumAll) && !contains(cx.Venue.AcceptedMedia, cx.Work.Medium) {
		issues = append(issues, ValidationIssue{
			Code: "venue_medium", Subject: cx.Venue.Ref,
			Message: fmt.Sprintf("%s: 场地接受 %v，作品媒介为 %s", ErrVenueMedium, cx.Venue.AcceptedMedia, cx.Work.Medium),
		})
	}

	// 权利：至少一条有效声明必须覆盖整个展出区间，且所有声明都不能被撤回后仍被引用。
	covered := false
	for _, r := range cx.Rights {
		if r.WorkRef != cx.Work.Ref || r.Revoked {
			continue
		}
		if r.CoversInterval(cx.Venue.Country, cx.Work.Medium, start, end) {
			covered = true
			break
		}
	}
	if !covered {
		issues = append(issues, ValidationIssue{Code: "right_denied", Subject: cx.Work.Ref,
			Message: fmt.Sprintf("%s: 无覆盖 %s / %s / [%s,%s) 的有效权利声明",
				ErrRightDenied, cx.Venue.Country, cx.Work.Medium, LocalClock(start), LocalClock(end))})
	}

	// 借展窗口。
	if cx.Loan != nil {
		if start.Before(cx.Loan.Start) || end.After(cx.Loan.End) {
			issues = append(issues, ValidationIssue{Code: "loan_window", Subject: cx.Loan.Ref,
				Message: fmt.Sprintf("%s: 借展窗口 [%s,%s)", ErrLoanWindow, LocalClock(cx.Loan.Start), LocalClock(cx.Loan.End))})
		}
	}

	// 场地条件必须被分配设备满足。
	provided := map[string]bool{}
	for _, eq := range cx.Equipment {
		for _, p := range eq.Provides {
			provided[p] = true
		}
	}
	for _, req := range cx.Venue.Requirements {
		if !provided[req] {
			issues = append(issues, ValidationIssue{Code: "venue_requirement", Subject: req,
				Message: fmt.Sprintf("%s: 缺少 %s", ErrVenueRequirement, req)})
		}
	}

	// 数字组成件的播放规格必须有分配设备支持；实体件只核对运输条件登记。
	// 设备的 tested_profiles 不参与静态校验，而是作为"现场试播"前置项由运营事件闭环。
	playable := map[string]bool{}
	for _, eq := range cx.Equipment {
		for _, p := range eq.PlaybackProfiles {
			playable[p] = true
		}
	}
	for _, c := range cx.Components {
		if c.Form == FormPhysical {
			if c.TransportClass == "" {
				issues = append(issues, ValidationIssue{Code: "transport_class_missing", Subject: c.Ref,
					Message: fmt.Sprintf("实体组成件 %s 缺少运输条件登记", c.Ref)})
			}
			continue
		}
		if c.PlaybackProfile == "" {
			continue
		}
		if !playable[c.PlaybackProfile] {
			issues = append(issues, ValidationIssue{Code: "playback_mismatch", Subject: c.Ref,
				Message: fmt.Sprintf("%s: 无设备支持 %s", ErrPlaybackMismatch, c.PlaybackProfile)})
		}
	}

	// 已到场但标记损坏的实体件不得进入新排期。
	for _, c := range cx.Components {
		if c.Damaged {
			issues = append(issues, ValidationIssue{Code: "component_damaged", Subject: c.Ref,
				Message: fmt.Sprintf("组成件 %s 已登记损坏，须先按处置方案处理", c.Ref)})
		}
	}
	return issues
}

// CanReplay 判断既有运输/展出记录在目标时刻是否已"发生"。
// 已经开始的排期不允许重排覆盖；未开始的未来排期可以换场。
func SlotHasStarted(s Slot, now time.Time) bool {
	return !s.Start.After(now)
}
