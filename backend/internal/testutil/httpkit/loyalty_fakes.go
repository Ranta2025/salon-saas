package httpkit

import (
	httpapi "salon-saas/internal/adapters/http"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/loyalty"
	"salon-saas/internal/ports"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Fakes de fidelidad (F9) del adaptador HTTP, con la misma semántica que pgstore:
// scope por salón, counting_from fijado en el primer guardado y entregar/usar
// como UNA «transacción» bajo un mutex que reusa la regla de dominio.

type LoyaltyPrograms struct {
	Mu       sync.Mutex
	Services *Services
	Rows     map[string]domain.LoyaltyProgram
	Saves    int
}

func (f *LoyaltyPrograms) named(p domain.LoyaltyProgram) domain.LoyaltyProgram {
	if svc, err := f.Services.FindByID(p.SalonID, p.RewardServiceID); err == nil {
		p.RewardServiceName = svc.Name
	}
	p.Saved = true
	return p
}

func (f *LoyaltyPrograms) Find(salonID string) (domain.LoyaltyProgram, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	p, ok := f.Rows[salonID]
	if !ok {
		return domain.LoyaltyProgram{}, domain.ErrNotFound
	}
	return f.named(p), nil
}

func (f *LoyaltyPrograms) Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Saves++
	if f.Rows == nil {
		f.Rows = map[string]domain.LoyaltyProgram{}
	}
	if old, ok := f.Rows[p.SalonID]; ok {
		p.CountingFrom = old.CountingFrom
	}
	f.Rows[p.SalonID] = p
	return f.named(p), nil
}

var _ ports.LoyaltyProgramRepository = (*LoyaltyPrograms)(nil)

type LoyaltyRewards struct {
	Mu       sync.Mutex
	Programs *LoyaltyPrograms
	// visits es lo que «calcularía» la consulta de la base, por salón|clienta.
	VisitCounts map[string]int
	Rewards     []domain.LoyaltyReward
}

func LoyaltyKey(salonID, clientID string) string { return salonID + "|" + clientID }

func (f *LoyaltyRewards) Visits(salonID, clientID string, _ time.Time) (int, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.VisitCounts[LoyaltyKey(salonID, clientID)], nil
}

func (f *LoyaltyRewards) granted(salonID, clientID string) int {
	n := 0
	for _, r := range f.Rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			n++
		}
	}
	return n
}

func (f *LoyaltyRewards) CountGranted(salonID, clientID string) (int, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.granted(salonID, clientID), nil
}

func (f *LoyaltyRewards) ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []domain.LoyaltyReward
	for _, r := range f.Rewards {
		if r.SalonID == salonID && r.ClientID == clientID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RewardNumber > out[j].RewardNumber })
	return out, nil
}

func (f *LoyaltyRewards) Grant(r domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if r.ClientOpID != "" {
		for _, x := range f.Rewards {
			if x.SalonID == r.SalonID && x.GrantOpID == r.ClientOpID {
				if x.ClientID != r.ClientID {
					return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyOpReused)
				}
				return x, nil
			}
		}
	}
	prog, ok := f.Programs.Rows[r.SalonID]
	if ok {
		prog = f.Programs.named(prog)
	}
	reward, err := domain.NewLoyaltyGrant(prog, r.ClientID, f.VisitCounts[LoyaltyKey(r.SalonID, r.ClientID)],
		f.granted(r.SalonID, r.ClientID), r.ClientOpID, r.Now, r.Loc)
	if err != nil {
		return domain.LoyaltyReward{}, err
	}
	f.Rewards = append(f.Rewards, reward)
	return reward, nil
}

func (f *LoyaltyRewards) Redeem(q domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	find := func() int {
		for i, x := range f.Rewards {
			if x.SalonID == q.SalonID && x.ClientID == q.ClientID && x.ID == q.RewardID {
				return i
			}
		}
		return -1
	}
	if q.ClientOpID != "" {
		for _, x := range f.Rewards {
			if x.SalonID == q.SalonID && x.RedeemOpID == q.ClientOpID {
				if i := find(); i < 0 || f.Rewards[i].ID != x.ID {
					return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyOpReused)
				}
				return x, nil
			}
		}
	}
	if prog, ok := f.Programs.Rows[q.SalonID]; !ok || !prog.Enabled {
		return domain.LoyaltyReward{}, domain.NewMessage(domain.ErrAlreadyExists, domain.MsgLoyaltyDisabled)
	}
	i := find()
	if i < 0 {
		return domain.LoyaltyReward{}, domain.ErrNotFound
	}
	x := &f.Rewards[i]
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

var _ ports.LoyaltyRewardRepository = (*LoyaltyRewards)(nil)

// NewLoyaltyEnv rearma el router con la fidelidad habilitada (el entorno del POS
// no la conoce: no se tocan los tests existentes) y la vidriera pública con su
// campo `loyalty`.
func NewLoyaltyEnv() (*Env, *LoyaltyPrograms, *LoyaltyRewards) {
	env := NewPOSEnv()
	programs := &LoyaltyPrograms{Services: env.Services}
	rewards := &LoyaltyRewards{Programs: programs, VisitCounts: map[string]int{}}
	svc := loyalty.LoyaltyService{
		Programs: programs, Rewards: rewards, Services: env.Services, Clients: env.Clients,
		Memberships: env.Mems, Settings: env.Settings,
	}
	env.Deps.Loyalty = httpapi.LoyaltyHandler{Loyalty: svc, Memberships: env.Mems}
	env.Deps.Public.Public = app.PublicSalonService{
		Salons: env.Salons, Services: env.Services, Settings: env.Settings, Hours: env.Weekly,
		Loyalty: programs,
	}
	env.Router = gin.New()
	httpapi.RegisterRoutes(env.Router, env.Deps)
	return env, programs, rewards
}
