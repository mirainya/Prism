package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// --- Prometheus 实现 ---

type promCounter struct {
	vec *prometheus.CounterVec
}

func (c *promCounter) Inc(labels ...string) {
	c.vec.WithLabelValues(labels...).Inc()
}

func (c *promCounter) Add(value float64, labels ...string) {
	c.vec.WithLabelValues(labels...).Add(value)
}

type promHistogram struct {
	vec *prometheus.HistogramVec
}

func (h *promHistogram) Observe(value float64, labels ...string) {
	h.vec.WithLabelValues(labels...).Observe(value)
}

type promProvider struct {
	registry *prometheus.Registry
}

// NewPrometheusProvider 创建 Prometheus 指标提供者
func NewPrometheusProvider(registry *prometheus.Registry) Provider {
	return &promProvider{registry: registry}
}

func (p *promProvider) NewCounter(name, help string, labelNames ...string) Counter {
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "prism",
		Name:      name,
		Help:      help,
	}, labelNames)
	p.registry.MustRegister(vec)
	return &promCounter{vec: vec}
}

func (p *promProvider) NewHistogram(name, help string, labelNames ...string) Histogram {
	vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "prism",
		Name:      name,
		Help:      help,
		Buckets:   prometheus.DefBuckets,
	}, labelNames)
	p.registry.MustRegister(vec)
	return &promHistogram{vec: vec}
}

// Registry 获取 Prometheus Registry（用于暴露 /metrics 端点）
func (p *promProvider) Registry() *prometheus.Registry {
	return p.registry
}

// InitPrometheus 初始化 Prometheus 指标并替换全局 Provider
func InitPrometheus() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())
	SetProvider(NewPrometheusProvider(registry))
	initMetrics()
	return registry
}
