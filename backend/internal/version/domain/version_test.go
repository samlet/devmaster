package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newVersion(t *testing.T) Version {
	t.Helper()
	version, err := NewVersion(NewVersionInput{
		OrganizationID: uuid.New(),
		ProductID:      uuid.New(),
		Name:           "1.0.0",
	})
	if err != nil {
		t.Fatalf("NewVersion 报错：%v", err)
	}
	return version
}

func TestVersionNameValidation(t *testing.T) {
	for _, name := range []string{"1.0", "1.0.0", "2.4.1-rc.1"} {
		if _, err := NewVersion(NewVersionInput{
			OrganizationID: uuid.New(), ProductID: uuid.New(), Name: name,
		}); err != nil {
			t.Fatalf("%s 应当合法：%v", name, err)
		}
	}
	for _, name := range []string{"", "v1.0.0", "1", "1.0.0.0.0", "release-1"} {
		if _, err := NewVersion(NewVersionInput{
			OrganizationID: uuid.New(), ProductID: uuid.New(), Name: name,
		}); err == nil {
			t.Fatalf("%s 应当被拒绝", name)
		}
	}
}

func TestFreezeBlocksScopeChanges(t *testing.T) {
	version := newVersion(t)
	if err := version.EnsureScopeMutable(); err != nil {
		t.Fatalf("未冻结时应当可改：%v", err)
	}
	if err := version.Freeze(); err != nil {
		t.Fatalf("Freeze 报错：%v", err)
	}
	if err := version.EnsureScopeMutable(); err == nil {
		t.Fatal("冻结后应当拒绝范围变更")
	}
	if err := version.Freeze(); err == nil {
		t.Fatal("重复冻结应当被拒绝")
	}
}

func TestReleaseLifecycle(t *testing.T) {
	version := newVersion(t)
	if err := version.MarkReleased(time.Now().UTC()); err != nil {
		t.Fatalf("MarkReleased 报错：%v", err)
	}
	if version.Status != StatusReleased || version.ReleasedAt == nil {
		t.Fatalf("发布后状态不对：%+v", version)
	}
	if err := version.MarkReleased(time.Now().UTC()); err == nil {
		t.Fatal("重复发布应当被拒绝")
	}
	if _, err := version.ApplyUpdate(UpdateFields{Name: ptr("1.0.1")}); err == nil {
		t.Fatal("已发布版本不应允许改名")
	}
}

func TestReleaseRecordFourEyes(t *testing.T) {
	release, err := NewReleaseRecord(NewReleaseInput{
		OrganizationID: uuid.New(),
		VersionID:      uuid.New(),
		CommitTo:       ptr("main"),
		SubmittedBy:    "alice",
	})
	if err != nil {
		t.Fatalf("NewReleaseRecord 报错：%v", err)
	}
	if err := release.Approve("alice"); err == nil {
		t.Fatal("自审批应当被四眼校验拒绝")
	}
	if err := release.Approve("bob"); err != nil {
		t.Fatalf("他人审批应当通过：%v", err)
	}
	if release.Status != ReleaseApproved || release.ApproverID == nil || *release.ApproverID != "bob" {
		t.Fatalf("审批后状态不对：%+v", release)
	}
	if err := release.MarkTagged("v1.0.0"); err != nil {
		t.Fatalf("MarkTagged 报错：%v", err)
	}
	if err := release.Publish("bob", time.Now().UTC()); err != nil {
		t.Fatalf("Publish 报错：%v", err)
	}
	if release.Status != ReleasePublished {
		t.Fatalf("status = %s，want PUBLISHED", release.Status)
	}
	if err := release.Fail("late failure"); err == nil {
		t.Fatal("已发布记录不应能标记失败")
	}
}

func TestReleaseRequiresCommit(t *testing.T) {
	if _, err := NewReleaseRecord(NewReleaseInput{
		OrganizationID: uuid.New(), VersionID: uuid.New(), SubmittedBy: "alice",
	}); err == nil {
		t.Fatal("没有 commit_to 应当被拒绝")
	}
}

func ptr(value string) *string { return &value }
