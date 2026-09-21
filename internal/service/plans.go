package service

import (
	"fmt"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// openDamagePlan 处理实体/数字组成件损坏：封存、报险、评估、换场、权利确认。
// 返回处置方案编号与受影响排期数。
func (s *Service) openDamagePlan(componentRef, note, eventID string, at time.Time) (string, int) {
	if _, ok := s.store.GetComponent(componentRef); !ok {
		return "", 0
	}
	affected := s.slotsUsingComponent(componentRef, at)

	steps := []domain.PlanStep{
		{Seq: 1, Kind: "seal", Description: "立即停止展出并封存受损组成件 " + componentRef + "，保持现场", Status: domain.PlanOpen,
			Deadline: ptrTime(at.Add(24 * time.Hour))},
		{Seq: 2, Kind: "insurance_claim", Description: "通知保险方报案，固定现场影像与台账事件证据", Status: domain.PlanOpen},
		{Seq: 3, Kind: "assess", Description: "评估修复可行性；不可修复则撤展或启用同媒介替代作品", Status: domain.PlanOpen},
		{Seq: 4, Kind: "reschedule", Description: "对受影响的未来排期执行换场或取消（留下换场审计记录）", Status: domain.PlanOpen},
		{Seq: 5, Kind: "notify", Description: "向权利人与借展方发出损坏书面通知并取得签收", Status: domain.PlanOpen},
	}
	alts := s.alternativesForSlots(affected)
	plan := domain.Plan{
		Ref: s.store.NextID("PLAN"), Trigger: domain.TriggerDamage, SubjectRef: componentRef,
		AffectedSlots: slotRefs(affected), Status: domain.PlanOpen, CreatedAt: at,
		TriggerEvent: eventID, Steps: steps, Alternatives: alts,
		Evidence: []string{eventID}, Notes: note,
	}
	s.store.PutPlan(plan)
	return plan.Ref, len(affected)
}

// openWithdrawalPlan 处理权利撤回：按地域/媒介/日期边界停展，再补权或替换。
func (s *Service) openWithdrawalPlan(r domain.Right, reason, eventID string, at time.Time) (string, int) {
	var affected []domain.Slot
	for _, sl := range s.store.SlotsForWork(r.WorkRef) {
		if !sl.End.After(at) {
			continue // 已结束的展出是历史记录，不受撤回影响
		}
		v, ok := s.store.GetVenue(sl.VenueRef)
		if !ok {
			continue
		}
		// 其余有效声明仍完整覆盖该排期时，撤回不影响该场展出。
		if s.coveredByOtherRights(r.WorkRef, r.Ref, v, sl.Start, sl.End) {
			continue
		}
		affected = append(affected, sl)
	}

	steps := []domain.PlanStep{
		{Seq: 1, Kind: "halt_display", Description: fmt.Sprintf("在声明覆盖的地域/媒介立即停止作品 %s 的公开展示与播放", r.WorkRef), Status: domain.PlanOpen,
			Deadline: ptrTime(at.Add(12 * time.Hour))},
		{Seq: 2, Kind: "remove_materials", Description: "撤下公开展示物料，关停线上/线下播放并记录撤下时间", Status: domain.PlanOpen},
		{Seq: 3, Kind: "reauthorize_or_replace", Description: "重新取得书面授权，或改用有完整权利覆盖的替代作品", Status: domain.PlanOpen},
		{Seq: 4, Kind: "insurance_loan", Description: "同步保险方与借展方：撤展引起的保单与协议变更", Status: domain.PlanOpen},
		{Seq: 5, Kind: "record", Description: "形成撤回处置记录并归入作品展陈轨迹（不可删除）", Status: domain.PlanOpen},
	}
	plan := domain.Plan{
		Ref: s.store.NextID("PLAN"), Trigger: domain.TriggerWithdrawal, SubjectRef: r.Ref,
		AffectedSlots: slotRefs(affected), Status: domain.PlanOpen, CreatedAt: at,
		TriggerEvent: eventID, Steps: steps, Alternatives: s.alternativesForSlots(affected),
		Evidence: []string{eventID}, Notes: reason,
	}
	s.store.PutPlan(plan)
	return plan.Ref, len(affected)
}

// openClosurePlan 处理场地关闭：封场、转移作品、回收设备、批改保险许可、重排。
func (s *Service) openClosurePlan(venueRef, reason, eventID string, at time.Time) (string, int) {
	var affected []domain.Slot
	for _, sl := range s.store.SlotsForVenue(venueRef) {
		if sl.End.After(at) {
			affected = append(affected, sl)
		}
	}
	steps := []domain.PlanStep{
		{Seq: 1, Kind: "secure_site", Description: "封场安全检查，清点场地内全部作品与设备", Status: domain.PlanOpen,
			Deadline: ptrTime(at.Add(6 * time.Hour))},
		{Seq: 2, Kind: "notify_parties", Description: "通知在展团队、全部相关权利人与借展方", Status: domain.PlanOpen},
		{Seq: 3, Kind: "transfer_works", Description: "将作品转移至备选场地并满足其运输条件", Status: domain.PlanOpen},
		{Seq: 4, Kind: "reclaim_equipment", Description: "回收播放与布展设备并重新分配给替代场地", Status: domain.PlanOpen},
		{Seq: 5, Kind: "amend_policy_permit", Description: "批改保险、重新申请公开展示许可", Status: domain.PlanOpen},
		{Seq: 6, Kind: "reconfirm_slots", Description: "在系统中对迁移后的排期重新确认（连带换场留痕）", Status: domain.PlanOpen},
	}
	plan := domain.Plan{
		Ref: s.store.NextID("PLAN"), Trigger: domain.TriggerClosure, SubjectRef: venueRef,
		AffectedSlots: slotRefs(affected), Status: domain.PlanOpen, CreatedAt: at,
		TriggerEvent: eventID, Steps: steps, Alternatives: s.alternativesForSlots(affected),
		Evidence: []string{eventID}, Notes: reason,
	}
	s.store.PutPlan(plan)
	return plan.Ref, len(affected)
}

// ExecutePlan 记录处置方案步骤的执行情况；全部步骤完成则方案归档为 executed。
func (s *Service) ExecutePlan(planRef string, req ExecutePlanRequest) (domain.Plan, error) {
	plan, ok := s.store.GetPlan(planRef)
	if !ok {
		return domain.Plan{}, notFound("处置方案 %s 不存在", planRef)
	}
	statusBySeq := map[int]string{}
	for _, ex := range req.Steps {
		statusBySeq[ex.Seq] = ex.Status
	}
	allDone := true
	for i := range plan.Steps {
		if st, given := statusBySeq[plan.Steps[i].Seq]; given {
			if st != "" {
				plan.Steps[i].Status = st
			}
		}
		if plan.Steps[i].Status != domain.PlanExecuted && plan.Steps[i].Status != "done" {
			allDone = false
		}
	}
	if allDone {
		plan.Status = domain.PlanExecuted
	}
	s.store.PutPlan(plan)
	return plan, nil
}

func (s *Service) slotsUsingComponent(componentRef string, at time.Time) []domain.Slot {
	var out []domain.Slot
	c, ok := s.store.GetComponent(componentRef)
	if !ok {
		return nil
	}
	for _, sl := range s.store.SlotsForWork(c.WorkRef) {
		if !sl.End.After(at) {
			continue
		}
		for _, ref := range sl.Components {
			if ref == componentRef {
				out = append(out, sl)
				break
			}
		}
	}
	return out
}

func slotRefs(slots []domain.Slot) []string {
	out := make([]string, 0, len(slots))
	for _, sl := range slots {
		out = append(out, sl.Ref)
	}
	return out
}

func ptrTime(t time.Time) *time.Time { return &t }
