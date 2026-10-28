package application

import (
	"reflect"
	"testing"
)

func TestParseKeys(t *testing.T) {
	message := "HOSP-12 fix login\n\nHOSP-12 again, PPM-7 related; UTF-8 stays out"
	got := FilterByProducts(ParseKeys(message), map[string]bool{"HOSP": true, "PPM": true})
	want := []string{"HOSP-12", "PPM-7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v，want %v", got, want)
	}
}

func TestParseKeysRejectsLookalikes(t *testing.T) {
	for _, message := range []string{"hosp-1", "HOSP", "A-1", "HOSP-", "fix-123"} {
		if keys := ParseKeys(message); len(keys) != 0 {
			t.Fatalf("%q 不应解析出 key：%v", message, keys)
		}
	}
	// UTF-8 形态合法，但产品集合里没有 UTF，必须被过滤掉。
	if keys := FilterByProducts(ParseKeys("UTF-8"), map[string]bool{"HOSP": true}); len(keys) != 0 {
		t.Fatalf("UTF-8 不应通过产品过滤：%v", keys)
	}
}
