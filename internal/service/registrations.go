package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"example.com/batch-092001-q007/internal/domain"
)

// RegisterArtwork 登记一件作品。重复登记同一编号视为对该编号的更新，
// 但不允许把作品挂靠到另一院校。
func (c *Coordinator) RegisterArtwork(req ArtworkRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" {
		return nil, validationf("作品编号不能为空")
	}
	if existing := c.state.artworks[req.Ref]; existing != nil &&
		req.SchoolRef != "" && existing.SchoolRef != "" && existing.SchoolRef != req.SchoolRef {
		return nil, &ConflictError{Reasons: []string{
			fmt.Sprintf("作品 %s 已属于院校 %s，不得改挂", req.Ref, existing.SchoolRef)}}
	}
	now := c.clk.Now()
	a := &domain.Artwork{
		Ref:       req.Ref,
		SchoolRef: req.SchoolRef,
		Title:     req.Title,
		Medias:    req.Medias,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if existing := c.state.artworks[req.Ref]; existing != nil {
		a.CreatedAt = existing.CreatedAt
		a.Pieces = existing.Pieces
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtArtworkRegistered, a, "", "")
}

// RegisterPiece 登记组成件。院校批量重传时以 manifest_ref+ref 作为
// 幂等键，同一清单重发不会产生重复组成件。
func (c *Coordinator) RegisterPiece(req PieceRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.ArtworkRef == "" {
		return nil, validationf("组成件编号与作品编号不能为空")
	}
	if c.state.artworks[req.ArtworkRef] == nil {
		return nil, validationf("作品 %s 尚未登记", req.ArtworkRef)
	}
	if req.Digest != "" && !strings.HasPrefix(req.Digest, "sha256:") {
		return nil, validationf("摘要必须形如 sha256:<hex>")
	}
	p := &domain.Piece{
		Ref:                 req.Ref,
		ArtworkRef:          req.ArtworkRef,
		Media:               req.Media,
		MaterialRef:         req.MaterialRef,
		Digest:              req.Digest,
		Transport:           req.Transport,
		RequiredDeviceKinds: req.RequiredDeviceKinds,
	}
	idem := req.IdempotencyKey
	if idem == "" && req.ManifestRef != "" {
		idem = "manifest:" + req.ManifestRef + ":piece:" + req.Ref
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtPieceRegistered, p, idem, req.ManifestRef)
}

// RecordRights 记录权利人授权声明。
func (c *Coordinator) RecordRights(req RightsRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.ArtworkRef == "" {
		return nil, validationf("声明编号与作品编号不能为空")
	}
	from, err := domain.ParseLocalDate(req.ValidFrom)
	if err != nil {
		return nil, err
	}
	to, err := domain.ParseLocalDate(req.ValidTo)
	if err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, validationf("授权终止日期 %s 早于起始日期 %s", to, from)
	}
	g := &domain.RightsGrant{
		Ref:              req.Ref,
		ArtworkRef:       req.ArtworkRef,
		AllowedCountries: req.AllowedCountries,
		AllowedMedia:     req.AllowedMedia,
		ValidFrom:        from,
		ValidTo:          to,
		RecordedAt:       c.clk.Now(),
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtRightsRecorded, g, "", "")
}

// RecordLoan 记录借展协议。
func (c *Coordinator) RecordLoan(req LoanRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.ArtworkRef == "" {
		return nil, validationf("协议编号与作品编号不能为空")
	}
	if req.MustReturnBy.IsZero() {
		return nil, validationf("撤展截止时刻不能为空")
	}
	l := &domain.LoanAgreement{
		Ref:          req.Ref,
		ArtworkRef:   req.ArtworkRef,
		MustReturnBy: req.MustReturnBy.UTC(),
		Notes:        req.Notes,
		RecordedAt:   c.clk.Now(),
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtLoanRecorded, l, "", "")
}

// RegisterVenue 登记场地，tz 必须是可解析的 IANA 时区，
// 跨时区提交才能按展馆当地时间落位。
func (c *Coordinator) RegisterVenue(req VenueRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" {
		return nil, validationf("场地编号不能为空")
	}
	if req.CountryCode == "" {
		return nil, validationf("场地 %s 缺少国家代码", req.Ref)
	}
	if req.TZ == "" {
		return nil, validationf("场地 %s 缺少时区", req.Ref)
	}
	if _, err := time.LoadLocation(req.TZ); err != nil {
		return nil, validationf("场地 %s 时区 %q 无法解析: %v", req.Ref, req.TZ, err)
	}
	v := &domain.Venue{
		Ref:         req.Ref,
		Name:        req.Name,
		CountryCode: strings.ToUpper(req.CountryCode),
		TZ:          req.TZ,
		Medias:      req.Medias,
		UpdatedAt:   c.clk.Now(),
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtVenueRegistered, v, "", "")
}

// RegisterDevice 登记播放/展示设备。
func (c *Coordinator) RegisterDevice(req DeviceRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.VenueRef == "" || req.Kind == "" {
		return nil, validationf("设备编号、场地编号与设备种类不能为空")
	}
	if c.state.venues[req.VenueRef] == nil {
		return nil, validationf("场地 %s 尚未登记", req.VenueRef)
	}
	d := &domain.Device{
		Ref:       req.Ref,
		VenueRef:  req.VenueRef,
		Kind:      req.Kind,
		UpdatedAt: c.clk.Now(),
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtDeviceRegistered, d, "", "")
}

// AddDependency 登记安装前置依赖：ToSlot 的安装要求 FromSlot 先达到
// delivered/installed 阶段。
func (c *Coordinator) AddDependency(req DependencyRequest) (*SubmitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.alreadySubmitted(req.EventID); r != nil {
		return r, nil
	}
	if req.Ref == "" || req.FromSlotRef == "" || req.ToSlotRef == "" {
		return nil, validationf("依赖编号、前置场次与目标场次不能为空")
	}
	if req.Requires != "delivered" && req.Requires != "installed" {
		return nil, validationf("前置阶段必须是 delivered 或 installed")
	}
	if req.FromSlotRef == req.ToSlotRef {
		return nil, validationf("场次不能依赖自身")
	}
	d := &domain.InstallDependency{
		Ref:         req.Ref,
		ArtworkRef:  req.ArtworkRef,
		FromSlotRef: req.FromSlotRef,
		ToSlotRef:   req.ToSlotRef,
		Requires:    req.Requires,
	}
	return c.emit(req.EventID, req.SourceID, req.SourceSequence, req.Ref,
		domain.EvtInstallDependencyAdded, d, "", "")
}

// emit 序列化载荷、计算 sha256 摘要并提交事件。
func (c *Coordinator) emit(eventID, sourceID string, seq int64, subject string,
	typ domain.EventType, payload any, idem, manifest string) (*SubmitResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化载荷: %w", err)
	}
	sum := sha256.Sum256(raw)
	evt := domain.Event{
		SchemaVersion:  SchemaVersion,
		EventID:        eventID,
		SourceID:       sourceID,
		SourceSequence: seq,
		OccurredAt:     c.clk.Now(),
		SubjectRef:     subject,
		Type:           typ,
		Payload:        raw,
		PayloadDigest:  "sha256:" + hex.EncodeToString(sum[:]),
		IdempotencyKey: idem,
		ManifestRef:    manifest,
	}
	return c.submit(evt)
}
