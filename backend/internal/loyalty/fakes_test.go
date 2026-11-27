package loyalty

import (
	"sort"
	"sync"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
	"salon-saas/internal/testutil"
)

// Fakes de fidelidad (F9) con la misma semántica que pgstore: scope por salón,
// counting_from fijado en el primer guardado, y entregar/usar como UNA
// "transacción" bajo un mutex que reusa la regla de dominio (NewLoyaltyGrant).

type fakeLoyaltyPrograms struct {
	mu        sync.Mutex
	services  *testutil.FakeServices
	rows      map[string]domain.LoyaltyProgram
	saveCalls int
}

func (f *fakeLoyaltyPrograms) named(p domain.LoyaltyProgram) domain.LoyaltyProgram {
	if svc, err := f.services.FindByID(p.SalonID, p.RewardServiceID); err == nil {
		p.RewardServiceName = svc.Name
	}
	p.Saved = true
	return p
}

func (f *fakeLoyaltyPrograms) Find(salonID string) (domain.LoyaltyProgram, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[salonID]
	if !ok {
		return domain.LoyaltyProgram{}, domain.ErrNotFound
	}
	return f.named(p), nil
}

func (f *fakeLoyaltyPrograms) Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.rows == nil {
		f.rows = map[string]domain.LoyaltyProgram{}
	}
	if old, ok := f.rows[p.SalonID]; ok {
		p.CountingFrom = old.CountingFrom
	}
	f.rows[p.SalonID] = p
	return f.named(p), nil
}

var _ ports.LoyaltyProgramRepository = (*fakeLoyaltyPrograms)(nil)

type fakeLoyaltyRewards struct {
	mu       sync.Mutex
	programs *fakeLoyaltyPrograms
	// visits es lo que «calcularía» la consulta de la base, por salón|clienta.
	visits  map[string]int
	rewards []domain.LoyaltyReward
	// since recuerda desde cuándo se pidió contar (debe ser counting_from).
	since []time.Time
}

func loyKey(salonID, clientID string) string { return salonID + "|" + clientID }

func (f *fakeLoyaltyRewards) Visits(salonID, clientID string, since time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	return f.visits[loyKey(salonID, clientID)], nil
}

func (f *fakeLoyaltyRewards) granted(salonID, clientID string) int {
	n := 0
	for _, r := range f.rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			n++
		}
	}
	return n
}

func (f *fakeLoyaltyRewards) CountGranted(salonID, clientID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.granted(salonID, clientID), nil
}

func (f *fakeLoyaltyRewards) ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LoyaltyReward
	for _, r := range f.rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RewardNumber > out[j].RewardNumber })
	if len(out) > domain.MaxLoyaltyListed {
		out = out[:domain.MaxLoyaltyListed]
	}
	return out, nil
}

func (f *fakeLoyaltyRewards) Grant(r domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.ClientOpID != "" {
		for _, x := range f.rewards {
			if x.SalonID == r.SalonID && x.GrantOpID == r.ClientOpID {
				if x.ClientID != r.ClientID {
					return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyOpReused)
				}
				return x, nil
			}
		}
	}
	prog, ok := f.programs.rows[r.SalonID]
	if ok {
		prog = f.programs.named(prog)
	}
	reward, err := domain.NewLoyaltyGrant(prog, r.ClientID, f.visits[loyKey(r.SalonID, r.ClientID)],
		f.granted(r.SalonID, r.ClientID), r.ClientOpID, r.Now, r.Loc)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	f.rewards = append(f.rewards, reward)
	return reward, nil
}

func (f *fakeLoyaltyRewards) Redeem(q domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	find := func() int {
		for i, x := range f.rewards {
			if x.SalonID == q.SalonID && x.ClientID == q.ClientID && x.ID == q.RewardID {
				return i
			}
		}
		return -1
	}
	if q.ClientOpID != "" {
		for _, x := range f.rewards {
			if x.SalonID == q.SalonID && x.RedeemOpID == q.ClientOpID {
				if i := find(); i < 0 || f.rewards[i].ID != x.ID {
					return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyOpReused)
				}
				return x, nil
			}
		}
	}
	if prog, ok := f.programs.rows[q.SalonID]; !ok || !prog.Enabled {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyDisabled)
	}
	i := find()
	if i < 0 {
		return domain.LoyaltyReward{}, domain.ErrNotFound
	}
	x := &f.rewards[i]
	switch {
	case x.RedeemedAt != nil:
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyRewardUsed)
	case x.ExpiresAt != nil && !x.ExpiresAt.After(q.Now):
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyRewardExpired)
	}
	at := q.Now.UTC()
	x.RedeemedAt, x.RedeemedBy = &at, q.Actor
	x.RedeemedProfessionalID, x.RedeemNote, x.RedeemOpID = q.ProfessionalID, q.Note, q.ClientOpID
	return *x, nil
}

var _ ports.LoyaltyRewardRepository = (*fakeLoyaltyRewards)(nil)
