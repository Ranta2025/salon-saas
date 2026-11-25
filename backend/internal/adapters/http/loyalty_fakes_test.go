package httpapi

import (
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// Fakes de fidelidad (F9) del adaptador HTTP, con la misma semántica que pgstore:
// scope por salón, counting_from fijado en el primer guardado y entregar/usar
// como UNA «transacción» bajo un mutex que reusa la regla de dominio.

type mloyPrograms struct {
	mu       sync.Mutex
	services *msvc
	rows     map[string]domain.LoyaltyProgram
	saves    int
}

func (f *mloyPrograms) named(p domain.LoyaltyProgram) domain.LoyaltyProgram {
	if svc, err := f.services.FindByID(p.SalonID, p.RewardServiceID); err == nil {
		p.RewardServiceName = svc.Name
	}
	p.Saved = true
	return p
}

func (f *mloyPrograms) Find(salonID string) (domain.LoyaltyProgram, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[salonID]
	if !ok {
		return domain.LoyaltyProgram{}, domain.ErrNotFound
	}
	return f.named(p), nil
}

func (f *mloyPrograms) Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.rows == nil {
		f.rows = map[string]domain.LoyaltyProgram{}
	}
	if old, ok := f.rows[p.SalonID]; ok {
		p.CountingFrom = old.CountingFrom
	}
	f.rows[p.SalonID] = p
	return f.named(p), nil
}

var _ ports.LoyaltyProgramRepository = (*mloyPrograms)(nil)

type mloyRewards struct {
	mu       sync.Mutex
	programs *mloyPrograms
	// visits es lo que «calcularía» la consulta de la base, por salón|clienta.
	visits  map[string]int
	rewards []domain.LoyaltyReward
}

func mloyKey(salonID, clientID string) string { return salonID + "|" + clientID }

func (f *mloyRewards) Visits(salonID, clientID string, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.visits[mloyKey(salonID, clientID)], nil
}

func (f *mloyRewards) granted(salonID, clientID string) int {
	n := 0
	for _, r := range f.rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			n++
		}
	}
	return n
}

func (f *mloyRewards) CountGranted(salonID, clientID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.granted(salonID, clientID), nil
}

func (f *mloyRewards) ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.LoyaltyReward
	for _, r := range f.rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RewardNumber > out[j].RewardNumber })
	return out, nil
}

func (f *mloyRewards) Grant(r domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error) {
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
	reward, err := domain.NewLoyaltyGrant(prog, r.ClientID, f.visits[mloyKey(r.SalonID, r.ClientID)],
		f.granted(r.SalonID, r.ClientID), r.ClientOpID, r.Now, r.Loc)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	f.rewards = append(f.rewards, reward)
	return reward, nil
}

func (f *mloyRewards) Redeem(q domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error) {
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

var _ ports.LoyaltyRewardRepository = (*mloyRewards)(nil)

// newLoyaltyEnv rearma el router con la fidelidad habilitada (el entorno del POS
// no la conoce: no se tocan los tests existentes) y la vidriera pública con su
// campo `loyalty`.
func newLoyaltyEnv() (*posEnv, *mloyPrograms, *mloyRewards) {
	env := newPOSEnv()
	programs := &mloyPrograms{services: env.services}
	rewards := &mloyRewards{programs: programs, visits: map[string]int{}}
	svc := app.LoyaltyService{
		Programs: programs, Rewards: rewards, Services: env.services, Clients: env.clients,
		Memberships: env.mems, Settings: env.settings,
	}
	env.d.Loyalty = LoyaltyHandler{Loyalty: svc, Memberships: env.mems}
	env.d.Public.Public = app.PublicSalonService{
		Salons: env.salons, Services: env.services, Settings: env.settings, Hours: env.weekly,
		Loyalty: programs,
	}
	env.r = gin.New()
	RegisterRoutes(env.r, env.d)
	return env, programs, rewards
}
