package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/dbtest"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	reportpostgres "github.com/xiaofeiwu/devmaster/backend/internal/report/adapter/postgres"
	reportapp "github.com/xiaofeiwu/devmaster/backend/internal/report/application"
)

func TestReportsFromEvents(t *testing.T) {
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

	base := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Hour)
	err = db.InOrgTx(context.Background(), org, func(tx *sql.Tx) error {
		for i := 1; i <= 8; i++ {
			id := uuid.New()
			created := base
			done := base.Add(24 * time.Hour)
			status := "DONE"
			if i <= 2 {
				status = "IN_PROGRESS"
			}
			if _, err := tx.ExecContext(context.Background(), `
				INSERT INTO work_items (id, organization_id, key, product_id, type, status, title,
					reporter_id, assignee_id, rank, created_at, updated_at)
				VALUES ($1,$2,$3,$4,'TASK',$5,$6,'alice','bob',$7,$8,$8)`,
				id, org, "HOSP-"+string(rune('0'+i)), product.ID, status, "item",
				"000000000"+string(rune('0'+i)), created); err != nil {
				return err
			}
			events := []struct {
				status string
				at     time.Time
			}{{"BACKLOG", created}, {status, done}}
			for _, event := range events {
				if _, err := tx.ExecContext(context.Background(), `
					INSERT INTO work_item_events (organization_id, work_item_id, event_type, to_status, actor_id, payload, occurred_at)
					VALUES ($1,$2,'transitioned',$3,'alice','{}',$4)`,
					org, id, event.status, event.at); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("造数失败：%v", err)
	}

	service := reportapp.NewService(reportpostgres.NewRepository(db))
	actor := reportapp.Actor{Subject: "alice", OrganizationID: org}
	from, to := base.Add(-time.Hour), base.AddDate(0, 0, 2)

	summaries, err := service.CycleTime(context.Background(), actor, &product.ID, from, to)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("周期时间 = %+v err=%v", summaries, err)
	}
	if summaries[0].Count != 6 || summaries[0].P50 < 23.9 || summaries[0].P50 > 24.1 {
		t.Fatalf("周期时间不对：%+v", summaries[0])
	}

	buckets, err := service.Throughput(context.Background(), actor, &product.ID, from, to)
	if err != nil || len(buckets) == 0 {
		t.Fatalf("吞吐 = %+v err=%v", buckets, err)
	}
	total := 0
	for _, bucket := range buckets {
		total += bucket.Count
	}
	if total != 6 {
		t.Fatalf("吞吐合计 = %d，want 6", total)
	}

	load, err := service.TeamLoad(context.Background(), actor, &product.ID)
	if err != nil || len(load) != 1 {
		t.Fatalf("负载 = %+v err=%v", load, err)
	}
	if load[0].Assignee != "bob" || load[0].OpenItems != 2 || load[0].WIPItems != 2 {
		t.Fatalf("负载不对：%+v", load[0])
	}

	points, err := service.CFD(context.Background(), actor, &product.ID, from, to)
	if err != nil || len(points) < 2 {
		t.Fatalf("CFD = %+v err=%v", points, err)
	}
	last := points[len(points)-1]
	if last.Done != 6 || last.InProgress != 2 {
		t.Fatalf("CFD 末点不对：%+v", last)
	}
}
