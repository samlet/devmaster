// Package apperr 是领域层与传输层之间的错误词汇。
//
// 领域层不 import gRPC：错误用 Kind 表达语义，由传输层翻译成 status code。
// 这样领域测试不需要起 gRPC，也不会因为换协议而改业务代码。
package apperr

import "fmt"

type Kind string

const (
	KindInvalid            Kind = "INVALID"
	KindNotFound           Kind = "NOT_FOUND"
	KindConflict           Kind = "CONFLICT"
	KindFailedPrecondition Kind = "FAILED_PRECONDITION"
)

type Error struct {
	Kind    Kind
	Field   string
	Message string
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Message, e.Field)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func Invalid(field, message string) error {
	return &Error{Kind: KindInvalid, Field: field, Message: message}
}

func NotFound(message string) error {
	return &Error{Kind: KindNotFound, Message: message}
}

func Conflict(field, message string) error {
	return &Error{Kind: KindConflict, Field: field, Message: message}
}

func FailedPrecondition(field, message string) error {
	return &Error{Kind: KindFailedPrecondition, Field: field, Message: message}
}

// KindOf 取出错误语义；非本包错误返回 false。
func KindOf(err error) (Kind, bool) {
	if typed, ok := err.(*Error); ok {
		return typed.Kind, true
	}
	return "", false
}
