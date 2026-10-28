package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapp "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
	worklogpostgres "github.com/xiaofeiwu/devmaster/backend/internal/worklog/adapter/postgres"
	worklogapp "github.com/xiaofeiwu/devmaster/backend/internal/worklog/application"
)

type fixture struct {
	org   uuid.UUID
	item  workitemdomain.WorkItem
	svc   *worklogapp.Service
	actor worklogapp.Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := dbtest.DB(t)
	dbtest.Cleanup(t)
	org := uuid.New()
	product, err := productdomain.NewProduct(productdomain.NewProductInput{
		OrganizationID: org, Key: "HOSP", Name: "酒店 PMS",
	})
	if err != nil {
		t.Fatalf("构造产品失败：%v", err)
	}
	if _, err := productpostgres.NewRepository(db).Create(context.Background(), product, nil); err != nil {
		t.Fatalf("创建产品失败：%v", err)
	}
	item, err := workitemapp.NewService(workitempostgres.NewRepository(db)).Create(
		context.Background(),
		workitemapp.Actor{Subject: "alice", OrganizationID: org, Roles: []string{"DEV_MEMBER"}},
		workitemapp.CreateCommand{ProductID: product.ID, Type: workitemdomain.TypeTask, Title: "登录"},
	)
	if err != nil {
		t.Fatalf("创建工作项失败：%v", err)
	}
	return &fixture{
		org:   org,
		item:  item,
		svc:   worklogapp.NewService(worklogpostgres.NewRepository(db)),
		actor: worklogapp.Actor{Subject: "alice", OrganizationID: org},
	}
}

func TestWorkLogAppendOnlyAndReversal(t *testing.T) {
	f := newFixture(t)
	log, err := f.svc.Log(context.Background(), f.actor, worklogapp.LogCommand{
		WorkItemID: &f.item.ID,
		Minutes:    90,
		WorkDate:   time.Now().UTC(),
		GradeCode:  ptr("P5"),
	})
	if err != nil {
		t.Fatalf("记工时报错：%v", err)
	}
	if log.Minutes != 90 || log.WorkItemKey != f.item.Key {
		t.Fatalf("工时记录不对：%+v", log)
	}

	// E7 payload 必须与 PPM 契约一致。
	db := dbtest.DB(t)
	var payload []byte
	if err := db.SQL.QueryRow(
		`SELECT payload FROM outbox_events WHERE subject = 'dev.v1.work.logged'`).Scan(&payload); err != nil {
		t.Fatalf("读 outbox 失败：%v", err)
	}
	var envelope struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("解析 payload 失败：%v", err)
	}
	for _, field := range []string{"work_log_id", "work_item_key", "product_code", "minutes",
		"work_date", "project_code", "wbs_code", "author_id", "grade_code", "source"} {
		if _, ok := envelope.Payload[field]; !ok {
			t.Fatalf("E7 缺少字段 %s：%s", field, string(payload))
		}
	}
	if envelope.Payload["minutes"].(float64) != 90 || envelope.Payload["product_code"] != "HOSP" {
		t.Fatalf("E7 内容不对：%s", string(payload))
	}

	// 分次冲正：先冲 30，净额 60。
	thirty := 30
	reversal, err := f.svc.Reverse(context.Background(), f.actor, log.ID, &thirty, nil)
	if err != nil {
		t.Fatalf("部分更正失败：%v", err)
	}
	if reversal.Minutes != -30 || reversal.ReversalOf == nil {
		t.Fatalf("部分更正记录不对：%+v", reversal)
	}
	total, err := f.svc.Sum(context.Background(), f.actor, worklogapp.ListFilter{WorkItemID: &f.item.ID})
	if err != nil || total != 60 {
		t.Fatalf("部分更正后净额 = %d err=%v，want 60", total, err)
	}
	// 再冲 60，净额 0。
	sixty := 60
	if _, err := f.svc.Reverse(context.Background(), f.actor, log.ID, &sixty, nil); err != nil {
		t.Fatalf("第二次更正失败：%v", err)
	}
	total, err = f.svc.Sum(context.Background(), f.actor, worklogapp.ListFilter{WorkItemID: &f.item.ID})
	if err != nil || total != 0 {
		t.Fatalf("全额冲正后净额 = %d err=%v，want 0", total, err)
	}
	// 再冲 1 分钟应当冲突：累计已等于原工时。
	one := 1
	if _, err := f.svc.Reverse(context.Background(), f.actor, log.ID, &one, nil); err == nil {
		t.Fatal("超额更正应当被拒绝")
	} else if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindConflict {
		t.Fatalf("超额更正应当是 Conflict，got %v", err)
	}

	// 追加纪律：UPDATE / DELETE 必须被触发器拒绝。
	// 必须带组织上下文执行，否则 RLS 会先过滤成 0 行，触发器根本不会触发。
	if err := db.InOrgTx(context.Background(), f.org, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE work_logs SET minutes = 1`)
		return err
	}); err == nil {
		t.Fatal("UPDATE work_logs 应当被拒绝")
	}
	if err := db.InOrgTx(context.Background(), f.org, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM work_logs`)
		return err
	}); err == nil {
		t.Fatal("DELETE work_logs 应当被拒绝")
	}
}

func ptr(value string) *string { return &value }
