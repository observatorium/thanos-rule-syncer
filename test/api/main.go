package main

import (
	"fmt"
	"log"
	"net/http"
)

// Run this HTTP server for a deterministic rules API for testing.

func main() {
	m := http.NewServeMux()
	m.HandleFunc("/api/metrics/v1/test-oidc/rules", tenantTest)

	addr := ":8443"
	fmt.Println("serving mocked api rules", addr)
	if err := http.ListenAndServe(addr, m); err != nil {
		log.Fatal(err)
	}
}

const testRules = `
groups:
  - name: kubelet.rules
    interval: 0s
    rules:
      - record: node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile
        expr: |
            histogram_quantile(0.99, sum(rate(kubelet_pleg_relist_duration_seconds_bucket[5m])) by (instance, le) * on(instance) group_left(node) kubelet_node_name{job="kubelet"})
        labels:
            quantile: "0.99"
      - record: node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile
        expr: |
            histogram_quantile(0.9, sum(rate(kubelet_pleg_relist_duration_seconds_bucket[5m])) by (instance, le) * on(instance) group_left(node) kubelet_node_name{job="kubelet"})
        labels:
            quantile: "0.9"
      - record: node_quantile:kubelet_pleg_relist_duration_seconds:histogram_quantile
        expr: |
            histogram_quantile(0.5, sum(rate(kubelet_pleg_relist_duration_seconds_bucket[5m])) by (instance, le) * on(instance) group_left(node) kubelet_node_name{job="kubelet"})
        labels:
            quantile: "0.5"
  - name: nodes.rules
    interval: 0s
    rules: []
`

func tenantTest(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(testRules))
}
