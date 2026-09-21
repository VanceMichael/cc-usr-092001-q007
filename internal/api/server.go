// Package api 把协调器应用服务暴露为 HTTP JSON 接口。
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"example.com/batch-092001-q007/internal/service"
)

// Server 持有 HTTP 路由与应用服务。
type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

// New 构造路由完整的 API 服务。
func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	m.HandleFunc("POST /v1/venues", s.registerVenue)
	m.HandleFunc("GET /v1/venues", s.listVenues)
	m.HandleFunc("POST /v1/equipment", s.registerEquipment)
	m.HandleFunc("GET /v1/equipment", s.listEquipment)

	m.HandleFunc("POST /v1/batches", s.submitBatch)
	m.HandleFunc("POST /v1/events", s.ingestEvent)
	m.HandleFunc("GET /v1/events", s.listEvents)

	m.HandleFunc("POST /v1/slots", s.proposeSlot)
	m.HandleFunc("GET /v1/slots/{ref}", s.getSlot)
	m.HandleFunc("POST /v1/slots/{ref}/confirm", s.confirmSlot)
	m.HandleFunc("POST /v1/slots/{ref}/reschedule", s.rescheduleSlot)
	m.HandleFunc("POST /v1/slots/{ref}/cancel", s.cancelSlot)

	m.HandleFunc("GET /v1/works/{ref}/timeline", s.workTimeline)
	m.HandleFunc("GET /v1/venues/{ref}/window", s.venueWindow)

	m.HandleFunc("GET /v1/plans/{ref}", s.getPlan)
	m.HandleFunc("POST /v1/plans/{ref}/execute", s.executePlan)
	m.HandleFunc("GET /v1/change-orders", s.listChangeOrders)
}

// Handler 暴露给主程序。
func (s *Server) Handler() http.Handler { return s.mux }

// ---------- 目录 ----------

func (s *Server) registerVenue(w http.ResponseWriter, r *http.Request) {
	var in service.VenueInput
	if !decode(w, r, &in) {
		return
	}
	v, err := s.svc.RegisterVenue(in)
	respond(w, v, err)
}

func (s *Server) listVenues(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"venues": s.svc.Store().ListVenues()})
}

func (s *Server) registerEquipment(w http.ResponseWriter, r *http.Request) {
	var in service.EquipmentInput
	if !decode(w, r, &in) {
		return
	}
	e, err := s.svc.RegisterEquipment(in)
	respond(w, e, err)
}

func (s *Server) listEquipment(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"equipment": s.svc.Store().ListEquipment()})
}

// ---------- 批次与事件 ----------

func (s *Server) submitBatch(w http.ResponseWriter, r *http.Request) {
	var in service.BatchRequest
	if !decode(w, r, &in) {
		return
	}
	res, err := s.svc.SubmitBatch(in)
	respond(w, res, err)
}

func (s *Server) ingestEvent(w http.ResponseWriter, r *http.Request) {
	var in service.EventRequest
	if !decode(w, r, &in) {
		return
	}
	res, err := s.svc.IngestEvent(in)
	respond(w, res, err)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject_ref")
	if subject != "" {
		writeJSON(w, http.StatusOK, map[string]any{"events": s.svc.Store().EventsForSubject(subject)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": s.svc.Store().AllEvents()})
}

// ---------- 排期 ----------

func (s *Server) proposeSlot(w http.ResponseWriter, r *http.Request) {
	var in service.SlotRequest
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.ProposeSlot(in)
	respond(w, view, err)
}

func (s *Server) getSlot(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	sl, ok := s.svc.Store().GetSlot(ref)
	if !ok {
		respond(w, nil, service.NotFoundf("排期 %s 不存在", ref))
		return
	}
	writeJSON(w, http.StatusOK, sl)
}

func (s *Server) confirmSlot(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.ConfirmSlot(r.PathValue("ref"))
	respond(w, view, err)
}

func (s *Server) rescheduleSlot(w http.ResponseWriter, r *http.Request) {
	var in service.RescheduleRequest
	if !decode(w, r, &in) {
		return
	}
	view, err := s.svc.RescheduleSlot(r.PathValue("ref"), in)
	respond(w, view, err)
}

func (s *Server) cancelSlot(w http.ResponseWriter, r *http.Request) {
	var in service.CancelRequest
	if !decode(w, r, &in) {
		return
	}
	change, err := s.svc.CancelSlot(r.PathValue("ref"), in)
	respond(w, change, err)
}

// ---------- 查询与处置 ----------

func (s *Server) workTimeline(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.WorkTimeline(r.PathValue("ref"))
	respond(w, view, err)
}

func (s *Server) venueWindow(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		writeError(w, &service.APIError{Status: 400, Code: "bad_time", Message: "start 需要 RFC3339 带偏移量时间"})
		return
	}
	end, err := time.Parse(time.RFC3339, q.Get("end"))
	if err != nil {
		writeError(w, &service.APIError{Status: 400, Code: "bad_time", Message: "end 需要 RFC3339 带偏移量时间"})
		return
	}
	view, svcErr := s.svc.VenueWindow(r.PathValue("ref"), start, end)
	respond(w, view, svcErr)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	p, ok := s.svc.Store().GetPlan(r.PathValue("ref"))
	if !ok {
		respond(w, nil, service.NotFoundf("处置方案 %s 不存在", r.PathValue("ref")))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	var in service.ExecutePlanRequest
	if !decode(w, r, &in) {
		return
	}
	p, err := s.svc.ExecutePlan(r.PathValue("ref"), in)
	respond(w, p, err)
}

func (s *Server) listChangeOrders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"change_orders": s.svc.Store().ListChangeOrders()})
}

// ---------- 响应工具 ----------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, &service.APIError{Status: 400, Code: "invalid_json", Message: "请求体解析失败: " + err.Error()})
		return false
	}
	return true
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		if ae, ok := err.(*service.APIError); ok {
			writeError(w, ae)
			return
		}
		writeError(w, &service.APIError{Status: 500, Code: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeError(w http.ResponseWriter, ae *service.APIError) {
	writeJSON(w, ae.Status, map[string]any{
		"error": map[string]string{"code": ae.Code, "message": ae.Message},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
