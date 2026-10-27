package ent

// ent 客户端由 schema 生成。迁移不依赖 AutoMigrate：所有 DDL 走
// backend/migrations 下的版本化 SQL（CHECK、部分索引、RLS 这些 ent 表达不了）。
//
//go:generate go run entgo.io/ent/cmd/ent generate --feature sql/upsert ./schema
