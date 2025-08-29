package toxiproxy

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/Shopify/toxiproxy/v2/collectors"
	"github.com/Shopify/toxiproxy/v2/stream"
)

func TestProxyMetricsReceivedSentBytes(t *testing.T) {
	srv := NewServer(NewMetricsContainer(prometheus.NewRegistry()), zerolog.Nop())
	srv.Metrics.ProxyMetrics = collectors.NewProxyMetricCollectors()

	proxy := NewProxy(srv, "test_proxy_metrics_received_sent_bytes", "localhost:0", "upstream")

	r := bufio.NewReader(bytes.NewBufferString("hello"))
	w := &testWriteCloser{
		bufio.NewWriter(bytes.NewBuffer([]byte{})),
	}
	linkName := "testupstream"
	proxy.Toxics.StartLink(srv, linkName, r, w, stream.Upstream)
	proxy.Toxics.RemoveLink(linkName)

	actual := prometheusOutput(t, srv, "toxiproxy_proxy")

	// Check that we have the expected byte metrics
	expectedBytes := []string{
		`toxiproxy_proxy_received_bytes_total{` +
			`direction="upstream",listener="localhost:0",` +
			`proxy="test_proxy_metrics_received_sent_bytes",upstream="upstream"` +
			`} 5`,

		`toxiproxy_proxy_sent_bytes_total{` +
			`direction="upstream",listener="localhost:0",` +
			`proxy="test_proxy_metrics_received_sent_bytes",upstream="upstream"` +
			`} 5`,
	}

	// Check if we have connection duration metrics
	var foundBytes []string
	var foundDuration bool
	for _, metric := range actual {
		if strings.Contains(metric, "received_bytes_total") || strings.Contains(metric, "sent_bytes_total") {
			foundBytes = append(foundBytes, metric)
		}
		if strings.Contains(metric, "connection_duration_seconds") {
			foundDuration = true
		}
	}

	if !reflect.DeepEqual(foundBytes, expectedBytes) {
		t.Fatalf(
			"\nexpected byte metrics:\n  [%v]\ngot:\n  [%v]",
			strings.Join(expectedBytes, "\n  "),
			strings.Join(foundBytes, "\n  "),
		)
	}

	if !foundDuration {
		t.Fatal("Expected connection_duration_seconds metric not found")
	}
}

func TestRuntimeMetricsBuildInfo(t *testing.T) {
	srv := NewServer(NewMetricsContainer(prometheus.NewRegistry()), zerolog.Nop())
	srv.Metrics.RuntimeMetrics = collectors.NewRuntimeMetricCollectors()

	expected := `go_build_info{checksum="[^"]*",path="[^"]*",version="[^"]*"} 1`

	actual := prometheusOutput(t, srv, "go_build_info")

	if len(actual) != 1 {
		t.Fatalf(
			"\nexpected: 1 item\ngot: %d item(s)\nmetrics:\n  %+v",
			len(actual),
			strings.Join(actual, "\n  "),
		)
	}

	matched, err := regexp.MatchString(expected, actual[0])
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}
	if !matched {
		t.Fatalf("\nexpected:\n  %v\nto match:\n  %v", actual[0], expected)
	}
}

type testWriteCloser struct {
	*bufio.Writer
}

func (t *testWriteCloser) Close() error {
	return t.Flush()
}

func prometheusOutput(t *testing.T, apiServer *ApiServer, prefix string) []string {
	t.Helper()

	testServer := httptest.NewServer(apiServer.Metrics.handler())
	defer testServer.Close()

	resp, err := http.Get(testServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var selected []string
	s := bufio.NewScanner(resp.Body)
	for s.Scan() {
		if strings.HasPrefix(s.Text(), prefix) {
			selected = append(selected, s.Text())
		}
	}
	return selected
}
