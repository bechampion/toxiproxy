package collectors

import (
	"github.com/prometheus/client_golang/prometheus"
)

type ProxyMetricCollectors struct {
	collectors  []prometheus.Collector
	proxyLabels []string

	ReceivedBytesTotal      *prometheus.CounterVec
	SentBytesTotal          *prometheus.CounterVec
	ConnectionDuration      *prometheus.HistogramVec
	RealConnectionDuration  *prometheus.HistogramVec
}

func (c *ProxyMetricCollectors) Collectors() []prometheus.Collector {
	return c.collectors
}

func NewProxyMetricCollectors() *ProxyMetricCollectors {
	var m ProxyMetricCollectors
	m.proxyLabels = []string{
		"direction",
		"proxy",
		"listener",
		"upstream",
	}
	m.ReceivedBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "proxy",
			Name:      "received_bytes_total",
		},
		m.proxyLabels)
	m.collectors = append(m.collectors, m.ReceivedBytesTotal)

	m.SentBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "proxy",
			Name:      "sent_bytes_total",
		},
		m.proxyLabels)
	m.collectors = append(m.collectors, m.SentBytesTotal)

	m.ConnectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "proxy",
			Name:      "connection_duration_seconds",
			Help:      "Duration of proxy connections in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"proxy", "listener", "upstream"})
	m.collectors = append(m.collectors, m.ConnectionDuration)

	m.RealConnectionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "proxy",
			Name:      "real_connection_duration_seconds",
			Help:      "Real duration of proxy connections excluding artificial latency from toxics in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"proxy", "listener", "upstream"})
	m.collectors = append(m.collectors, m.RealConnectionDuration)

	return &m
}
