package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewWorkLogValidates(t *testing.T) {
	base := NewWorkLogInput{
		OrganizationID: uuid.New(),
		WorkItemID:     uuid.New(),
		AuthorID:       "alice",
		Minutes:        90,
		WorkDate:       time.Now().UTC(),
	}
	if _, err := NewWorkLog(base); err != nil {
		t.Fatalf("合法工时被拒：%v", err)
	}
	for name, mutate := range map[string]func(*NewWorkLogInput){
		"零分钟":  func(i *NewWorkLogInput) { i.Minutes = 0 },
		"超过一天": func(i *NewWorkLogInput) { i.Minutes = 24*60 + 1 },
		"未来日期": func(i *NewWorkLogInput) { i.WorkDate = time.Now().AddDate(0, 0, 3) },
		"空作者":  func(i *NewWorkLogInput) { i.AuthorID = "  " },
	} {
		input := base
		mutate(&input)
		if _, err := NewWorkLog(input); err == nil {
			t.Fatalf("%s 应当被拒绝", name)
		}
	}
}

func TestNewReversal(t *testing.T) {
	original, err := NewWorkLog(NewWorkLogInput{
		OrganizationID: uuid.New(), WorkItemID: uuid.New(), AuthorID: "alice",
		Minutes: 90, WorkDate: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewWorkLog 报错：%v", err)
	}
	reversal, err := NewReversal(original, "bob", nil)
	if err != nil {
		t.Fatalf("NewReversal 报错：%v", err)
	}
	if reversal.Minutes != -90 || reversal.ReversalOf == nil || *reversal.ReversalOf != original.ID {
		t.Fatalf("更正记录不对：%+v", reversal)
	}
	if _, err := NewReversal(reversal, "carol", nil); err == nil {
		t.Fatal("更正记录不能再被更正")
	}
	// 部分冲正：30 分钟合法，0 与超过原值非法。
	if partial, err := NewReversalOf(original, 30, "bob", nil); err != nil || partial.Minutes != -30 {
		t.Fatalf("部分冲正不对：%+v err=%v", partial, err)
	}
	for _, amount := range []int{0, -1, 91} {
		if _, err := NewReversalOf(original, amount, "bob", nil); err == nil {
			t.Fatalf("冲正 %d 分钟应当被拒绝", amount)
		}
	}
}
