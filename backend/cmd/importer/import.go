package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/xiaofeiwu/devmaster/backend/internal/platform/apperr"
	"github.com/xiaofeiwu/devmaster/backend/internal/platform/database"
	productpostgres "github.com/xiaofeiwu/devmaster/backend/internal/product/adapter/postgres"
	productapplication "github.com/xiaofeiwu/devmaster/backend/internal/product/application"
	productdomain "github.com/xiaofeiwu/devmaster/backend/internal/product/domain"
	workitempostgres "github.com/xiaofeiwu/devmaster/backend/internal/workitem/adapter/postgres"
	workitemapplication "github.com/xiaofeiwu/devmaster/backend/internal/workitem/application"
	workitemdomain "github.com/xiaofeiwu/devmaster/backend/internal/workitem/domain"
)

type Importer struct {
	db         *database.DB
	org        uuid.UUID
	products   *productpostgres.Repository
	productSvc *productapplication.Service
	workRepo   *workitempostgres.Repository
	workSvc    *workitemapplication.Service
	workActor  workitemapplication.Actor
	productAct productapplication.Actor
}

type Summary struct {
	ProductsCreated  int
	ProductsExisting int
	ItemsCreated     int
	ItemsExisting    int
}

func NewImporter(db *database.DB, organizationID uuid.UUID) *Importer {
	products := productpostgres.NewRepository(db)
	workRepo := workitempostgres.NewRepository(db)
	return &Importer{
		db:         db,
		org:        organizationID,
		products:   products,
		productSvc: productapplication.NewService(products),
		workRepo:   workRepo,
		workSvc:    workitemapplication.NewService(workRepo),
		workActor: workitemapplication.Actor{
			Subject:        "importer",
			OrganizationID: organizationID,
			Roles:          []string{"DEV_MEMBER", "DEV_LEAD"},
		},
		productAct: productapplication.Actor{Subject: "importer", OrganizationID: organizationID},
	}
}

func (i *Importer) Run(ctx context.Context, dataset *Dataset) (Summary, error) {
	var summary Summary
	for _, row := range dataset.Products {
		existing, err := i.products.GetByKey(ctx, i.org, row.Key)
		if err == nil {
			_ = existing
			summary.ProductsExisting++
			continue
		}
		if kind, ok := apperr.KindOf(err); !ok || kind != apperr.KindNotFound {
			return summary, fmt.Errorf("查询产品 %s: %w", row.Key, err)
		}
		_, err = i.productSvc.Create(ctx, i.productAct, productapplication.CreateCommand{
			Key:         row.Key,
			Name:        row.Name,
			Description: row.Description,
		})
		if err != nil {
			return summary, fmt.Errorf("创建产品 %s: %w", row.Key, err)
		}
		summary.ProductsCreated++
	}

	for _, row := range dataset.WorkItems {
		existing, found, err := i.workRepo.FindByExternalRef(ctx, i.org, row.ExternalRef)
		if err != nil {
			return summary, fmt.Errorf("查询工作项 %s: %w", row.ExternalRef, err)
		}
		if found {
			_ = existing
			summary.ItemsExisting++
			continue
		}
		if err := i.importWorkItem(ctx, row); err != nil {
			return summary, err
		}
		summary.ItemsCreated++
	}
	return summary, nil
}

func (i *Importer) importWorkItem(ctx context.Context, row WorkItemRow) error {
	product, err := i.products.GetByKey(ctx, i.org, row.ProductKey)
	if err != nil {
		return fmt.Errorf("工作项 %s 的产品 %s 不存在: %w", row.ExternalRef, row.ProductKey, err)
	}
	itemType, err := workitemdomain.ParseType(row.Type)
	if err != nil {
		return fmt.Errorf("工作项 %s: %w", row.ExternalRef, err)
	}
	priority := workitemdomain.PriorityP2
	if row.Priority != "" {
		priority, err = parsePriorityLabel(row.Priority)
		if err != nil {
			return fmt.Errorf("工作项 %s: %w", row.ExternalRef, err)
		}
	}
	var severity *workitemdomain.Severity
	if row.Severity != "" {
		value, parseErr := workitemdomain.ParseSeverity(row.Severity)
		if parseErr != nil {
			return fmt.Errorf("工作项 %s: %w", row.ExternalRef, parseErr)
		}
		severity = &value
	}
	var assignee *string
	if row.AssigneeID != "" {
		value := row.AssigneeID
		assignee = &value
	}
	var externalRef *string
	if row.ExternalRef != "" {
		value := row.ExternalRef
		externalRef = &value
	}
	var storyPoints *int16
	if row.StoryPoints != "" {
		parsed, parseErr := strconv.Atoi(row.StoryPoints)
		if parseErr != nil || parsed < 0 || parsed > 100 {
			return fmt.Errorf("工作项 %s: story_points 必须是 0-100 的整数", row.ExternalRef)
		}
		value := int16(parsed)
		storyPoints = &value
	}

	item, err := i.workSvc.Create(ctx, i.workActor, workitemapplication.CreateCommand{
		ProductID:     product.ID,
		Type:          itemType,
		Title:         row.Title,
		DescriptionMD: row.Description,
		Priority:      priority,
		Severity:      severity,
		AssigneeID:    assignee,
		ExternalRef:   externalRef,
		StoryPoints:   storyPoints,
	})
	if err != nil {
		return fmt.Errorf("创建工作项 %s: %w", row.ExternalRef, err)
	}
	if row.Status == "" {
		return nil
	}
	target, err := workitemdomain.ParseStatus(row.Status)
	if err != nil {
		return fmt.Errorf("工作项 %s: %w", row.ExternalRef, err)
	}
	if target == item.Status {
		return nil
	}
	path, err := transitionPath(itemType, item.Status, target)
	if err != nil {
		return fmt.Errorf("工作项 %s: %w", row.ExternalRef, err)
	}
	for _, step := range path {
		command := workitemapplication.TransitionCommand{
			WorkItemID:      item.ID,
			To:              step,
			ExpectedVersion: item.Version,
		}
		if step == workitemdomain.StatusResolved || step == workitemdomain.StatusClosed {
			resolution := workitemdomain.ResolutionFixed
			command.Resolution = &resolution
		}
		if step == workitemdomain.StatusCancelled {
			command.Comment = "导入器按数据包取消"
		}
		item, err = i.workSvc.Transition(ctx, i.workActor, command)
		if err != nil {
			return fmt.Errorf("工作项 %s 迁移到 %s: %w", row.ExternalRef, step, err)
		}
	}
	return nil
}

// transitionPath 用 BFS 找一条从 from 到 to 的合法路径。
// 不手写 if-else：状态机改了之后，手写路径会静默过期。
func transitionPath(itemType workitemdomain.Type, from, to workitemdomain.Status) ([]workitemdomain.Status, error) {
	if from == to {
		return nil, nil
	}
	type node struct {
		status workitemdomain.Status
		path   []workitemdomain.Status
	}
	queue := []node{{status: from}}
	visited := map[workitemdomain.Status]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range workitemdomain.AllowedTransitions(itemType, current.status) {
			if visited[next] {
				continue
			}
			path := append(append([]workitemdomain.Status{}, current.path...), next)
			if next == to {
				return path, nil
			}
			visited[next] = true
			queue = append(queue, node{status: next, path: path})
		}
	}
	return nil, apperr.FailedPrecondition("status", fmt.Sprintf(
		"%s 无法从 %s 迁移到 %s", itemType, from, to))
}

func parsePriorityLabel(value string) (workitemdomain.Priority, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) == 2 && trimmed[0] == 'P' {
		number := int(trimmed[1] - '0')
		return workitemdomain.ParsePriority(int16(number))
	}
	return 0, apperr.Invalid("priority", fmt.Sprintf("priority %q must look like P0..P3", value))
}

// Verify 对照 expectations.json 检查导入结果。
func (i *Importer) Verify(ctx context.Context, dataset *Dataset) error {
	products, productTotal, err := i.products.List(ctx, i.org, productapplication.ListFilter{Limit: 200})
	if err != nil {
		return fmt.Errorf("读取产品失败：%w", err)
	}
	if expected := dataset.Expectations.ProductCount; expected > 0 && productTotal != expected {
		return fmt.Errorf("产品数 = %d，期望 %d", productTotal, expected)
	}
	productByKey := map[string]productdomain.Product{}
	for _, product := range products {
		productByKey[product.Key] = product
	}
	for _, expectation := range dataset.Expectations.Products {
		product, ok := productByKey[expectation.Key]
		if !ok {
			return fmt.Errorf("期望的产品 %s 不存在", expectation.Key)
		}
		if expectation.Status != "" && string(product.Status) != expectation.Status {
			return fmt.Errorf("产品 %s 状态 = %s，期望 %s", expectation.Key, product.Status, expectation.Status)
		}
	}

	items, itemTotal, err := i.workSvc.List(ctx, i.workActor, workitemapplication.ListFilter{Limit: 200})
	if err != nil {
		return fmt.Errorf("读取工作项失败：%w", err)
	}
	if expected := dataset.Expectations.WorkItemCount; expected > 0 && itemTotal != expected {
		return fmt.Errorf("工作项数 = %d，期望 %d", itemTotal, expected)
	}
	byRef := map[string]workitemdomain.WorkItem{}
	for _, item := range items {
		if item.ExternalRef != nil {
			byRef[*item.ExternalRef] = item
		}
	}
	for _, expectation := range dataset.Expectations.WorkItems {
		item, ok := byRef[expectation.ExternalRef]
		if !ok {
			return fmt.Errorf("期望的工作项 %s 不存在", expectation.ExternalRef)
		}
		if expectation.Status != "" && string(item.Status) != expectation.Status {
			return fmt.Errorf("工作项 %s 状态 = %s，期望 %s",
				expectation.ExternalRef, item.Status, expectation.Status)
		}
	}
	return nil
}
