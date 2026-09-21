// Package api 把协调器暴露为 JSON HTTP 接口。
//
// 写接口接收带 event_id（以及可选 source_id/source_sequence）的命令；
// 校验失败返回 400，违反占用/权利等不变量返回 409，响应体为统一的
// 错误信封。时间字段一律使用带偏移量的 RFC 3339（ISO 8601）字符串。
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"example.com/batch-092001-q007/internal/service"
)

// Handler 持有协调器并构造路由。
type Handler struct {
	coordinator *service.Coordinator
}

// NewHandler 创建装配好全部路由的 http.Handler。
func NewHandler(coord *service.Coordinator) http.Handler {
	h := &Handler{coordinator: coord}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/artworks", h.registerArtwork)
	mux.HandleFunc("POST /v1/pieces", h.registerPiece)
	mux.HandleFunc("POST /v1/rights", h.recordRights)
	mux.HandleFunc("POST /v1/loans", h.recordLoan)
	mux.HandleFunc("POST /v1/venues", h.registerVenue)
	mux.HandleFunc("POST /v1/devices", h.registerDevice)
	mux.HandleFunc("POST /v1/dependencies", h.addDependency)

	mux.HandleFunc("POST /v1/slots", h.planSlot)
	mux.HandleFunc("POST /v1/slots/{ref}/reschedule", h.rescheduleSlot)
	mux.HandleFunc("POST /v1/slots/{ref}/confirm", h.confirmSlot)
	mux.HandleFunc("POST /v1/slots/{ref}/cancel", h.cancelSlot)

	mux.HandleFunc("POST /v1/movements", h.appendMovement)
	mux.HandleFunc("POST /v1/incidents/damage", h.reportDamage)
	mux.HandleFunc("POST /v1/incidents/rights-withdrawal", h.withdrawRights)
	mux.HandleFunc("POST /v1/incidents/venue-closure", h.closeVenue)
	mux.HandleFunc("POST /v1/incidents/device-breakage", h.breakDevice)
	mux.HandleFunc("POST /v1/dispositions/{ref}/resolve", h.resolveDisposition)

	mux.HandleFunc("GET /v1/artworks/{ref}", h.getArtwork)
	mux.HandleFunc("GET /v1/artworks/{ref}/track", h.artworkTrack)
	mux.HandleFunc("GET /v1/venues/{ref}/brief", h.venueBrief)
	return mux
}

func (h *Handler) registerArtwork(w http.ResponseWriter, r *http.Request) {
	var req service.ArtworkRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RegisterArtwork(req)
	})
}

func (h *Handler) registerPiece(w http.ResponseWriter, r *http.Request) {
	var req service.PieceRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RegisterPiece(req)
	})
}

func (h *Handler) recordRights(w http.ResponseWriter, r *http.Request) {
	var req service.RightsRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RecordRights(req)
	})
}

func (h *Handler) recordLoan(w http.ResponseWriter, r *http.Request) {
	var req service.LoanRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RecordLoan(req)
	})
}

func (h *Handler) registerVenue(w http.ResponseWriter, r *http.Request) {
	var req service.VenueRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RegisterVenue(req)
	})
}

func (h *Handler) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req service.DeviceRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RegisterDevice(req)
	})
}

func (h *Handler) addDependency(w http.ResponseWriter, r *http.Request) {
	var req service.DependencyRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.AddDependency(req)
	})
}

func (h *Handler) planSlot(w http.ResponseWriter, r *http.Request) {
	var req service.SlotRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.PlanSlot(req)
	})
}

func (h *Handler) rescheduleSlot(w http.ResponseWriter, r *http.Request) {
	var req service.SlotRequest
	if !decode(w, r, &req) {
		return
	}
	req.Ref = r.PathValue("ref")
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.RescheduleSlot(req)
	})
}

func (h *Handler) confirmSlot(w http.ResponseWriter, r *http.Request) {
	var req service.ConfirmRequest
	if !decode(w, r, &req) {
		return
	}
	req.Ref = r.PathValue("ref")
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.ConfirmSlot(req)
	})
}

func (h *Handler) cancelSlot(w http.ResponseWriter, r *http.Request) {
	var req service.ConfirmRequest
	if !decode(w, r, &req) {
		return
	}
	req.Ref = r.PathValue("ref")
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.CancelSlot(req)
	})
}

func (h *Handler) appendMovement(w http.ResponseWriter, r *http.Request) {
	var req service.MovementRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.AppendMovement(req)
	})
}

func (h *Handler) reportDamage(w http.ResponseWriter, r *http.Request) {
	var req service.DamageRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.ReportDamage(req)
	})
}

func (h *Handler) withdrawRights(w http.ResponseWriter, r *http.Request) {
	var req service.WithdrawalRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.WithdrawRights(req)
	})
}

func (h *Handler) closeVenue(w http.ResponseWriter, r *http.Request) {
	var req service.VenueCloseRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.CloseVenue(req)
	})
}

func (h *Handler) breakDevice(w http.ResponseWriter, r *http.Request) {
	var req service.DeviceBreakRequest
	if !decode(w, r, &req) {
		return
	}
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.BreakDevice(req)
	})
}

func (h *Handler) resolveDisposition(w http.ResponseWriter, r *http.Request) {
	var req service.ResolutionRequest
	if !decode(w, r, &req) {
		return
	}
	req.Ref = r.PathValue("ref")
	h.submit(w, r, func() (*service.SubmitResult, error) {
		return h.coordinator.ResolveDisposition(req)
	})
}

func (h *Handler) getArtwork(w http.ResponseWriter, r *http.Request) {
	view := h.coordinator.GetArtwork(r.PathValue("ref"))
	if view == nil {
		writeError(w, http.StatusNotFound, "not_found", "作品不存在", nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) artworkTrack(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	track := h.coordinator.ArtworkTrack(ref)
	if track == nil {
		writeError(w, http.StatusNotFound, "not_found", "作品不存在", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artwork_ref": ref, "track": track})
}

func (h *Handler) venueBrief(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "start 须为带偏移量的 RFC 3339 时间", nil)
		return
	}
	end, err := time.Parse(time.RFC3339, q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "end 须为带偏移量的 RFC 3339 时间", nil)
		return
	}
	brief, err := h.coordinator.VenueBrief(r.PathValue("ref"), start, end)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, brief)
}

// submit 执行一次写操作并把错误映射为状态码。
func (h *Handler) submit(w http.ResponseWriter, r *http.Request,
	fn func() (*service.SubmitResult, error)) {
	result, err := fn()
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error(), nil)
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	var conflict *service.ConflictError
	if errors.As(err, &conflict) {
		writeError(w, http.StatusConflict, "conflict", conflict.Error(), conflict.Reasons)
		return
	}
	var validation *service.ValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusBadRequest, "bad_request", validation.Error(), nil)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
}

type errorBody struct {
	Error errorEnvelope `json:"error"`
}

type errorEnvelope struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Reasons []string `json:"reasons,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string, reasons []string) {
	writeJSON(w, status, errorBody{Error: errorEnvelope{Code: code, Message: msg, Reasons: reasons}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
