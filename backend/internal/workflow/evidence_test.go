package workflow

import (
	"context"
	"strings"
	"testing"
)

func testEvidenceInput() EvidenceInput {
	return EvidenceInput{
		ReleaseID:   "rel-1",
		VersionID:   "ver-1",
		VersionName: "1.4.0",
		ProductCode: "HOSP",
		Tag:         "v1.4.0",
		CommitFrom:  "abc1234",
		CommitTo:    "def5678",
		ItemCount:   12,
		NotesRef:    "content-1:revision-1",
		SubmittedBy: "alice",
		ApproverID:  "bob",
	}
}

// 证据文本必须逐字节稳定：Temporal 重放或重试时文本一变，artifact 侧的
// 载荷指纹就变，同一版本会被封出第二份内容。
func TestRenderEvidenceIsDeterministic(t *testing.T) {
	first := renderEvidence(testEvidenceInput())
	second := renderEvidence(testEvidenceInput())
	if first != second {
		t.Fatal("evidence rendering is not deterministic")
	}
	for _, want := range []string{
		"HOSP 1.4.0 发布证据", "`v1.4.0`", "`abc1234..def5678`",
		"工作项数：12", "`alice`", "`bob`", "四眼校验通过",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("evidence text is missing %q", want)
		}
	}
}

// 任何事实变化都必须改变文本，否则不同发布可能算出同一个指纹。
func TestRenderEvidenceTracksFacts(t *testing.T) {
	base := renderEvidence(testEvidenceInput())
	changed := testEvidenceInput()
	changed.ApproverID = "carol"
	if renderEvidence(changed) == base {
		t.Fatal("approver change did not affect the evidence text")
	}
}

// artifact 未接入时显式降级：evidence_ref 退化为发布说明引用，不报错。
func TestSealEvidenceWithoutArtifactFallsBackToNotesRef(t *testing.T) {
	activities := &Activities{}
	result, err := activities.SealEvidence(context.Background(),
		ReleaseInput{}, ReleaseInfo{NotesRef: "content-1:revision-1"}, ApprovalResult{}, "v1.4.0")
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	if !result.Skipped {
		t.Error("expected skipped result when artifact is not configured")
	}
	if result.Ref != "content-1:revision-1" {
		t.Errorf("ref=%q", result.Ref)
	}
}
