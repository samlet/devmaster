package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewProductNormalizesKey(t *testing.T) {
	product, err := NewProduct(NewProductInput{
		OrganizationID: uuid.New(),
		Key:            " hosp ",
		Name:           "  酒店 PMS  ",
	})
	if err != nil {
		t.Fatalf("NewProduct 报错：%v", err)
	}
	if product.Key != "HOSP" {
		t.Fatalf("key = %q，want HOSP", product.Key)
	}
	if product.Name != "酒店 PMS" {
		t.Fatalf("name = %q", product.Name)
	}
	if product.Status != StatusActive {
		t.Fatalf("status = %s，want ACTIVE", product.Status)
	}
}

func TestNewProductRejectsBadKeys(t *testing.T) {
	for _, key := range []string{"", "1HOSP", "HOSP-1", "ABCDEFGHIJK", " H O "} {
		if _, err := NewProduct(NewProductInput{
			OrganizationID: uuid.New(),
			Key:            key,
			Name:           "x",
		}); err == nil {
			t.Fatalf("key %q 应当被拒绝", key)
		}
	}
}

func TestNewProductRejectsBadNames(t *testing.T) {
	if _, err := NewProduct(NewProductInput{
		OrganizationID: uuid.New(),
		Key:            "HOSP",
		Name:           "   ",
	}); err == nil {
		t.Fatal("空名称应当被拒绝")
	}
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NewProduct(NewProductInput{
		OrganizationID: uuid.New(),
		Key:            "HOSP",
		Name:           string(long),
	}); err == nil {
		t.Fatal("超长名称应当被拒绝")
	}
}

func TestArchiveIsOneWay(t *testing.T) {
	product, err := NewProduct(NewProductInput{
		OrganizationID: uuid.New(),
		Key:            "FI",
		Name:           "财务",
	})
	if err != nil {
		t.Fatalf("NewProduct 报错：%v", err)
	}
	if err := product.Archive(); err != nil {
		t.Fatalf("Archive 报错：%v", err)
	}
	if product.Status != StatusArchived {
		t.Fatalf("status = %s，want ARCHIVED", product.Status)
	}
	if err := product.Archive(); err == nil {
		t.Fatal("重复归档应当被拒绝")
	}
}
