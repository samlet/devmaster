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
        run relay worker dev web-install web-dev web-build web-check \
        test test-integration lint build check clean

help:
	@echo "make tools            检查 go / buf / atlas"
	@echo "make db-create        建 owner/app 角色与 dev/test 库"
	@echo "make migrate          对 $(DB_NAME) 应用迁移并授权运行时角色"
	@echo "make migrate-test     对 $(TEST_DB_NAME) 应用迁移"
	@echo "make proto            生成 Go proto 代码"
	@echo "make run              起 server（gRPC $(GRPC_PORT) / HTTP $(HTTP_PORT)）"
	@echo "make relay            outbox → JetStream"
	@echo "make worker           Temporal worker"
	@echo "make dev              server + relay + worker 同起同停"
	@echo "make web-install      安装前端依赖（pnpm）"
	@echo "make web-dev          控制台 :3031"
	@echo "make web-build        控制台生产构建"
	@echo "make web-check        tsc --noEmit"
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

# 三个进程同起同停：任一退出即整体退出，避免"半边活着"的假象。
dev:
	@trap 'kill 0' EXIT INT TERM; \
	( cd backend && go run ./cmd/server -config configs/local.example.yaml ) & \
	( cd backend && go run ./cmd/relay  -config configs/local.example.yaml ) & \
	( cd backend && go run ./cmd/worker -config configs/local.example.yaml ) & \
	wait

web-install:
	pnpm --dir web install

web-dev:
	pnpm --dir web dev --port 3031

web-build:
	pnpm --dir web build

web-check:
	pnpm --dir web typecheck

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
