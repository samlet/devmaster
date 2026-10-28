// SealEvidence：把一次发布的证据封进 artifact。
//
// 证据文本刻意不含时间戳：Temporal 重试或工作流重放时，文本必须逐字节相同，
// 否则 artifact 侧载荷指纹会变，同一版本会封出第二份内容。要时间就去看
// artifact 的 revision.created_at —— 那才是事实发生的时间。
package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/artifact"
)

// EvidenceInput 是一次发布存证的全部事实。字段全部来自已落库的发布记录与
// 审批结果，不接受外部传入的自由文本。
type EvidenceInput struct {
	ReleaseID   string
	VersionID   string
	VersionName string
	ProductCode string
	Tag         string
	CommitFrom  string
	CommitTo    string
	ItemCount   int
	NotesRef    string
	SubmittedBy string
	ApproverID  string
}

// EvidenceResult 是存证回执。Ref 写入 release_records.evidence_ref。
type EvidenceResult struct {
	// Ref 形如 content_id:revision_id，固定版本引用，不存预签名 URL。
	Ref           string `json:"ref"`
	ContentID     string `json:"content_id"`
	RevisionID    string `json:"revision_id"`
	PayloadSHA256 string `json:"payload_sha256"`
	ManifestHash  string `json:"manifest_hash"`
	// Skipped=true 表示 artifact 未接入（本地开发），Ref 退化为发布说明引用。
	Skipped bool `json:"skipped"`
}

// SealEvidence 生成证据文本并封成 artifact 的不可变修订，再回读校验字节。
//
// 失败即失败：存证不成功时发布不应继续（文档 §7.2 的顺序就是先存证再置
// PUBLISHED）。只有在 artifact 整体未接入时才显式降级。
func (a *Activities) SealEvidence(ctx context.Context, input ReleaseInput, info ReleaseInfo, approval ApprovalResult, tag string) (EvidenceResult, error) {
	if a.Artifact == nil {
		return EvidenceResult{Ref: info.NotesRef, Skipped: true}, nil
	}
	evidence := EvidenceInput{
		ReleaseID:   input.ReleaseID.String(),
		VersionID:   input.VersionID.String(),
		VersionName: info.VersionName,
		ProductCode: info.ProductCode,
		Tag:         tag,
		CommitFrom:  info.CommitFrom,
		CommitTo:    info.CommitTo,
		ItemCount:   info.ItemCount,
		NotesRef:    info.NotesRef,
		SubmittedBy: input.SubmittedBy,
		ApproverID:  approval.ApproverID,
	}
	sealed, err := a.Artifact.SealEvidence(ctx, artifact.Evidence{
		ContentType: a.Artifact.EvidenceType(),
		Title:       evidenceTitle(evidence),
		Metadata: map[string]any{
			"release_id":   evidence.ReleaseID,
			"version_id":   evidence.VersionID,
			"version":      evidence.VersionName,
			"product_code": evidence.ProductCode,
			"tag":          evidence.Tag,
			"commit_from":  evidence.CommitFrom,
			"commit_to":    evidence.CommitTo,
			"item_count":   evidence.ItemCount,
			"notes_ref":    evidence.NotesRef,
			"submitted_by": evidence.SubmittedBy,
			"approver_id":  evidence.ApproverID,
		},
		Role: "evidence",
		Text: renderEvidence(evidence),
	})
	if err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{
		Ref:           sealed.Ref,
		ContentID:     sealed.ContentID,
		RevisionID:    sealed.RevisionID,
		PayloadSHA256: sealed.PayloadSHA256,
		ManifestHash:  sealed.ManifestHash,
	}, nil
}

func evidenceTitle(evidence EvidenceInput) string {
	return fmt.Sprintf("%s %s 发布证据", evidence.ProductCode, evidence.VersionName)
}

// renderEvidence 输出人可读的 Markdown。顺序与字段固定，不用 map 遍历——
// 否则每次生成的字节不同，幂等指纹就失效了。
func renderEvidence(evidence EvidenceInput) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", evidenceTitle(evidence))
	fmt.Fprintf(&builder, "- 版本 ID：`%s`\n", evidence.VersionID)
	fmt.Fprintf(&builder, "- 发布记录：`%s`\n", evidence.ReleaseID)
	fmt.Fprintf(&builder, "- 产品：`%s`\n", evidence.ProductCode)
	fmt.Fprintf(&builder, "- Git tag：`%s`\n", evidence.Tag)
	fmt.Fprintf(&builder, "- 提交范围：`%s..%s`\n", evidence.CommitFrom, evidence.CommitTo)
	fmt.Fprintf(&builder, "- 工作项数：%d\n", evidence.ItemCount)
	fmt.Fprintf(&builder, "- 发布说明：`%s`\n", evidence.NotesRef)
	fmt.Fprintf(&builder, "- 提交人：`%s`\n", evidence.SubmittedBy)
	fmt.Fprintf(&builder, "- 审批人：`%s`\n", evidence.ApproverID)
	builder.WriteString("\n## 审批结论\n\n")
	builder.WriteString("APPROVED（四眼校验通过：审批人 ≠ 提交人）。\n")
	return builder.String()
}
