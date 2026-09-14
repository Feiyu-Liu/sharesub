package application

import (
	"context"
	"sort"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

func (s *Service) PlanConcurrency(ctx context.Context, userID, planID string) (domain.PlanConcurrency, error) {
	info, err := s.store.PlanConcurrencyContext(ctx, planID, userID)
	if err != nil {
		return domain.PlanConcurrency{}, err
	}
	now := s.now()
	// 1,440 complete minute boundaries plus the current partial minute.
	start := now.Truncate(time.Minute).Add(-24 * time.Hour)
	values, err := s.store.ConcurrencyMinutes(ctx, planID, info.Plan.AccountID, info.Plan.AccountBindingGeneration, start, now)
	if err != nil {
		return domain.PlanConcurrency{}, err
	}
	live := []domain.ConcurrencyMinute{}
	current := map[string]int{}
	if info.Plan.AccountID != "" {
		live, current = s.traffic.liveHistory(info, now)
	}
	return buildPlanConcurrency(info, now, start, values, live, current), nil
}

func buildPlanConcurrency(info domain.PlanConcurrencyContext, now, start time.Time, stored, live []domain.ConcurrencyMinute, current map[string]int) domain.PlanConcurrency {
	out := domain.PlanConcurrency{AccountID: info.Plan.AccountID, AccountMax: info.AccountMax, Policy: info.Policy, UpdatedAt: now, Points: make([]domain.ConcurrencyPoint, 0, 1441), Members: make([]domain.MemberConcurrencyTrend, 0)}
	type key struct {
		instance string
		bucket   int64
	}
	merged := map[key]domain.ConcurrencyMinute{}
	for _, set := range [][]domain.ConcurrencyMinute{stored, live} {
		for _, v := range set {
			if !v.BucketStart.Before(start) && !v.BucketStart.After(now) {
				merged[key{v.InstanceID, v.BucketStart.Unix()}] = v
			}
		}
	}
	indexes := map[int64]int{}
	for at := start; !at.After(now); at = at.Add(time.Minute) {
		indexes[at.Unix()] = len(out.Points)
		out.Points = append(out.Points, domain.ConcurrencyPoint{BucketStart: at})
	}
	members := map[string]*domain.MemberConcurrencyTrend{}
	addMember := func(user, name string) *domain.MemberConcurrencyTrend {
		if m := members[user]; m != nil {
			return m
		}
		m := &domain.MemberConcurrencyTrend{UserID: user, Username: name, Current: current[user], Average: make([]float64, len(out.Points))}
		members[user] = m
		return m
	}
	for _, v := range merged {
		i := indexes[v.BucketStart.Unix()]
		p := &out.Points[i]
		p.ObservedSeconds += v.ObservedSeconds
		p.Peak = max(p.Peak, v.Peak)
		for _, m := range v.Members {
			addMember(m.UserID, m.Username).Average[i] += m.RequestSeconds
		}
	}
	for _, m := range info.Policy.Members {
		addMember(m.UserID, m.Username).Username = m.Username
	}
	for user, n := range current {
		out.Current += n
		if members[user] == nil {
			addMember(user, user)
		}
	}
	for _, m := range members {
		for i := range m.Average {
			if out.Points[i].ObservedSeconds > 0 {
				m.Average[i] /= out.Points[i].ObservedSeconds
			}
			out.Points[i].Average += m.Average[i]
		}
		out.Members = append(out.Members, *m)
	}
	for _, p := range out.Points {
		out.Peak = max(out.Peak, p.Peak)
	}
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].UserID < out.Members[j].UserID })
	return out
}

func validateConcurrencyPolicy(policy domain.ConcurrencyPolicy) error {
	if len(policy.Members) > 1000 {
		return domain.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, m := range policy.Members {
		if m.UserID == "" || seen[m.UserID] || m.BaseLimit < 0 || m.BaseLimit > 100 || m.MaxConcurrency < 0 || m.MaxConcurrency > 100 || (m.MaxConcurrency > 0 && m.MaxConcurrency < m.BaseLimit) {
			return domain.ErrInvalidInput
		}
		seen[m.UserID] = true
	}
	return nil
}

func (s *Service) UpdatePlanConcurrency(ctx context.Context, userID, planID string, policy domain.ConcurrencyPolicy) error {
	if err := validateConcurrencyPolicy(policy); err != nil {
		return err
	}
	event, err := s.newAuditEvent(userID, "plan.concurrency_updated", "plan", planID, policy)
	if err != nil {
		return err
	}
	return s.store.UpdatePlanConcurrency(ctx, planID, userID, policy, event)
}
func (s *Service) AdminPlanConcurrency(ctx context.Context, admin domain.User, planID string) (domain.PlanConcurrency, error) {
	plan, err := s.adminPlan(ctx, admin, planID)
	if err != nil {
		return domain.PlanConcurrency{}, err
	}
	return s.PlanConcurrency(ctx, plan.OwnerUserID, planID)
}
func (s *Service) AdminUpdatePlanConcurrency(ctx context.Context, admin domain.User, planID string, policy domain.ConcurrencyPolicy) error {
	plan, err := s.adminPlan(ctx, admin, planID)
	if err != nil {
		return err
	}
	// Retain the actual administrator as the audit actor.
	if err := validateConcurrencyPolicy(policy); err != nil {
		return err
	}
	event, err := s.newAuditEvent(admin.ID, "plan.concurrency_updated", "plan", planID, policy)
	if err != nil {
		return err
	}
	return s.store.UpdatePlanConcurrency(ctx, planID, plan.OwnerUserID, policy, event)
}
