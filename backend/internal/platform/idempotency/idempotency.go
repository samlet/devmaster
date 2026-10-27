// Package idempotency 保证写接口"同一个 key 重放返回同一结果"。
//
// 语义与生态其它仓一致：
//   - 同 key 同请求 → 返回缓存响应；
//   - 同 key 异请求 → ABORTED（不能悄悄把两次不同的意图当成一次）；
//   - 保留 7 天，过期后同一个 key 可以重新使用。
package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
)

const ttl = 7 * 24 * time.Hour

// Do 执行一次幂等写。
//
// 泛型让调用方直接拿到具体响应类型，不必在调用处做 type assertion；
// newResponse 必须返回一个空的 proto 消息（用于反序列化缓存）。
func Do[T proto.Message](
	ctx context.Context,
	db *database.DB,
	organizationID uuid.UUID,
	scope string,
	key string,
	request proto.Message,
	newResponse func() T,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T
	if key == "" {
		return zero, status.Error(codes.InvalidArgument, "idempotency_key is required for write operations")
	}
	if newResponse == nil {
		return zero, errors.New("idempotency: newResponse must not be nil")
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return zero, fmt.Errorf("marshal idempotent request: %w", err)
	}
	requestHash := sha256.Sum256(raw)
	compositeKey := fmt.Sprintf("%s:%s:%s", organizationID, scope, key)

	if cached, found, err := load(ctx, db, organizationID, compositeKey, requestHash[:]); err != nil {
		return zero, err
	} else if found {
		response := newResponse()
		if err := proto.Unmarshal(cached, response); err != nil {
			return zero, fmt.Errorf("unmarshal cached response: %w", err)
		}
		return response, nil
	}

	response, err := fn(ctx)
	if err != nil {
		return zero, err
	}
	encoded, err := proto.Marshal(response)
	if err != nil {
		return zero, fmt.Errorf("marshal idempotent response: %w", err)
	}
	if err := save(ctx, db, organizationID, scope, compositeKey, requestHash[:], encoded); err != nil {
		return zero, err
	}
	return response, nil
}

func load(ctx context.Context, db *database.DB, organizationID uuid.UUID, key string, requestHash []byte) ([]byte, bool, error) {
	var (
		storedRequest  []byte
		storedResponse []byte
	)
	err := db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT request_hash, response_blob
			  FROM idempotency_keys
			 WHERE key = $1 AND expires_at > now()`, key).Scan(&storedRequest, &storedResponse)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load idempotency key: %w", err)
	}
	if !equalBytes(storedRequest, requestHash) {
		return nil, false, status.Error(codes.Aborted,
			"idempotency key was already used with a different request")
	}
	return storedResponse, true, nil
}

func save(ctx context.Context, db *database.DB, organizationID uuid.UUID, scope, key string, requestHash, response []byte) error {
	err := db.InOrgTx(ctx, organizationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO idempotency_keys (key, organization_id, scope, request_hash, response_blob, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, now(), now() + make_interval(secs => $6))
			ON CONFLICT (key) DO NOTHING`,
			key, organizationID, scope, requestHash, response, ttl.Seconds())
		return err
	})
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
