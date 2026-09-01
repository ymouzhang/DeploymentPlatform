package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"DP/internal/access"
	"DP/internal/domain"
	"DP/internal/realtime"
)

type communicationPublisher interface {
	Publish(userIDs []string, event realtime.Event)
}

type CommunicationRepository interface {
	CreateCommunication(context.Context, domain.User, domain.CommunicationCreateInput) (domain.Communication, error)
	ListCommunications(context.Context, domain.User, domain.CommunicationFilter) ([]domain.Communication, error)
	GetCommunication(context.Context, domain.User, string) (domain.Communication, error)
	MarkCommunicationRead(context.Context, domain.User, string) (domain.Communication, error)
	CommunicationSummary(context.Context, domain.User) (domain.CommunicationSummary, error)
	SendCommunicationMessage(context.Context, domain.User, string, string) (domain.CommunicationMessage, error)
	ChangeCommunicationState(context.Context, domain.User, string, bool, string) (domain.Communication, error)
	ListUsers(context.Context) ([]domain.User, error)
}

type CommunicationService struct {
	store  CommunicationRepository
	events communicationPublisher
}

func NewCommunicationService(db CommunicationRepository, events communicationPublisher) *CommunicationService {
	return &CommunicationService{store: db, events: events}
}

func (s *CommunicationService) Create(ctx context.Context, actor domain.User, input domain.CommunicationCreateInput) (domain.Communication, error) {
	if scope, ok := actor.Permissions.Scope(access.CommunicationCreate); !ok || scope != access.ScopeAll {
		return domain.Communication{}, domain.ErrForbidden
	}
	input.TargetUserID = strings.TrimSpace(input.TargetUserID)
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if input.TargetUserID == "" {
		return domain.Communication{}, domain.FieldError("target_user_id", "请选择目标协作账号")
	}
	if length := utf8.RuneCountInString(input.Title); length < 1 || length > 100 {
		return domain.Communication{}, domain.FieldError("title", "标题长度必须为 1–100 个字符")
	}
	if err := validateCommunicationContent(input.Content, false); err != nil {
		return domain.Communication{}, err
	}
	if len(input.Resources) > 50 {
		return domain.Communication{}, domain.FieldError("resources", "单个事项最多关联 50 个资源")
	}
	for index := range input.Resources {
		input.Resources[index].ResourceType = strings.ToLower(strings.TrimSpace(input.Resources[index].ResourceType))
		input.Resources[index].ResourceID = strings.TrimSpace(input.Resources[index].ResourceID)
		input.Resources[index].ResourceKey = strings.ToLower(strings.TrimSpace(input.Resources[index].ResourceKey))
	}
	item, err := s.store.CreateCommunication(ctx, actor, input)
	if err == nil {
		s.publish(ctx, item.ID, realtime.ChangeCreated, item.TargetUserID)
	}
	return item, err
}

func (s *CommunicationService) List(ctx context.Context, actor domain.User, filter domain.CommunicationFilter) ([]domain.Communication, error) {
	scope, ok := actor.Permissions.Scope(access.CommunicationRead)
	if !ok {
		return nil, domain.ErrForbidden
	}
	if scope != access.ScopeAll {
		filter.TargetUserID = actor.ID
	}
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	return s.store.ListCommunications(ctx, actor, filter)
}

func (s *CommunicationService) Get(ctx context.Context, actor domain.User, id string) (domain.Communication, error) {
	if _, ok := actor.Permissions.Scope(access.CommunicationRead); !ok {
		return domain.Communication{}, domain.ErrForbidden
	}
	return s.store.GetCommunication(ctx, actor, id)
}

func (s *CommunicationService) MarkRead(ctx context.Context, actor domain.User, id string) (domain.Communication, error) {
	if _, ok := actor.Permissions.Scope(access.CommunicationReply); !ok {
		return domain.Communication{}, domain.ErrForbidden
	}
	item, err := s.store.MarkCommunicationRead(ctx, actor, id)
	if err == nil {
		s.publish(ctx, item.ID, realtime.ChangeRead, actor.ID)
	}
	return item, err
}

func (s *CommunicationService) Summary(ctx context.Context, actor domain.User) (domain.CommunicationSummary, error) {
	if _, ok := actor.Permissions.Scope(access.CommunicationRead); !ok {
		return domain.CommunicationSummary{}, domain.ErrForbidden
	}
	return s.store.CommunicationSummary(ctx, actor)
}

func (s *CommunicationService) Send(ctx context.Context, actor domain.User, id, content string) (domain.CommunicationMessage, error) {
	scope, ok := actor.Permissions.Scope(access.CommunicationReply)
	if !ok {
		return domain.CommunicationMessage{}, domain.ErrForbidden
	}
	content = strings.TrimSpace(content)
	if err := validateCommunicationContent(content, false); err != nil {
		return domain.CommunicationMessage{}, err
	}
	targetUserID := actor.ID
	if scope == access.ScopeAll {
		item, err := s.store.GetCommunication(ctx, actor, id)
		if err != nil {
			return domain.CommunicationMessage{}, err
		}
		targetUserID = item.TargetUserID
	}
	item, err := s.store.SendCommunicationMessage(ctx, actor, id, content)
	if err == nil {
		s.publish(ctx, id, realtime.ChangeMessage, targetUserID)
	}
	return item, err
}

func (s *CommunicationService) ChangeState(ctx context.Context, actor domain.User, id string, reopen bool, content string) (domain.Communication, error) {
	if scope, ok := actor.Permissions.Scope(access.CommunicationManage); !ok || scope != access.ScopeAll {
		return domain.Communication{}, domain.ErrForbidden
	}
	content = strings.TrimSpace(content)
	if err := validateCommunicationContent(content, true); err != nil {
		return domain.Communication{}, err
	}
	item, err := s.store.ChangeCommunicationState(ctx, actor, id, reopen, content)
	if err == nil {
		change := realtime.ChangeClosed
		if reopen {
			change = realtime.ChangeReopened
		}
		s.publish(ctx, item.ID, change, item.TargetUserID)
	}
	return item, err
}

func (s *CommunicationService) publish(ctx context.Context, threadID, change string, affectedUserIDs ...string) {
	if s.events == nil {
		return
	}
	userIDs := append(make([]string, 0, len(affectedUserIDs)+4), affectedUserIDs...)
	users, err := s.store.ListUsers(ctx)
	if err == nil {
		for _, user := range users {
			if scope, ok := user.Permissions.Scope(access.CommunicationRead); ok && scope == access.ScopeAll && user.Enabled {
				userIDs = append(userIDs, user.ID)
			}
		}
	}
	s.events.Publish(userIDs, realtime.NewEvent(realtime.CommunicationChanged, threadID, change))
}

func validateCommunicationContent(value string, optional bool) error {
	length := utf8.RuneCountInString(value)
	if optional && length == 0 {
		return nil
	}
	if length < 1 || length > 5000 {
		return domain.FieldError("content", "消息长度必须为 1–5000 个字符")
	}
	return nil
}
