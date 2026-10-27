package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDatasetHandlesBOMAndOptionalColumns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
		"name": "test",
		"version": 1,
		"organization_id": "00000000-0000-0000-0000-000000000001",
		"files": [
			{"path": "products.csv", "kind": "products", "required": true},
			{"path": "work_items.csv", "kind": "work_items", "required": true}
		]
	}`)
	// Excel 导出的 CSV 常带 BOM：不兼容它，导入器在真实数据上第一步就失败。
	writeFile(t, filepath.Join(dir, "products.csv"), "\ufeffkey,name,description\nHOSP,酒店 PMS,说明\n")
	writeFile(t, filepath.Join(dir, "work_items.csv"),
		"product_key,type,title,external_ref,priority,status\nHOSP,TASK,接入登录,m1-1,P1,DONE\n")

	dataset, err := LoadDataset(dir)
	if err != nil {
		t.Fatalf("LoadDataset 报错：%v", err)
	}
	if len(dataset.Products) != 1 || dataset.Products[0].Key != "HOSP" {
		t.Fatalf("产品解析不对：%+v", dataset.Products)
	}
	if len(dataset.WorkItems) != 1 || dataset.WorkItems[0].ExternalRef != "m1-1" {
		t.Fatalf("工作项解析不对：%+v", dataset.WorkItems)
	}
}

func TestLoadDatasetRejectsMissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
		"name": "test", "version": 1,
		"organization_id": "00000000-0000-0000-0000-000000000001",
		"files": [{"path": "products.csv", "kind": "products", "required": true}]
	}`)
	writeFile(t, filepath.Join(dir, "products.csv"), "code,name\nHOSP,酒店\n")
	if _, err := LoadDataset(dir); err == nil {
		t.Fatal("缺少 key 列应当被拒绝")
	}
}

func TestTransitionPath(t *testing.T) {
	taskPath, err := transitionPath("TASK", "BACKLOG", "DONE")
	if err != nil {
		t.Fatalf("TASK→DONE 找不到路径：%v", err)
	}
	if len(taskPath) != 4 {
		t.Fatalf("TASK→DONE 路径长度 = %d，want 4：%v", len(taskPath), taskPath)
	}
	bugPath, err := transitionPath("BUG", "OPEN", "CLOSED")
	if err != nil {
		t.Fatalf("BUG→CLOSED 找不到路径：%v", err)
	}
	if len(bugPath) != 4 {
		t.Fatalf("BUG→CLOSED 路径长度 = %d，want 4：%v", len(bugPath), bugPath)
	}
	if _, err := transitionPath("TASK", "BACKLOG", "CLOSED"); err == nil {
		t.Fatal("TASK 到 CLOSED 没有合法路径，应当报错")
	}
}

func TestParsePriorityLabel(t *testing.T) {
	for label, want := range map[string]int16{"P0": 0, "P1": 1, "p3": 3} {
		got, err := parsePriorityLabel(label)
		if err != nil || int16(got) != want {
			t.Fatalf("%s → %v %v，want %d", label, got, err, want)
		}
	}
	if _, err := parsePriorityLabel("P9"); err == nil {
		t.Fatal("P9 应当被拒绝")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}
