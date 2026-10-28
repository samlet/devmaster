package domain

import "testing"

func TestPercentileNearestRank(t *testing.T) {
	samples := []float64{10, 20, 30, 40, 50}
	cases := map[float64]float64{0.5: 30, 0.85: 50, 0.95: 50}
	for p, want := range cases {
		got, ok := Percentile(samples, p)
		if !ok || got != want {
			t.Fatalf("p=%.2f → %v，want %v", p, got, want)
		}
	}
	if _, ok := Percentile(nil, 0.5); ok {
		t.Fatal("空样本应返回 ok=false")
	}
}

func TestSummarizeRequiresMinimumSamples(t *testing.T) {
	if _, ok := Summarize([]float64{1, 2, 3, 4}, 5); ok {
		t.Fatal("样本不足 5 个不应输出分位数")
	}
	summary, ok := Summarize([]float64{1, 2, 3, 4, 5}, 5)
	if !ok || summary.Count != 5 || summary.P50 != 3 {
		t.Fatalf("summary = %+v ok=%v", summary, ok)
	}
}
