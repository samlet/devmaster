package domain

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
)

// 排序用固定宽度的 base36 分数索引：rank 是 10 位、左补零的字符串，
// 字典序即数值序。
//
// 为什么不用浮点：浮点在反复取中点后会失去精度，而"排序悄悄错乱"是那种
// 一年后才被发现的缺陷。为什么不用变长 LexoRank：变长实现要处理进位与
// 重新平衡，M1 只需要"追加到末尾"和"插到中间"，定宽数值足够，且行为可穷举。
//
// 代价：两个相邻 rank 之间最多插入约 51 次（每次减半）就会耗尽空间。
// 耗尽时明确报错，由 M2 的 rebalance 重排整列 —— 不静默退化。
const (
	rankWidth = 10
	rankBase  = 36
	// rankStep 是追加时的步长。留出间隔，才能在两枚相邻项之间插入；
	// 步长为 1 时"追加两项"立刻就没有中间值可用了。
	rankStep = 1 << 10
)

var rankSpace int64 = func() int64 {
	value := int64(1)
	for i := 0; i < rankWidth; i++ {
		value *= rankBase
	}
	return value
}()

// ErrRankExhausted 表示相邻两项之间没有可插入的整数了。
var ErrRankExhausted = apperr.FailedPrecondition("rank", "rank space exhausted between neighbours; rebalance required")

func RankInitial() string { return formatRank(rankSpace / 2) }

// RankForSequence 把单调序号映射成 rank。
//
// 创建时用它而不是"查当前最大 rank 再加一步"：序号由 product_counters 的行锁
// 分配，天然唯一且单调，因此 rank 不会重复，也不需要额外查询。
func RankForSequence(sequence int64) (string, error) {
	if sequence <= 0 {
		return "", apperr.Invalid("sequence", "must be positive")
	}
	value := sequence * rankStep
	if value >= rankSpace {
		return "", ErrRankExhausted
	}
	return formatRank(value), nil
}

func RankAfter(previous string) (string, error) {
	if strings.TrimSpace(previous) == "" {
		return RankInitial(), nil
	}
	value, err := parseRank(previous)
	if err != nil {
		return "", err
	}
	if value+rankStep >= rankSpace {
		return "", ErrRankExhausted
	}
	return formatRank(value + rankStep), nil
}

func RankBefore(next string) (string, error) {
	if strings.TrimSpace(next) == "" {
		return RankInitial(), nil
	}
	value, err := parseRank(next)
	if err != nil {
		return "", err
	}
	if value <= 1 {
		return "", ErrRankExhausted
	}
	return formatRank(value / 2), nil
}

// RankBetween 返回严格位于 previous 与 next 之间的 rank。
// 空字符串表示"这一端没有边界"。
func RankBetween(previous, next string) (string, error) {
	if strings.TrimSpace(previous) == "" && strings.TrimSpace(next) == "" {
		return RankInitial(), nil
	}
	if strings.TrimSpace(previous) == "" {
		return RankBefore(next)
	}
	if strings.TrimSpace(next) == "" {
		value, err := parseRank(previous)
		if err != nil {
			return "", err
		}
		mid := value + (rankSpace-value)/2
		if mid <= value || mid >= rankSpace {
			return "", ErrRankExhausted
		}
		return formatRank(mid), nil
	}

	left, err := parseRank(previous)
	if err != nil {
		return "", err
	}
	right, err := parseRank(next)
	if err != nil {
		return "", err
	}
	if left >= right {
		return "", apperr.FailedPrecondition("rank", fmt.Sprintf(
			"previous rank %q is not before next rank %q", previous, next))
	}
	if right-left <= 1 {
		return "", ErrRankExhausted
	}
	return formatRank(left + (right-left)/2), nil
}

func parseRank(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != rankWidth {
		return 0, apperr.Invalid("rank", fmt.Sprintf("rank must be %d characters, got %q", rankWidth, value))
	}
	parsed, err := strconv.ParseInt(trimmed, rankBase, 64)
	if err != nil || parsed < 0 || parsed >= rankSpace {
		return 0, apperr.Invalid("rank", fmt.Sprintf("rank %q is not a valid base36 value", value))
	}
	return parsed, nil
}

func formatRank(value int64) string {
	encoded := strconv.FormatInt(value, rankBase)
	if len(encoded) >= rankWidth {
		return encoded[len(encoded)-rankWidth:]
	}
	return strings.Repeat("0", rankWidth-len(encoded)) + encoded
}
