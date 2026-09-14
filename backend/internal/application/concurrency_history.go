package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

type concurrencySeriesKey struct {
	account, plan string
	generation    int64
}
type concurrencyBucket struct {
	minute           domain.ConcurrencyMinute
	seconds          map[string]float64
	revision         uint64
	saved            uint64
	snapshot         domain.ConcurrencyMinute
	snapshotRevision uint64
}
type concurrencySeries struct {
	instanceID string
	key        concurrencySeriesKey
	last       time.Time
	lastUsed   time.Time
	counts     map[string]int
	names      map[string]string
	buckets    map[int64]*concurrencyBucket
}

func concurrencyInstanceID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

// All collector operations run under the admission mutex. Integrating at each
// transition preserves short spikes; the persistence cadence is not sampling.
func (c *accountTrafficController) series(key concurrencySeriesKey, now time.Time) *concurrencySeries {
	v := c.history[key]
	if v == nil {
		c.nextSeries++
		v = &concurrencySeries{instanceID: c.instanceID + "-" + strconv.FormatUint(c.nextSeries, 10), key: key, last: now, lastUsed: now, counts: map[string]int{}, names: map[string]string{}, buckets: map[int64]*concurrencyBucket{}}
		c.history[key] = v
	}
	return v
}
func (c *accountTrafficController) bucket(v *concurrencySeries, at time.Time) *concurrencyBucket {
	at = at.Truncate(time.Minute)
	b := v.buckets[at.Unix()]
	if b == nil {
		b = &concurrencyBucket{minute: domain.ConcurrencyMinute{InstanceID: v.instanceID, AccountID: v.key.account, PlanID: v.key.plan, Generation: v.key.generation, BucketStart: at}, seconds: map[string]float64{}}
		v.buckets[at.Unix()] = b
	}
	return b
}
func (c *accountTrafficController) advance(v *concurrencySeries, now time.Time) {
	// Bound work after suspension or an unusually long persistence outage.
	cutoff := now.Add(-25 * time.Hour).Truncate(time.Minute)
	if v.last.Before(cutoff) {
		v.last = cutoff
	}
	total := 0
	for _, n := range v.counts {
		total += n
	}
	for v.last.Before(now) {
		end := v.last.Truncate(time.Minute).Add(time.Minute)
		if end.After(now) {
			end = now
		}
		seconds := end.Sub(v.last).Seconds()
		b := c.bucket(v, v.last)
		b.minute.ObservedSeconds += seconds
		b.minute.Peak = max(b.minute.Peak, total)
		for user, n := range v.counts {
			if n > 0 {
				b.seconds[user] += float64(n) * seconds
			}
		}
		b.revision++
		v.last = end
	}
}
func (c *accountTrafficController) changeHistory(credential domain.GatewayCredential, delta int, now time.Time) {
	key := concurrencySeriesKey{credential.Account.ID, credential.Plan.ID, credential.AccountBindingGeneration}
	v := c.series(key, now)
	if now.Before(v.last) {
		now = v.last
	}
	c.advance(v, now)
	v.lastUsed = now
	user := credential.Member.UserID
	v.names[user] = credential.Member.Username
	v.counts[user] += delta
	if v.counts[user] == 0 {
		delete(v.counts, user)
	}
	total := 0
	for _, n := range v.counts {
		total += n
	}
	b := c.bucket(v, now)
	b.minute.Peak = max(b.minute.Peak, total)
	b.revision++
}

// Published snapshots and their member slices are immutable. Completed minutes
// can therefore be reused by every reader without copying or sorting members
// while holding the admission mutex. Only a changed bucket needs a new snapshot.
func (c *accountTrafficController) copyMinute(v *concurrencySeries, b *concurrencyBucket) domain.ConcurrencyMinute {
	if b.snapshot.Members != nil && b.snapshotRevision == b.revision {
		return b.snapshot
	}
	out := b.minute
	out.Members = make([]domain.ConcurrencyMemberSample, 0, len(b.seconds))
	for user, seconds := range b.seconds {
		out.Members = append(out.Members, domain.ConcurrencyMemberSample{UserID: user, Username: v.names[user], RequestSeconds: seconds})
	}
	b.snapshot, b.snapshotRevision = out, b.revision
	if !b.minute.BucketStart.Add(time.Minute).After(v.last) {
		// advance never revisits a completed minute. Keep only its immutable
		// representation rather than retaining a second full member map.
		b.seconds = nil
	}
	return out
}

type pendingConcurrencyMinute struct {
	value    domain.ConcurrencyMinute
	revision uint64
}

func (c *accountTrafficController) pendingMinutes(now time.Time) []pendingConcurrencyMinute {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]pendingConcurrencyMinute, 0)
	for _, v := range c.history {
		cutoff := now.Add(-25 * time.Hour).Truncate(time.Minute).Unix()
		for at := range v.buckets {
			if at < cutoff {
				delete(v.buckets, at)
			}
		}
		c.advance(v, now)
		for _, b := range v.buckets {
			if b.revision != b.saved {
				out = append(out, pendingConcurrencyMinute{c.copyMinute(v, b), b.revision})
			}
		}
	}
	return out
}
func (c *accountTrafficController) acknowledgeMinutes(pending []pendingConcurrencyMinute) {
	c.mu.Lock()
	defer c.mu.Unlock()
	touched := make(map[concurrencySeriesKey]*concurrencySeries)
	for _, p := range pending {
		v := c.history[concurrencySeriesKey{p.value.AccountID, p.value.PlanID, p.value.Generation}]
		if v != nil {
			if b := v.buckets[p.value.BucketStart.Unix()]; b != nil && b.minute.InstanceID == p.value.InstanceID {
				b.saved = max(b.saved, p.revision)
				touched[v.key] = v
			}
		}
	}
	// A failed flush never acknowledges data, so its collector remains available
	// for retry. Requests or readers arriving during persistence keep it alive.
	for key, v := range touched {
		if len(v.counts) != 0 || v.last.Sub(v.lastUsed) <= 25*time.Hour {
			continue
		}
		clean := true
		for _, b := range v.buckets {
			if b.saved != b.revision {
				clean = false
				break
			}
		}
		if clean {
			delete(c.history, key)
		}
	}
}
func (c *accountTrafficController) liveHistory(info domain.PlanConcurrencyContext, now time.Time) ([]domain.ConcurrencyMinute, map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.series(concurrencySeriesKey{info.Plan.AccountID, info.Plan.ID, info.Plan.AccountBindingGeneration}, now)
	v.lastUsed = now
	c.advance(v, now)
	out := make([]domain.ConcurrencyMinute, 0, len(v.buckets))
	for _, b := range v.buckets {
		out = append(out, c.copyMinute(v, b))
	}
	counts := make(map[string]int, len(v.counts))
	for user, n := range v.counts {
		counts[user] = n
	}
	return out, counts
}

func (s *Service) FlushConcurrencyHistory(ctx context.Context) error {
	s.concurrencyFlush.Lock()
	defer s.concurrencyFlush.Unlock()
	pending := s.traffic.pendingMinutes(s.now())
	if len(pending) == 0 {
		return nil
	}
	values := make([]domain.ConcurrencyMinute, len(pending))
	for i, p := range pending {
		values[i] = p.value
	}
	if err := s.store.SaveConcurrencyMinutes(ctx, values); err != nil {
		return err
	}
	s.traffic.acknowledgeMinutes(pending)
	return nil
}
func (s *Service) RunConcurrencyHistory(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := s.FlushConcurrencyHistory(flushCtx)
			cancel()
			if err != nil && ctx.Err() == nil && s.logger != nil {
				s.logger.Error("persist concurrency history", "error", err)
			}
		}
	}
}
