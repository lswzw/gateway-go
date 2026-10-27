package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// RequestsTotal 统计网关处理的请求总数 (REQ-P1-04)
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of HTTP requests processed by the gateway",
		},
		[]string{"route", "method", "code"},
	)

	// UpstreamRTSeconds 统计上游服务响应耗时直方图 (REQ-P1-04)
	UpstreamRTSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_upstream_rt_seconds",
			Help:    "Latency of upstream requests forwarded by the gateway in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"route"},
	)

	// RateLimitRejectedTotal 统计被限流插件拒绝的请求总数 (REQ-P1-04)
	RateLimitRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_ratelimit_rejected_total",
			Help: "Total number of requests rejected by rate limiting",
		},
		[]string{"route"},
	)

	// CircuitOpenTotal 统计熔断器触发/拒绝的请求总数 (REQ-P1-04)
	CircuitOpenTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_circuit_open_total",
			Help: "Total number of requests rejected due to circuit breaker open state",
		},
		[]string{"route"},
	)

	// ReloadTotal 统计配置重载次数与结果 (REQ-P1-04)
	ReloadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_reload_total",
			Help: "Total number of configuration reloads by result (e.g. success, failure)",
		},
		[]string{"result"},
	)

	// ConfigVersion 标识当前生效的配置版本 (REQ-P1-04)
	ConfigVersion = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_config_version",
			Help: "Current active configuration version",
		},
		[]string{"version"},
	)

	// AuthCacheHitsTotal 认证结果本地缓存命中次数（每副本独立计数）
	AuthCacheHitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_auth_cache_hits_total",
			Help: "Total number of interface_auth cache hits (local per replica)",
		},
		[]string{"route"},
	)

	// AuthCacheMissesTotal 认证结果本地缓存未命中次数
	AuthCacheMissesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_auth_cache_misses_total",
			Help: "Total number of interface_auth cache misses (local per replica)",
		},
		[]string{"route"},
	)

	// AuthUpstreamCallsTotal 对认证服务的实际外呼次数（result: ok/deny/error/open）
	AuthUpstreamCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_auth_upstream_calls_total",
			Help: "Total number of calls to the external auth service by result",
		},
		[]string{"route", "result"},
	)
)

func init() {
	Register(prometheus.DefaultRegisterer)
}

// Register 将所有指标注册到指定的 prometheus.Registerer，容忍已注册错误
func Register(r prometheus.Registerer) {
	if r == nil {
		return
	}
	collectors := []prometheus.Collector{
		RequestsTotal,
		UpstreamRTSeconds,
		RateLimitRejectedTotal,
		CircuitOpenTotal,
		ReloadTotal,
		ConfigVersion,
		AuthCacheHitsTotal,
		AuthCacheMissesTotal,
		AuthUpstreamCallsTotal,
	}
	for _, c := range collectors {
		if err := r.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
	}
}

// RecordRequest 记录请求计数
func RecordRequest(route, method, code string) {
	if route == "" {
		route = "unknown"
	}
	if method == "" {
		method = "-"
	}
	if code == "" {
		code = "unknown"
	}
	RequestsTotal.WithLabelValues(route, method, code).Inc()
}

// RecordRequestStatus 便捷方法：使用 int 状态码记录请求计数
func RecordRequestStatus(route, method string, code int) {
	RecordRequest(route, method, strconv.Itoa(code))
}

// RecordUpstreamLatency 记录上游服务响应耗时
func RecordUpstreamLatency(route string, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	UpstreamRTSeconds.WithLabelValues(route).Observe(duration.Seconds())
}

// RecordRateLimitRejected 记录限流拒绝次数
func RecordRateLimitRejected(route string) {
	if route == "" {
		route = "unknown"
	}
	RateLimitRejectedTotal.WithLabelValues(route).Inc()
}

// RecordCircuitOpen 记录熔断开启/拒绝次数
func RecordCircuitOpen(route string) {
	if route == "" {
		route = "unknown"
	}
	CircuitOpenTotal.WithLabelValues(route).Inc()
}

// RecordReload 记录配置重载结果（如 "success", "failure"）
func RecordReload(result string) {
	if result == "" {
		result = "unknown"
	}
	ReloadTotal.WithLabelValues(result).Inc()
}

// SetConfigVersion 设置当前配置版本
func SetConfigVersion(version string) {
	if version == "" {
		version = "unknown"
	}
	ConfigVersion.Reset()
	ConfigVersion.WithLabelValues(version).Set(1)
}

// Reset 重置所有指标，用于单元测试环境清理
func Reset() {
	RequestsTotal.Reset()
	UpstreamRTSeconds.Reset()
	RateLimitRejectedTotal.Reset()
	CircuitOpenTotal.Reset()
	ReloadTotal.Reset()
	ConfigVersion.Reset()
	AuthCacheHitsTotal.Reset()
	AuthCacheMissesTotal.Reset()
	AuthUpstreamCallsTotal.Reset()
}

// RecordAuthCacheHit 记录认证缓存命中
func RecordAuthCacheHit(route string) {
	if route == "" {
		route = "unknown"
	}
	AuthCacheHitsTotal.WithLabelValues(route).Inc()
}

// RecordAuthCacheMiss 记录认证缓存未命中
func RecordAuthCacheMiss(route string) {
	if route == "" {
		route = "unknown"
	}
	AuthCacheMissesTotal.WithLabelValues(route).Inc()
}

// RecordAuthUpstreamCall 记录对认证服务的实际外呼（result: ok/deny/error/open）
func RecordAuthUpstreamCall(route, result string) {
	if route == "" {
		route = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	AuthUpstreamCallsTotal.WithLabelValues(route, result).Inc()
}
