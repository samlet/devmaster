package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	productapp "github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	versionapp "github.com/xiaofeiwu/devmaster/backend/internal/version/application"
	versiondomain "github.com/xiaofeiwu/devmaster/backend/internal/version/domain"
)

type fakeProducts struct {
	createCalls int
	lastCmd     productapp.CreateCommand
	err         error
}

func (f *fakeProducts) Create(_ context.Context, _ productapp.Actor, cmd productapp.CreateCommand) (productdomain.Product, error) {
	f.createCalls++
	f.lastCmd = cmd
	if f.err != nil {
		return productdomain.Product{}, f.err
	}
	return productdomain.Product{ID: uuid.New(), Key: cmd.Key, Name: cmd.Name}, nil
}

type fakeVersions struct {
	createCalls int
	assignCalls int
	lastItems   []uuid.UUID
	err         error
}

func (f *fakeVersions) Create(_ context.Context, _ versionapp.Actor, cmd versionapp.CreateCommand) (versiondomain.Version, error) {
	f.createCalls++
	if f.err != nil {
		return versiondomain.Version{}, f.err
	}
	return versiondomain.Version{ID: uuid.New(), Name: cmd.Name, Status: versiondomain.StatusPlanned}, nil
}

func (f *fakeVersions) AssignItems(_ context.Context, _ versionapp.Actor, _ uuid.UUID, itemIDs []uuid.UUID) (int, error) {
	f.assignCalls++
	f.lastItems = itemIDs
	if f.err != nil {
		return 0, f.err
	}
	return len(itemIDs), nil
}

func catalogServer(products *fakeProducts, versions *fakeVersions, items *fakeWorkItems) *Server {
	return &Server{
		productsW:             products,
		versionsW:             versions,
		workItems:             items,
		bffSecret:             testBFFSecret,
		logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		defaultOrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	}
}

func TestCreateProductAcceptsSignedRequest(t *testing.T) {
	products := &fakeProducts{}
	server := catalogServer(products, &fakeVersions{}, &fakeWorkItems{})

	recorder := httptest.NewRecorder()
	server.handleCreateProduct(recorder, signedPost(t, "/api/products", map[string]any{
		"key": "PAY", "name": "支付平台", "description": "收单与结算", "actor": "alice",
	}, false))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if products.createCalls != 1 || products.lastCmd.Key != "PAY" {
		t.Fatalf("cmd=%+v", products.lastCmd)
	}
}

// 产品 key 重复是业务冲突，要给人话而不是 internal error。
func TestCreateProductMapsDuplicateKeyToConflict(t *testing.T) {
	products := &fakeProducts{err: apperr.Conflict("key", `duplicate key value violates unique constraint "products_key_key"`)}
	server := catalogServer(products, &fakeVersions{}, &fakeWorkItems{})

	recorder := httptest.NewRecorder()
	server.handleCreateProduct(recorder, signedPost(t, "/api/products", map[string]any{
		"key": "PAY", "name": "支付平台", "actor": "alice",
	}, false))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateVersionAcceptsSignedRequest(t *testing.T) {
	versions := &fakeVersions{}
	server := catalogServer(&fakeProducts{}, versions, &fakeWorkItems{})

	recorder := httptest.NewRecorder()
	server.handleCreateVersion(recorder, signedPost(t, "/api/versions", map[string]any{
		"product_id":   "44444444-4444-7444-8444-444444444444",
		"name":         "2.0.0",
		"planned_date": "2026-12-01",
		"actor":        "alice",
	}, false))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if versions.createCalls != 1 {
		t.Fatalf("calls=%d", versions.createCalls)
	}
}

func TestAssignVersionItemsResolvesKeys(t *testing.T) {
	versions := &fakeVersions{}
	server := catalogServer(&fakeProducts{}, versions, &fakeWorkItems{})

	id := uuid.NewString()
	request := signedPost(t, "/api/versions/"+id+"/items", map[string]any{
		"item_keys": []string{"DEMO-1"}, "actor": "alice",
	}, false)
	request.SetPathValue("id", id)

	recorder := httptest.NewRecorder()
	server.handleAssignVersionItems(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if versions.assignCalls != 1 || len(versions.lastItems) != 1 {
		t.Fatalf("items=%v", versions.lastItems)
	}
}

// 版本范围冻结后挂项必须 409：这是发布链的关键约束。
func TestAssignVersionItemsMapsFrozenToConflict(t *testing.T) {
	versions := &fakeVersions{err: apperr.FailedPrecondition("version", "version scope is frozen")}
	server := catalogServer(&fakeProducts{}, versions, &fakeWorkItems{})

	id := uuid.NewString()
	request := signedPost(t, "/api/versions/"+id+"/items", map[string]any{
		"item_keys": []string{"DEMO-1"}, "actor": "alice",
	}, false)
	request.SetPathValue("id", id)

	recorder := httptest.NewRecorder()
	server.handleAssignVersionItems(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
}
