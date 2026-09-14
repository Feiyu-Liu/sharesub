package application

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

func concurrencyCredential(user string) domain.GatewayCredential {
	return domain.GatewayCredential{Account: domain.Account{ID: "account", MaxConcurrency: 12}, Plan: domain.Plan{ID: "plan"}, AccountBindingGeneration: 1, Member: domain.Member{UserID: user, Username: user}, ConcurrencyPolicy: domain.ConcurrencyPolicy{Enabled: true, Members: []domain.MemberConcurrencyLimit{{UserID: "a", BaseLimit: 2, MaxConcurrency: 6}, {UserID: "b", BaseLimit: 2, MaxConcurrency: 10}, {UserID: "c", BaseLimit: 2, MaxConcurrency: 10}}}}
}
func TestMemberConcurrencyReservedSlotsSharedPoolAndHardCap(t *testing.T) {
	c := newAccountTrafficController()
	now := time.Now()
	take := func(user string) func() {
		t.Helper()
		commit, release, err := c.prepareGateway(concurrencyCredential(user), now)
		if err != nil {
			t.Fatal(err)
		}
		commit()
		return release
	}
	releases := []func(){}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	for range 6 {
		releases = append(releases, take("a"))
	}
	if _, _, err := c.prepareGateway(concurrencyCredential("a"), now); !errors.Is(err, domain.ErrMemberConcurrency) {
		t.Fatalf("hard cap: %v", err)
	}
	// B uses two reserved slots and the two remaining shared slots.
	for range 4 {
		releases = append(releases, take("b"))
	}
	if _, _, err := c.prepareGateway(concurrencyCredential("b"), now); !errors.Is(err, domain.ErrMemberConcurrency) {
		t.Fatalf("borrowed C's base: %v", err)
	}
	for range 2 {
		releases = append(releases, take("c"))
	}
	if _, _, err := c.prepareGateway(concurrencyCredential("c"), now); !errors.Is(err, domain.ErrAccountConcurrency) {
		t.Fatalf("account ceiling: %v", err)
	}
	releases[0]()
	releases[0]()
	releases = append(releases, take("b")) // Released shared slot is reusable exactly once.
}
func TestMemberConcurrencyParallelKeysCannotBypassLimit(t *testing.T) {
	c := newAccountTrafficController()
	now := time.Now()
	var admitted atomic.Int64
	var wg sync.WaitGroup
	gate := make(chan struct{})
	done := make(chan func(), 100)
	for i := range 100 {
		wg.Add(1)
		go func(key int) {
			defer wg.Done()
			<-gate
			v := concurrencyCredential("a")
			v.APIKeyID = string(rune(key))
			commit, release, err := c.prepareGateway(v, now)
			if err == nil {
				commit()
				admitted.Add(1)
				done <- release
			} else if !errors.Is(err, domain.ErrMemberConcurrency) {
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	close(gate)
	wg.Wait()
	close(done)
	if admitted.Load() != 6 {
		t.Fatalf("admitted %d", admitted.Load())
	}
	for release := range done {
		release()
	}
	if c.states["account"].activeRequests != 0 || len(c.states["account"].activeMembers) != 0 {
		t.Fatal("capacity leaked")
	}
}
func TestMemberConcurrencyPolicyChangesAndFailedPreflight(t *testing.T) {
	c := newAccountTrafficController()
	now := time.Now()
	v := concurrencyCredential("a")
	v.ConcurrencyPolicy.Enabled = false
	v.Account.RPMLimit = 1
	_, release, err := c.prepareGateway(v, now)
	if err != nil {
		t.Fatal(err)
	}
	release()
	commit, release, err := c.prepareGateway(v, now)
	if err != nil {
		t.Fatalf("preflight consumed RPM or member capacity: %v", err)
	}
	commit()
	release()
	v.Account.RPMLimit = 0
	v.ConcurrencyPolicy.Enabled = true
	v.Account.MaxConcurrency = 5
	if _, _, err := c.prepareGateway(v, now); !errors.Is(err, domain.ErrConcurrencyConfiguration) {
		t.Fatalf("invalid reservation: %v", err)
	}
	v.Account.MaxConcurrency = 12
	v.ConcurrencyPolicy.Members = v.ConcurrencyPolicy.Members[1:]
	if _, _, err := c.prepareGateway(v, now); !errors.Is(err, domain.ErrAccountUnavailable) {
		t.Fatalf("removed user admitted: %v", err)
	}
}
func TestConcurrencyHistoryIntegratesSpikesAcrossMinuteAndDeduplicatesFlush(t *testing.T) {
	c := newAccountTrafficController()
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	now := start
	c.now = func() time.Time { return now }
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}, AccountMax: 12, Policy: concurrencyCredential("a").ConcurrencyPolicy}
	c.liveHistory(info, now)
	now = start.Add(10 * time.Second)
	_, a, err := c.prepareGateway(concurrencyCredential("a"), now)
	if err != nil {
		t.Fatal(err)
	}
	now = start.Add(20 * time.Second)
	_, b, err := c.prepareGateway(concurrencyCredential("b"), now)
	if err != nil {
		t.Fatal(err)
	}
	now = start.Add(30 * time.Second)
	a()
	// B crosses the minute boundary and survives a history flush.
	now = start.Add(70 * time.Second)
	pending := c.pendingMinutes(now)
	c.acknowledgeMinutes(pending)
	if n := len(c.pendingMinutes(now)); n != 0 {
		t.Fatalf("unchanged buckets rewritten: %d", n)
	}
	now = start.Add(80 * time.Second)
	b()
	now = start.Add(120 * time.Second)
	live, current := c.liveHistory(info, now)
	stored := make([]domain.ConcurrencyMinute, len(pending))
	for i, v := range pending {
		stored[i] = v.value
	}
	out := buildPlanConcurrency(info, now, start, stored, live, current)
	if out.Current != 0 || out.Peak != 2 {
		t.Fatalf("counts: %+v", out)
	}
	// First minute: A=20 seconds, B=40 seconds => average total 1.
	if math.Abs(out.Points[0].Average-1) > 1e-9 || math.Abs(out.Points[1].Average-1.0/3) > 1e-9 {
		t.Fatalf("averages: %+v", out.Points)
	}
	for i, p := range out.Points {
		sum := 0.0
		for _, m := range out.Members {
			sum += m.Average[i]
		}
		if math.Abs(sum-p.Average) > 1e-9 {
			t.Fatal("stack does not equal total")
		}
	}
}
func TestConcurrencyHistorySeparatePeaksMustNotBeSummed(t *testing.T) {
	c := newAccountTrafficController()
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	now := start
	c.now = func() time.Time { return now }
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}, Policy: concurrencyCredential("a").ConcurrencyPolicy}
	c.liveHistory(info, now)
	_, a, _ := c.prepareGateway(concurrencyCredential("a"), now)
	now = start.Add(10 * time.Second)
	a()
	now = start.Add(20 * time.Second)
	_, b, _ := c.prepareGateway(concurrencyCredential("b"), now)
	now = start.Add(30 * time.Second)
	b()
	now = start.Add(time.Minute)
	live, current := c.liveHistory(info, now)
	out := buildPlanConcurrency(info, now, start, nil, live, current)
	if out.Peak != 1 {
		t.Fatalf("peak fabricated: %d", out.Peak)
	}
}

type concurrencyHistoryStore struct {
	Store
	values []domain.ConcurrencyMinute
	fail   bool
}

func (s *concurrencyHistoryStore) SaveConcurrencyMinutes(_ context.Context, v []domain.ConcurrencyMinute) error {
	if s.fail {
		return errors.New("offline")
	}
	s.values = v
	return nil
}
func TestConcurrencyHistoryRetriesFailedPersistence(t *testing.T) {
	now := time.Now()
	c := newAccountTrafficController()
	c.now = func() time.Time { return now }
	_, release, _ := c.prepareGateway(concurrencyCredential("a"), now)
	now = now.Add(time.Second)
	release()
	store := &concurrencyHistoryStore{fail: true}
	s := &Service{traffic: c, store: store, now: func() time.Time { return now }}
	if err := s.FlushConcurrencyHistory(context.Background()); err == nil {
		t.Fatal("failure hidden")
	}
	store.fail = false
	if err := s.FlushConcurrencyHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.values) == 0 || len(c.pendingMinutes(now)) != 0 {
		t.Fatal("failed snapshot lost or not acknowledged")
	}
}

func TestResolveAndReacquireRespectMemberConcurrency(t *testing.T) {
	now := time.Now()
	manager := testSecurityManager(t)
	first := testCredential(t, manager, "key", "member-a", "plan-a", 1, 5000, 0, now)
	first.Account.MaxConcurrency = 12
	first.ConcurrencyPolicy = domain.ConcurrencyPolicy{Enabled: true, Members: []domain.MemberConcurrencyLimit{{UserID: first.Member.UserID, BaseLimit: 1, MaxConcurrency: 1}}}
	second := testCredential(t, manager, "key", "member-b", "plan-b", 2, 5000, 0, now)
	store := &gatewayStore{routes: domain.GatewayRouteSet{APIKey: domain.APIKey{ID: "key", Strategy: domain.RoutePriority}, Candidates: []domain.GatewayCredential{first, second}}, exhausted: map[string]bool{}}
	service := NewService(store, manager, nil, time.Hour, "", "")
	active, err := service.ResolveGatewayAccess(context.Background(), "sk-sharesub-test")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Release()
	// A new request can choose another route; a pinned WS turn must reject.
	next, err := service.ResolveGatewayAccess(context.Background(), "sk-sharesub-test")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	if next.Credential.Plan.ID != second.Plan.ID {
		t.Fatal("did not route around member saturation")
	}
	if _, err := service.ReacquireGatewayAccess(context.Background(), "sk-sharesub-test", active); !errors.Is(err, domain.ErrMemberConcurrency) {
		t.Fatalf("pinned turn bypassed member limit: %v", err)
	}
	active.Release()
	resumed, err := service.ReacquireGatewayAccess(context.Background(), "sk-sharesub-test", active)
	if err != nil {
		t.Fatal(err)
	}
	resumed.Release()
}

func TestConcurrencyCollectorRecreationDoesNotOverwritePersistedMinute(t *testing.T) {
	c := newAccountTrafficController()
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}}
	c.liveHistory(info, start)
	now := start.Add(25*time.Hour + 10*time.Second)
	pending := c.pendingMinutes(now)
	c.acknowledgeMinutes(pending) // Expire only after the final snapshot is saved.
	c.liveHistory(info, now.Add(time.Second))
	live, _ := c.liveHistory(info, now.Add(2*time.Second))
	stored := make([]domain.ConcurrencyMinute, len(pending))
	for i, p := range pending {
		stored[i] = p.value
	}
	c.acknowledgeMinutes(pending)
	if len(c.pendingMinutes(now.Add(2*time.Second))) == 0 {
		t.Fatal("old acknowledgement marked new collector saved")
	}
	out := buildPlanConcurrency(info, now.Add(2*time.Second), now.Truncate(time.Minute), stored, live, nil)
	if math.Abs(out.Points[0].ObservedSeconds-11) > 1e-9 {
		t.Fatalf("recreated collector overwrote elapsed history: %+v", out.Points[0])
	}
}

func TestConcurrencyHistoryExpiredCollectorRetriesFailedPersistence(t *testing.T) {
	c := newAccountTrafficController()
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}}
	c.liveHistory(info, now)
	store := &concurrencyHistoryStore{}
	s := &Service{traffic: c, store: store, now: func() time.Time { return now }}
	now = now.Add(25 * time.Hour)
	if err := s.FlushConcurrencyHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.values = nil
	store.fail = true
	now = now.Add(5 * time.Second)
	if err := s.FlushConcurrencyHistory(context.Background()); err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(c.history) != 1 {
		t.Fatal("failed final snapshot lost its collector")
	}
	store.fail = false
	if err := s.FlushConcurrencyHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 1 || store.values[0].ObservedSeconds != 5 {
		t.Fatalf("final snapshot not retried: %+v", store.values)
	}
	if len(c.history) != 0 {
		t.Fatal("saved idle collector was not reclaimed")
	}
}

func TestConcurrencyHistorySnapshotReuseAndIsolation(t *testing.T) {
	c := newAccountTrafficController()
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	now := start
	c.now = func() time.Time { return now }
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}}
	_, release, err := c.prepareGateway(concurrencyCredential("a"), now)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	first, _ := c.liveHistory(info, start.Add(10*time.Second))
	later, _ := c.liveHistory(info, start.Add(20*time.Second))
	if first[0].Members[0].RequestSeconds != 10 || later[0].Members[0].RequestSeconds != 20 {
		t.Fatal("updating the current bucket mutated a published snapshot")
	}
	completed, _ := c.liveHistory(info, start.Add(time.Minute))
	repeated, _ := c.liveHistory(info, start.Add(70*time.Second))
	var old, reused domain.ConcurrencyMinute
	for _, m := range completed {
		if m.BucketStart.Equal(start) {
			old = m
		}
	}
	for _, m := range repeated {
		if m.BucketStart.Equal(start) {
			reused = m
		}
	}
	if len(old.Members) != 1 || len(reused.Members) != 1 || &old.Members[0] != &reused.Members[0] {
		t.Fatal("completed history copied its member slice again")
	}
	// Persistence consumes the same immutable data while subsequent requests run.
	pending := c.pendingMinutes(start.Add(80 * time.Second))
	now = start.Add(90 * time.Second)
	release()
	c.acknowledgeMinutes(pending)
	if len(c.pendingMinutes(now)) == 0 {
		t.Fatal("acknowledgement lost a concurrent update")
	}
}

func TestConcurrencyHistoryActivityDuringFinalFlushPreventsEviction(t *testing.T) {
	c := newAccountTrafficController()
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}}
	c.liveHistory(info, start)
	now := start.Add(25*time.Hour + time.Second)
	pending := c.pendingMinutes(now)
	c.liveHistory(info, now.Add(time.Second))
	c.acknowledgeMinutes(pending)
	if len(c.history) != 1 || len(c.pendingMinutes(now.Add(time.Second))) == 0 {
		t.Fatal("final acknowledgement discarded a reactivated collector")
	}
}

func TestConcurrencyHistoryConcurrentReadersAndRequests(t *testing.T) {
	c := newAccountTrafficController()
	info := domain.PlanConcurrencyContext{Plan: domain.Plan{ID: "plan", AccountID: "account", AccountBindingGeneration: 1}}
	c.liveHistory(info, time.Now().Add(-time.Hour))
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 100 {
			_, release, err := c.prepareGateway(concurrencyCredential("a"), time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			release()
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			minutes, _ := c.liveHistory(info, time.Now())
			if _, err := json.Marshal(minutes); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			pending := c.pendingMinutes(time.Now())
			for _, p := range pending {
				if _, err := json.Marshal(p.value); err != nil {
					t.Error(err)
					return
				}
			}
			c.acknowledgeMinutes(pending)
		}
	}()
	wg.Wait()
	_, current := c.liveHistory(info, time.Now())
	if len(current) != 0 {
		t.Fatal("concurrent history work leaked active requests")
	}
}
