package service

import (
	"errors"
	"fmt"
	"time"

	"example.com/batch-092001-q007/internal/domain"
	"example.com/batch-092001-q007/internal/store"
)

// APIError 携带可判定错误码与 HTTP 状态，API 层据此返回响应。
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func badRequest(code, format string, args ...any) *APIError {
	return &APIError{Status: 400, Code: code, Message: fmt.Sprintf(format, args...)}
}

func conflict(code, format string, args ...any) *APIError {
	return &APIError{Status: 409, Code: code, Message: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *APIError {
	return &APIError{Status: 404, Code: "not_found", Message: fmt.Sprintf(format, args...)}
}

// NotFoundf 供包外（如 API 层的直接存储读取）构造一致的 404 错误。
func NotFoundf(format string, args ...any) *APIError { return notFound(format, args...) }

func asAPIError(err error) *APIError {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	return &APIError{Status: 500, Code: "internal", Message: err.Error()}
}

// Clock 便于测试中固定当前时刻。
type Clock func() time.Time

// Service 是协调器的应用服务，持有事实库与只追加台账。
type Service struct {
	store *store.Store
	now   Clock
}

// New 创建应用服务。
func New(st *store.Store) *Service {
	return &Service{store: st, now: time.Now}
}

// WithClock 替换时钟（测试用），返回服务自身。
func (s *Service) WithClock(c Clock) *Service {
	s.now = c
	return s
}

// Store 暴露底层事实库（API 层只读列表直接使用）。
func (s *Service) Store() *store.Store { return s.store }

// ---------- 场地 / 设备登记 ----------

func (s *Service) RegisterVenue(in VenueInput) (domain.Venue, error) {
	if in.Ref == "" || in.Name == "" || in.Country == "" || in.Timezone == "" {
		return domain.Venue{}, badRequest("invalid_venue", "场地 ref/name/country/timezone 均为必填")
	}
	if _, _, err := domain.InVenueTimezone(s.now(), in.Timezone); err != nil {
		return domain.Venue{}, badRequest("invalid_timezone", "%v", err)
	}
	if len(in.AcceptedMedia) == 0 {
		in.AcceptedMedia = []string{domain.MediumAll}
	}
	v := domain.Venue{
		Ref: in.Ref, Name: in.Name, Country: in.Country, Timezone: in.Timezone,
		AcceptedMedia: in.AcceptedMedia, Requirements: in.Requirements,
	}
	s.store.PutVenue(v)
	return v, nil
}

func (s *Service) RegisterEquipment(in EquipmentInput) (domain.Equipment, error) {
	if in.Ref == "" || in.Kind == "" {
		return domain.Equipment{}, badRequest("invalid_equipment", "设备 ref/kind 为必填")
	}
	e := domain.Equipment{
		Ref: in.Ref, Kind: in.Kind, PlaybackProfiles: in.PlaybackProfiles,
		Provides: in.Provides, TestedProfiles: in.TestedProfiles, Notes: in.Notes,
	}
	s.store.PutEquipment(e)
	return e, nil
}

// ---------- 院校批量清单（幂等） ----------

// SubmitBatch 受理一所院校的批量重传清单。
// 幂等键为 (source=school:<school_ref>, batch_ref)：
// 同一批次重传直接返回首次受理结果，不重复落事实、不重复追加台账事件。
// 重传内容与首次不同时仍保持幂等（以首次为准），仅在响应中提示摘要变化。
func (s *Service) SubmitBatch(in BatchRequest) (BatchResponse, error) {
	if in.BatchRef == "" || in.SchoolRef == "" {
		return BatchResponse{}, badRequest("invalid_batch", "batch_ref 与 school_ref 为必填")
	}
	if len(in.Works) == 0 {
		return BatchResponse{}, badRequest("invalid_batch", "清单至少包含一件作品")
	}
	source := "school:" + in.SchoolRef
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = s.now()
	}

	digest, err := domain.PayloadDigest(in.Works)
	if err != nil {
		return BatchResponse{}, badRequest("invalid_payload", "清单摘要计算失败: %v", err)
	}

	if existing, ok := s.store.GetBatch(source, in.BatchRef); ok {
		return BatchResponse{
			Source:        source,
			BatchRef:      in.BatchRef,
			Digest:        existing.Digest,
			Replayed:      true,
			DigestChanged: existing.Digest != digest,
			AcceptedWorks: existing.Works,
			EventIDs:      existing.EventIDs,
			ReceivedAt:    existing.ReceivedAt,
		}, nil
	}

	// 校验全部作品后再落库，避免半份清单进入事实库。
	for _, w := range in.Works {
		if err := validateWorkInput(w); err != nil {
			return BatchResponse{}, err
		}
	}

	now := s.now()
	seq := s.store.NextSourceSequence(source)
	eventIDs := make([]string, 0, len(in.Works)+1)
	workRefs := make([]string, 0, len(in.Works))

	batchEventID := "EVT-" + source + "-" + in.BatchRef
	payloadRaw := mustJSON(in.Works)
	ev := domain.Event{
		SchemaVersion:  "1",
		EventID:        batchEventID,
		Source:         source,
		SourceSequence: seq,
		OccurredAt:     occurred, // 保留院校原始提交时刻，不用到达时间覆盖
		ReceivedAt:     now,
		SubjectRef:     "batch:" + in.BatchRef,
		Type:           "batch_submitted",
		PayloadDigest:  digest,
		Payload:        payloadRaw,
	}
	s.store.AppendEvent(ev)
	eventIDs = append(eventIDs, batchEventID)

	for _, wi := range in.Works {
		work := domain.Work{
			Ref: wi.Ref, Title: wi.Title, ArtistRef: wi.ArtistRef,
			SchoolRef: in.SchoolRef, Medium: wi.Medium,
		}
		s.store.PutWork(work)
		workRefs = append(workRefs, wi.Ref)

		for _, ci := range wi.Components {
			s.store.PutComponent(domain.Component{
				Ref: ci.Ref, WorkRef: wi.Ref, Form: ci.Form, Medium: ci.Medium,
				TransportClass: ci.TransportClass, PlaybackProfile: ci.PlaybackProfile,
				Sha256: ci.Sha256, Notes: ci.Notes,
			})
		}
		for _, ri := range wi.Rights {
			s.store.PutRight(domain.Right{
				Ref: ri.Ref, WorkRef: wi.Ref, HolderRef: ri.HolderRef,
				Countries: normalizeCountries(ri.Countries), Media: ri.Media,
				NotBefore: ri.NotBefore, NotAfter: ri.NotAfter,
				StatementSha: ri.StatementSha,
			})
		}
		if wi.Loan != nil {
			s.store.PutLoan(domain.Loan{
				Ref: wi.Loan.Ref, WorkRef: wi.Ref, LenderRef: wi.Loan.LenderRef,
				Start: wi.Loan.Start, End: wi.Loan.End, Conditions: wi.Loan.Conditions,
			})
		}
	}

	rec := store.BatchRecord{
		Key: store.BatchKey(source, in.BatchRef), Source: source, BatchRef: in.BatchRef,
		Digest: digest, EventIDs: eventIDs, Works: workRefs, ReceivedAt: now,
	}
	s.store.SaveBatch(rec)

	return BatchResponse{
		Source: source, BatchRef: in.BatchRef, Digest: digest,
		AcceptedWorks: workRefs, EventIDs: eventIDs, ReceivedAt: now,
	}, nil
}

func validateWorkInput(w WorkInput) error {
	if w.Ref == "" || w.Title == "" || w.ArtistRef == "" || w.Medium == "" {
		return badRequest("invalid_work", "作品 %s 的 ref/title/artist_ref/medium 为必填", w.Ref)
	}
	switch w.Medium {
	case domain.MediumPhoto, domain.MediumVideo, domain.MediumBook, domain.MediumExpFilm:
	default:
		return badRequest("invalid_medium", "作品 %s 的媒介 %q 不在受控集合", w.Ref, w.Medium)
	}
	compSeen := map[string]bool{}
	for _, c := range w.Components {
		if c.Ref == "" || c.Form == "" {
			return badRequest("invalid_component", "作品 %s 存在缺少 ref/form 的组成件", w.Ref)
		}
		if compSeen[c.Ref] {
			return badRequest("duplicate_component", "作品 %s 的组成件 %s 重复", w.Ref, c.Ref)
		}
		compSeen[c.Ref] = true
		if c.Form != domain.FormPhysical && c.Form != domain.FormDigital {
			return badRequest("invalid_form", "组成件 %s 的 form 必须是 physical/digital", c.Ref)
		}
	}
	for _, r := range w.Rights {
		if r.Ref == "" || r.HolderRef == "" || len(r.Countries) == 0 || len(r.Media) == 0 {
			return badRequest("invalid_right", "作品 %s 的权利声明字段不完整", w.Ref)
		}
	}
	if w.Loan != nil && !w.Loan.End.After(w.Loan.Start) {
		return badRequest("invalid_loan", "作品 %s 的借展窗口结束必须晚于开始", w.Ref)
	}
	return nil
}

func normalizeCountries(list []string) []string {
	if len(list) == 0 {
		return []string{domain.CountryAll}
	}
	out := make([]string, 0, len(list))
	for _, c := range list {
		if c == "*" {
			c = domain.CountryAll
		}
		out = append(out, c)
	}
	return out
}
