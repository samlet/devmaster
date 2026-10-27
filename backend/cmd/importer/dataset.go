package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manifest struct {
	Name           string     `json:"name"`
	Version        int        `json:"version"`
	OrganizationID string     `json:"organization_id"`
	Files          []FileSpec `json:"files"`
	Expectations   string     `json:"expectations"`
}

type FileSpec struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

type ProductRow struct {
	Key         string
	Name        string
	Description string
}

type WorkItemRow struct {
	ProductKey  string
	Type        string
	Title       string
	Description string
	Priority    string
	Severity    string
	Status      string
	AssigneeID  string
	ExternalRef string
}

type ProductExpectation struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type WorkItemExpectation struct {
	ExternalRef string `json:"external_ref"`
	Status      string `json:"status"`
}

type Expectations struct {
	ProductCount  int                   `json:"product_count"`
	WorkItemCount int                   `json:"work_item_count"`
	Products      []ProductExpectation  `json:"products"`
	WorkItems     []WorkItemExpectation `json:"work_items"`
}

type Dataset struct {
	Dir          string
	Manifest     Manifest
	Products     []ProductRow
	WorkItems    []WorkItemRow
	Expectations Expectations
}

func LoadDataset(dir string) (*Dataset, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("manifest.name is required")
	}
	if strings.TrimSpace(manifest.OrganizationID) == "" {
		return nil, fmt.Errorf("manifest.organization_id is required")
	}

	dataset := &Dataset{Dir: dir, Manifest: manifest}
	for _, file := range manifest.Files {
		path := filepath.Join(dir, file.Path)
		if _, err := os.Stat(path); err != nil {
			if file.Required {
				return nil, fmt.Errorf("required file %s missing: %w", file.Path, err)
			}
			continue
		}
		switch file.Kind {
		case "products":
			rows, err := readProducts(path)
			if err != nil {
				return nil, err
			}
			dataset.Products = append(dataset.Products, rows...)
		case "work_items":
			rows, err := readWorkItems(path)
			if err != nil {
				return nil, err
			}
			dataset.WorkItems = append(dataset.WorkItems, rows...)
		default:
			return nil, fmt.Errorf("unknown file kind %q in %s", file.Kind, file.Path)
		}
	}
	if manifest.Expectations != "" {
		raw, err := os.ReadFile(filepath.Join(dir, manifest.Expectations))
		if err != nil {
			return nil, fmt.Errorf("read expectations: %w", err)
		}
		if err := json.Unmarshal(raw, &dataset.Expectations); err != nil {
			return nil, fmt.Errorf("parse expectations: %w", err)
		}
	}
	return dataset, nil
}

func readProducts(path string) ([]ProductRow, error) {
	records, err := readCSV(path, []string{"key", "name"})
	if err != nil {
		return nil, err
	}
	rows := make([]ProductRow, 0, len(records))
	for index, record := range records {
		row := ProductRow{
			Key:         strings.TrimSpace(record["key"]),
			Name:        strings.TrimSpace(record["name"]),
			Description: strings.TrimSpace(record["description"]),
		}
		if row.Key == "" || row.Name == "" {
			return nil, fmt.Errorf("%s 第 %d 行：key 与 name 必填", path, index+2)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readWorkItems(path string) ([]WorkItemRow, error) {
	records, err := readCSV(path, []string{"product_key", "type", "title", "external_ref"})
	if err != nil {
		return nil, err
	}
	rows := make([]WorkItemRow, 0, len(records))
	for index, record := range records {
		row := WorkItemRow{
			ProductKey:  strings.TrimSpace(record["product_key"]),
			Type:        strings.TrimSpace(record["type"]),
			Title:       strings.TrimSpace(record["title"]),
			Description: strings.TrimSpace(record["description"]),
			Priority:    strings.TrimSpace(record["priority"]),
			Severity:    strings.TrimSpace(record["severity"]),
			Status:      strings.TrimSpace(record["status"]),
			AssigneeID:  strings.TrimSpace(record["assignee_id"]),
			ExternalRef: strings.TrimSpace(record["external_ref"]),
		}
		if row.ProductKey == "" || row.Type == "" || row.Title == "" || row.ExternalRef == "" {
			return nil, fmt.Errorf("%s 第 %d 行：product_key / type / title / external_ref 必填", path, index+2)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// readCSV 读取 CSV 并返回"列名 → 值"的行。必填列缺失时直接失败：
// 导入器最怕的不是报错，而是把空列当成合法值静默导进去。
func readCSV(path string, required []string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s 是空文件", path)
	}
	headers := make([]string, len(records[0]))
	for i, header := range records[0] {
		headers[i] = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(header, "\ufeff")))
	}
	columns := map[string]bool{}
	for _, header := range headers {
		columns[header] = true
	}
	for _, column := range required {
		if !columns[column] {
			return nil, fmt.Errorf("%s 缺少列 %q", path, column)
		}
	}

	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) == 1 && strings.TrimSpace(record[0]) == "" {
			continue
		}
		row := map[string]string{}
		for i, value := range record {
			if i < len(headers) {
				row[headers[i]] = value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
