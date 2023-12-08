package gubernator

import (
	"context"
	"fmt"
	"net/http"

	"github.com/duh-rpc/duh-go"
	v1 "github.com/duh-rpc/duh-go/proto/v1"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/propagation"
)

const (
	RPCPeerForward    = "/v1/peer.forward"
	RPCPeerUpdate     = "/v1/peer.update"
	RPCRateLimitCheck = "/v1/rate-limit.check"
	RPCHealthCheck    = "/v1/health.check"
)

type Handler struct {
	prop     propagation.TraceContext
	duration *prometheus.SummaryVec
	metrics  http.Handler
	service  *Service
}

func NewHandler(s *Service, metrics http.Handler) *Handler {
	return &Handler{
		duration: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Name: "gubernator_http_handler_duration",
			Help: "The timings of http requests handled by the service",
			Objectives: map[float64]float64{
				0.5:  0.05,
				0.99: 0.001,
			},
		}, []string{"path"}),
		metrics: metrics,
		service: s,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer prometheus.NewTimer(h.duration.WithLabelValues(r.URL.Path)).ObserveDuration()
	ctx := h.prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

	switch r.URL.Path {
	case RPCPeerForward:
		h.PeerForward(ctx, w, r)
		return
	case RPCPeerUpdate:
		h.PeerUpdate(ctx, w, r)
		return
	case RPCRateLimitCheck:
		h.CheckRateLimit(ctx, w, r)
		return
	case RPCHealthCheck:
		h.HealthCheck(w, r)
		return
	case "/metrics":
		h.metrics.ServeHTTP(w, r)
		return
	case "/healthz":
		h.HealthZ(w, r)
		return
	}
	duh.ReplyWithCode(w, r, duh.CodeNotImplemented, nil, "no such method; "+r.URL.Path)
}

func (h *Handler) PeerForward(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		duh.ReplyWithCode(w, r, duh.CodeBadRequest, nil,
			fmt.Sprintf("http method '%s' not allowed; only POST", r.Method))
		return
	}

	var req ForwardRequest
	if err := duh.ReadRequest(r, &req); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	var resp ForwardResponse
	if err := h.service.Forward(ctx, &req, &resp); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	duh.Reply(w, r, duh.CodeOK, &resp)
}

func (h *Handler) PeerUpdate(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		duh.ReplyWithCode(w, r, duh.CodeBadRequest, nil,
			fmt.Sprintf("http method '%s' not allowed; only POST", r.Method))
		return
	}

	var req UpdateRequest
	if err := duh.ReadRequest(r, &req); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	var resp v1.Reply
	if err := h.service.Update(ctx, &req, &resp); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	duh.Reply(w, r, duh.CodeOK, &resp)
}

func (h *Handler) CheckRateLimit(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		duh.ReplyWithCode(w, r, duh.CodeBadRequest, nil,
			fmt.Sprintf("http method '%s' not allowed; only POST", r.Method))
		return
	}

	var req CheckRateLimitsRequest
	if err := duh.ReadRequest(r, &req); err != nil {
		duh.ReplyError(w, r, err)
		return
	}

	var resp CheckRateLimitsResponse
	if err := h.service.CheckRateLimits(ctx, &req, &resp); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	duh.Reply(w, r, duh.CodeOK, &resp)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		duh.ReplyWithCode(w, r, duh.CodeBadRequest, nil,
			fmt.Sprintf("http method '%s' not allowed; only POST", r.Method))
		return
	}

	var req HealthCheckRequest
	if err := duh.ReadRequest(r, &req); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	var resp HealthCheckResponse
	if err := h.service.HealthCheck(r.Context(), &req, &resp); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	duh.Reply(w, r, duh.CodeOK, &resp)
}

func (h *Handler) HealthZ(w http.ResponseWriter, r *http.Request) {
	var resp HealthCheckResponse
	if err := h.service.HealthCheck(r.Context(), nil, &resp); err != nil {
		duh.ReplyError(w, r, err)
		return
	}
	duh.Reply(w, r, duh.CodeOK, &resp)
}

// Describe fetches prometheus metrics to be registered
func (h *Handler) Describe(ch chan<- *prometheus.Desc) {
	h.duration.Describe(ch)
}

// Collect fetches metrics from the server for use by prometheus
func (h *Handler) Collect(ch chan<- prometheus.Metric) {
	h.duration.Collect(ch)
}
