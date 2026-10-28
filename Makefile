SHELL := /bin/bash

DB_NAME ?= devmaster_dev
TEST_DB_NAME ?= devmaster_test
OWNER_ROLE ?= devmaster_owner
APP_ROLE ?= devmaster_app
PG_HOST ?= 127.0.0.1
PG_PORT ?= 5432
ATLAS_URL ?= postgres://$(OWNER_ROLE)@$(PG_HOST):$(PG_PORT)/$(DB_NAME)?sslmode=disable
ATLAS_TEST_URL ?= postgres://$(OWNER_ROLE)@$(PG_HOST):$(PG_PORT)/$(TEST_DB_NAME)?sslmode=disable
MIGRATIONS_DIR := backend/migrations
GRPC_PORT ?= 50091
HTTP_PORT ?= 8291

.PHONY: help tools proto proto-check db-create migrate migrate-test migrate-status \
        run relay worker consumer dev web-install web-dev web-build web-check \
        seed-check seed-dataset reconcile snapshot report-snapshot gitea-reconcile approver-bootstrap version-gate test-version-gate perf-baseline test test-integration lint build check clean

help:
	@echo "make tools            检查 go / buf / atlas"
	@echo "make db-create        建 owner/app 角色与 dev/test 库"
	@echo "make migrate          对 $(DB_NAME) 应用迁移并授权运行时角色"
	@echo "make migrate-test     对 $(TEST_DB_NAME) 应用迁移"
	@echo "make proto            生成 Go proto 代码"
	@echo "make run              起 server（gRPC $(GRPC_PORT) / HTTP $(HTTP_PORT)）"
	@echo "make relay            outbox → JetStream"
	@echo "make worker           Temporal worker"
	@echo "make consumer         消费 release.submitted 起发布工作流"
	@echo "make consumer         消费 release.submitted 起发布工作流"
	@echo "make dev              server + relay + worker 同起同停"
	@echo "make web-install      安装前端依赖（pnpm）"
	@echo "make web-dev          控制台 :3031"
	@echo "make web-build        控制台生产构建"
	@echo "make web-check        tsc --noEmit"
	@echo "make seed-check       校验数据包结构（不连库）"
	@echo "make seed-dataset     导入数据包（幂等，默认 m1-smoke）"
	@echo "make reconcile        事件折叠对账（需 ORGANIZATION_ID）"
	@echo "make snapshot         给进行中冲刺写当天燃尽快照"
	@echo "make approver-bootstrap 发布发布审批流程定义到 approver"
	@echo "make gitea-reconcile  Gitea 提交对账（补齐 webhook 漏关联）"
	@echo "make version-gate     部署前置校验（PRODUCT/VERSION）"
	@echo "make test-version-gate 版本门禁验收（需 server 在跑）"
	@echo "make perf-baseline    10 万项报表 P95 基线（需 server 在跑）"
	@echo "make report-snapshot  固化报表日快照（DAYS=1 日报 / DAYS=60 回填）"
	@echo "make test             buf lint + go test"
	@echo "make test-integration 真连 PG 的集成测试（-p 1）"
	@echo "make lint             go vet + buf lint"
	@echo "make build            编译全部进程"
	@echo "make check            lint + test（提交前跑这个）"

tools:
	@command -v go >/dev/null || (echo "go not found" && exit 1)
	@command -v buf >/dev/null || (echo "buf not found" && exit 1)
	@command -v atlas >/dev/null || (echo "atlas not found" && exit 1)
	@echo "tools ok"

proto: tools
	buf lint
	buf generate

proto-check: proto
	git diff --exit-code -- backend/gen

db-create:
	./scripts/bootstrap-db.sh

migrate: db-create
	atlas migrate hash --dir "file://$(MIGRATIONS_DIR)"
	atlas migrate apply --dir "file://$(MIGRATIONS_DIR)" --url "$(ATLAS_URL)"
	psql -d $(DB_NAME) -v ON_ERROR_STOP=1 -q -f scripts/grant-runtime.sql

migrate-test: db-create
	atlas migrate hash --dir "file://$(MIGRATIONS_DIR)"
	atlas migrate apply --dir "file://$(MIGRATIONS_DIR)" --url "$(ATLAS_TEST_URL)"
	psql -d $(TEST_DB_NAME) -v ON_ERROR_STOP=1 -q -f scripts/grant-runtime.sql

migrate-status:
	atlas migrate status --dir "file://$(MIGRATIONS_DIR)" --url "$(ATLAS_URL)"

run:
	cd backend && go run ./cmd/server -config configs/local.example.yaml

relay:
	cd backend && go run ./cmd/relay -config configs/local.example.yaml

worker:
	cd backend && go run ./cmd/worker -config configs/local.example.yaml

consumer:
	cd backend && go run ./cmd/consumer -config configs/local.example.yaml

consumer:
	cd backend && go run ./cmd/consumer -config configs/local.example.yaml

# 三个进程同起同停：任一退出即整体退出，避免"半边活着"的假象。
dev:
	@trap 'kill 0' EXIT INT TERM; \
	( cd backend && go run ./cmd/server -config configs/local.example.yaml ) & \
	( cd backend && go run ./cmd/relay  -config configs/local.example.yaml ) & \
	( cd backend && go run ./cmd/worker   -config configs/local.example.yaml ) & \
	( cd backend && go run ./cmd/consumer -config configs/local.example.yaml ) & \
	wait

web-install:
	pnpm --dir web install

web-dev:
	pnpm --dir web dev --port 3031

web-build:
	pnpm --dir web build

web-check:
	pnpm --dir web typecheck

DATASET ?= assets/datasets/m1-smoke
ORGANIZATION_ID ?= 00000000-0000-0000-0000-000000000001

seed-check:
	cd backend && go run ./cmd/importer -dataset ../$(DATASET) -dry-run

seed-dataset:
	cd backend && go run ./cmd/importer -dataset ../$(DATASET) -config configs/local.example.yaml

reconcile:
	cd backend && go run ./cmd/reconcile -organization-id $(ORGANIZATION_ID) -config configs/local.example.yaml

snapshot:
	cd backend && go run ./cmd/snapshot -organization-id $(ORGANIZATION_ID) -config configs/local.example.yaml

approver-bootstrap:
	./scripts/approver-bootstrap.sh

gitea-reconcile:
	cd backend && go run ./cmd/gitea-reconcile -organization-id $(ORGANIZATION_ID) -config configs/local.example.yaml

PRODUCT ?= HOSP
VERSION ?= 1.0.0

version-gate:
	cd backend && go run ./cmd/version-gate -product $(PRODUCT) -version $(VERSION)

test-version-gate:
	./scripts/test-version-gate.sh

REPORT_SNAPSHOT_DAYS ?= 1
report-snapshot:
	cd backend && go run ./cmd/report-snapshot -organization-id $(ORGANIZATION_ID) -days $(REPORT_SNAPSHOT_DAYS) -config configs/local.example.yaml

PERF_ITEMS ?= 100000
perf-baseline:
	cd backend && go run ./cmd/perfbaseline -config configs/local.example.yaml -items $(PERF_ITEMS)

test: tools
	buf lint
	cd backend && go test ./...

test-integration: migrate-test
	cd backend && DEVMASTER_RUN_INTEGRATION=1 DEVMASTER_DATABASE_NAME=$(TEST_DB_NAME) \
		go test -p 1 ./... -count=1

lint: tools
	buf lint
	cd backend && go vet ./...

build: tools
	buf lint
	cd backend && go build ./...

check: lint test

clean:
	cd backend && go clean ./...
