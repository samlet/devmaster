package domain

import (
	"math"
	"sort"
)

// Percentile 用 nearest-rank 取分位数：排序后取 ceil(p×n)-1。
// 样本为空或 p 越界时返回 ok=false。
func Percentile(samples []float64, p float64) (float64, bool) {
	if len(samples) == 0 || p <= 0 || p > 1 {
		return 0, false
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index], true
}

type Summary struct {
	P50   float64
	P85   float64
	P95   float64
	Count int
}

// Summarize 在样本少于 minSamples 时返回 ok=false：
// 1 个样本的 P95 没有意义，不该出现在报表上。
func Summarize(samples []float64, minSamples int) (Summary, bool) {
	if len(samples) < minSamples || len(samples) == 0 {
		return Summary{}, false
	}
	p50, _ := Percentile(samples, 0.50)
	p85, _ := Percentile(samples, 0.85)
	p95, _ := Percentile(samples, 0.95)
	return Summary{P50: p50, P85: p85, P95: p95, Count: len(samples)}, true
}
