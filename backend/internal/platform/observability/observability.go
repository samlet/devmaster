// Package observability 提供指标、健康检查与 gRPC 指标拦截器。
package observability

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Metrics 是本服务暴露给 Prometheus 的指标集合。
type Metrics struct {
	registry *prometheus.Registry

	GRPCDuration    *prometheus.HistogramVec
	OutboxSent      prometheus.Counter
	OutboxFailed    prometheus.Counter
	OutboxPending   prometheus.Gauge
	ProductsCreated prometheus.Counter
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		GRPCDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "devmaster_grpc_request_duration_seconds",
			Help:    "gRPC 请求耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "code"}),
		OutboxSent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devmaster_outbox_sent_total",
			Help: "已投递的 outbox 事件数",
		}),
		OutboxFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devmaster_outbox_failed_total",
			Help: "投递失败的 outbox 事件数",
		}),
		OutboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "devmaster_outbox_pending",
			Help: "当前待投递的 outbox 事件数",
		}),
		ProductsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devmaster_products_created_total",
			Help: "创建的产品数",
		}),
	}
	registry.MustRegister(metrics.GRPCDuration, metrics.OutboxSent, metrics.OutboxFailed, metrics.OutboxPending, metrics.ProductsCreated)
	return metrics
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// UnaryServerInterceptor 记录 gRPC 方法耗时与状态码。
func (m *Metrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		response, err := handler(ctx, req)
		code := status.Code(err).String()
		m.GRPCDuration.WithLabelValues(info.FullMethod, code).Observe(time.Since(started).Seconds())
		return response, err
	}
}

// Component 是健康检查的一个依赖。
type Component struct {
	Name string
	// Check 返回 nil 表示健康。TCP 探活也算一种 Check。
	Check func(ctx context.Context) error
}

// Health 汇总依赖状态：任一失败即 503，并带上失败的组件名。
//
// 503 而不是 200 + body 里写 DOWN：负载均衡与探针只看状态码，
// 用 body 表达失败等于让编排系统永远以为服务是好的。
type Health struct {
	Timeout    time.Duration
	Components []Component
}

func (h Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()

	type result struct {
		Status     string            `json:"status"`
		Components map[string]string `json:"components"`
	}
	out := result{Status: "ok", Components: map[string]string{}}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed bool
	)
	for _, component := range h.Components {
		component := component
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := component.Check(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Components[component.Name] = "down: " + err.Error()
				failed = true
				return
			}
			out.Components[component.Name] = "ok"
		}()
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	if failed {
		out.Status = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(out)
}

// TCPCheck 是最轻的依赖探活：只证明端口可连，不证明对端业务正常。
// 用它是因为 M0 不引入 NATS/Temporal 客户端到 server 进程。
func TCPCheck(address string) func(context.Context) error {
	return func(ctx context.Context) error {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
