package metrics

import "time"

// Counter 计数器接口
type Counter interface {
	Inc(labels ...string)
	Add(value float64, labels ...string)
}

// Histogram 直方图接口（用于耗时统计）
type Histogram interface {
	Observe(value float64, labels ...string)
}

// Provider 指标提供者接口，后续可接入 Prometheus 等
type Provider interface {
	NewCounter(name, help string, labelNames ...string) Counter
	NewHistogram(name, help string, labelNames ...string) Histogram
}

// --- noop 默认实现 ---

type noopCounter struct{}

func (n *noopCounter) Inc(labels ...string)              {}
func (n *noopCounter) Add(value float64, labels ...string) {}

type noopHistogram struct{}

func (n *noopHistogram) Observe(value float64, labels ...string) {}

type noopProvider struct{}

func (n *noopProvider) NewCounter(name, help string, labelNames ...string) Counter {
	return &noopCounter{}
}
func (n *noopProvider) NewHistogram(name, help string, labelNames ...string) Histogram {
	return &noopHistogram{}
}

// --- 全局实例 ---

var p Provider = &noopProvider{}

// SetProvider 替换全局指标提供者（如接入 Prometheus 时调用）
func SetProvider(provider Provider) {
	p = provider
}

// 预定义指标
var (
	APIRequestTotal    Counter
	APIRequestDuration Histogram
	WorkerTaskTotal    Counter
	WorkerTaskDuration Histogram
	UpstreamCallTotal  Counter
	UpstreamDuration   Histogram
)

func init() {
	initMetrics()
}

func initMetrics() {
	APIRequestTotal = p.NewCounter("api_request_total", "Total API requests", "method", "path", "status")
	APIRequestDuration = p.NewHistogram("api_request_duration_seconds", "API request duration", "method", "path")
	WorkerTaskTotal = p.NewCounter("worker_task_total", "Total worker tasks", "type", "status")
	WorkerTaskDuration = p.NewHistogram("worker_task_duration_seconds", "Worker task duration", "type")
	UpstreamCallTotal = p.NewCounter("upstream_call_total", "Total upstream API calls", "provider", "status")
	UpstreamDuration = p.NewHistogram("upstream_call_duration_seconds", "Upstream API call duration", "provider")
}

// SinceMs 计算从 start 到现在的秒数（用于 Histogram.Observe）
func SinceMs(start time.Time) float64 {
	return time.Since(start).Seconds()
}
