package domain

import (
	"errors"
	"testing"
)

func TestRankInitialAndAppend(t *testing.T) {
	first := RankInitial()
	if len(first) != rankWidth {
		t.Fatalf("rank 长度 = %d，want %d", len(first), rankWidth)
	}
	second, err := RankAfter(first)
	if err != nil {
		t.Fatalf("RankAfter 报错：%v", err)
	}
	if second <= first {
		t.Fatalf("追加的 rank 必须更大：%s <= %s", second, first)
	}
	third, err := RankAfter(second)
	if err != nil || third <= second {
		t.Fatalf("连续追加失败：%v %s", err, third)
	}
}

func TestRankBetween(t *testing.T) {
	first := RankInitial()
	second, _ := RankAfter(first)
	middle, err := RankBetween(first, second)
	if err != nil {
		t.Fatalf("RankBetween 报错：%v", err)
	}
	if !(first < middle && middle < second) {
		t.Fatalf("中点不在区间内：%s < %s < %s", first, middle, second)
	}
	// 两端为空：返回初始值。
	if got, err := RankBetween("", ""); err != nil || got != RankInitial() {
		t.Fatalf("空区间应返回初始值：%s %v", got, err)
	}
	// 只有右边界：取前半段。
	if got, err := RankBetween("", second); err != nil || got >= second {
		t.Fatalf("左端为空时应在右边界之前：%s %v", got, err)
	}
}

func TestRankExhaustionIsExplicit(t *testing.T) {
	first := formatRank(100)
	second := formatRank(101)
	if _, err := RankBetween(first, second); !errors.Is(err, ErrRankExhausted) {
		t.Fatalf("相邻整数之间应当明确报耗尽，got %v", err)
	}
}

func TestRankRejectsBadInput(t *testing.T) {
	for _, value := range []string{"", "abc", "!!!!!!!!!!", "000000000000"} {
		if value == "" {
			continue
		}
		if _, err := parseRank(value); err == nil {
			t.Fatalf("rank %q 应当被拒绝", value)
		}
	}
	if _, err := RankBetween(formatRank(200), formatRank(100)); err == nil {
		t.Fatal("左边界大于右边界应当被拒绝")
	}
}
