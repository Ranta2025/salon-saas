package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // la zona del salón viene embebida: no depende del sistema
)

// Seed es el programa completo: el estado que comparten los pasos y los
// helpers que usan todos.
type Seed struct {
	cfg config
	log *log.Logger

	api       *apiClient // cliente con el token de la dueña
	assetsDir string
	db        *db
	r         *rng
	clk       clock

	ownerID   string
	salonID   string
	salonSlug string

	services map[string]serviceRef
	staff    []staffRef
	clients  []clientRef
	products map[string]string // nombre -> id
	packages map[string]string // nombre -> id

	// past es el historial creado por SQL: lo consumen ventas, señas y reseñas.
	past []pastBooking
}

// serviceRef es un servicio del catálogo ya creado.
type serviceRef struct {
	ID            string
	Name          string
	Minutes       int
	PriceCents    int
	CommissionPct int
	ResourceType  string
}

// staffRef es un miembro del equipo que atiende clientes.
type staffRef struct {
	UserID   string
	Name     string
	Email    string
	Trade    string
	Services []string
	// segments son sus tramos semanales (con las pausas del mediodía).
	segments map[int][]hourSegment
}

// hourSegment es un tramo de trabajo de un día.
type hourSegment struct {
	Start string
	End   string
}

// clientRef es una ficha ya creada.
type clientRef struct {
	ID    string
	Name  string
	Phone string
	Email string
}

// salón: horario de apertura por día (1 = lunes … 7 = domingo). El domingo no
// está: cerrado.
var salonHours = map[int]hourSegment{
	1: {Start: "10:00", End: "19:00"},
	2: {Start: "09:00", End: "20:00"},
	3: {Start: "09:00", End: "20:00"},
	4: {Start: "09:00", End: "20:00"},
	5: {Start: "09:00", End: "21:00"},
	6: {Start: "09:00", End: "21:00"},
}

// staffSegments arma los tramos de una profesional: dos bloques con la pausa
// del mediodía, y un día libre distinto para cada una.
func staffSegments(email string) map[int][]hourSegment {
	switch email {
	case "dueno@salon.local":
		return map[int][]hourSegment{
			1: {{"10:00", "14:00"}, {"15:00", "19:00"}},
			2: {{"09:00", "13:00"}, {"14:00", "18:00"}},
			3: {{"09:00", "13:00"}, {"14:00", "18:00"}},
			4: {{"09:00", "13:00"}, {"14:00", "18:00"}},
			5: {{"09:00", "13:00"}, {"15:00", "20:00"}},
			6: {{"09:00", "13:00"}, {"14:00", "18:00"}},
		}
	case "camila@salon.local":
		return map[int][]hourSegment{
			1: {{"14:00", "19:00"}},
			2: {{"10:00", "13:00"}, {"14:00", "19:00"}},
			3: {{"10:00", "13:00"}, {"14:00", "19:00"}},
			5: {{"10:00", "13:00"}, {"14:00", "20:00"}},
			6: {{"09:00", "14:00"}},
		}
	case "lucia@salon.local":
		return map[int][]hourSegment{
			2: {{"09:00", "13:00"}, {"14:00", "18:00"}},
			3: {{"09:00", "13:00"}, {"14:00", "18:00"}},
			4: {{"13:00", "20:00"}},
			5: {{"09:00", "13:00"}, {"14:00", "19:00"}},
			6: {{"09:00", "15:00"}},
		}
	case "marcos@salon.local":
		return map[int][]hourSegment{
			1: {{"10:00", "19:00"}},
			2: {{"10:00", "19:00"}},
			4: {{"10:00", "19:00"}},
			5: {{"10:00", "21:00"}},
			6: {{"09:00", "21:00"}},
		}
	case "sofia@salon.local":
		return map[int][]hourSegment{
			2: {{"09:00", "13:00"}, {"15:00", "19:00"}},
			3: {{"09:00", "13:00"}},
			4: {{"09:00", "13:00"}, {"15:00", "19:00"}},
			5: {{"10:00", "16:00"}},
			6: {{"10:00", "16:00"}},
		}
	}
	return map[int][]hourSegment{
		2: {{"09:00", "13:00"}, {"14:00", "18:00"}},
		3: {{"09:00", "13:00"}, {"14:00", "18:00"}},
		4: {{"09:00", "13:00"}, {"14:00", "18:00"}},
	}
}

// run es el programa: seguros, API, base y pasos, en orden.
func run(cfg config, logger *log.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	assetsDir, err := AssetsDir(cfg.AssetsDir)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(demoTZ)
	if err != nil {
		return fmt.Errorf("seed: no cargo la zona %s: %w", demoTZ, err)
	}
	conn, err := openDB(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer conn.close()

	s := &Seed{
		cfg:       cfg,
		log:       logger,
		api:       newAPI(cfg.APIURL),
		assetsDir: assetsDir,
		db:        conn,
		r:         newRNG(cfg.Seed),
		clk:       newClock(time.Now(), loc),
		services:  make(map[string]serviceRef, len(demoServices)),
		products:  make(map[string]string, len(demoProducts)),
		packages:  make(map[string]string, len(demoPackages)),
	}
	return s.Run(ctx)
}

// Run ejecuta la demo completa. Es idempotente: si ya está sembrada no escribe
// nada y lo dice.
func (s *Seed) Run(ctx context.Context) error {
	s.log.Printf("fotos de la demo: %s", s.assetsDir)
	if err := s.waitForAPI(ctx); err != nil {
		return err
	}
	if err := s.login(ctx); err != nil {
		return err
	}
	if err := s.resolveSalon(ctx); err != nil {
		return err
	}

	before, err := s.countTables(ctx)
	if err != nil {
		return err
	}
	if err := s.checkAlreadySeeded(ctx); err != nil {
		return err
	}
	seeded, err := s.isSeeded(ctx)
	if err != nil {
		return err
	}
	if seeded {
		s.log.Printf("la demo ya está sembrada: no escribo nada")
		before.print("fila")
		return s.printSummary(ctx, before, before)
	}

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"salón, perfil y ajustes", s.stepSalon},
		{"servicios y equipamiento", s.stepServices},
		{"equipo y modelos de pago", s.stepTeam},
		{"horarios y ausencias", s.stepHours},
		{"clientas", s.stepClients},
		{"productos e inventario", s.stepProducts},
		{"paquetes, fidelidad y señas", s.stepPrograms},
		{"galería y fotos de fichas", s.stepGallery},
		{"turnos de los próximos días", s.stepFutureBookings},
		{"historial de turnos", s.stepPastBookings},
		{"señas del historial", s.stepDeposits},
		{"ventas y caja", s.stepSales},
		{"gastos", s.stepExpenses},
		{"nómina y liquidaciones", s.stepPayroll},
		{"paquetes vendidos", s.stepSellPackages},
		{"vales de fidelidad", s.stepLoyaltyRewards},
		{"lista de espera", s.stepWaitlist},
		{"reseñas", s.stepReviews},
		{"avisos a la dueña", s.stepNotifications},
		{"logins de la demo", s.stepCheckLogins},
	}
	for _, step := range steps {
		started := time.Now()
		if err := step.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		s.log.Printf("ok  %-32s %5dms", step.name, time.Since(started).Milliseconds())
	}

	after, err := s.countTables(ctx)
	if err != nil {
		return err
	}
	return s.printSummary(ctx, before, after)
}

// waitForAPI espera a que la API responda /health: en el compose la API puede
// estar "healthy" un segundo antes y en local la levantás vos.
func (s *Seed) waitForAPI(ctx context.Context) error {
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.APIURL+"/health", nil)
		if err != nil {
			return err
		}
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", res.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("seed: la API no responde en %s: %w", s.cfg.APIURL, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// login deja a la dueña como usuario del seed y guarda su id.
func (s *Seed) login(ctx context.Context) error {
	owner, err := s.loginOrRegister(ctx, demoUsers[0])
	if err != nil {
		return err
	}
	s.api = owner.client
	s.ownerID = owner.userID
	return nil
}

// loginOrRegister devuelve un cliente con el token de la cuenta: si existe
// inicia sesión, si no la registra (con la clave de la demo).
func (s *Seed) loginOrRegister(ctx context.Context, u demoUser) (account, error) {
	anon := newAPI(s.cfg.APIURL)
	var login struct {
		Token string `json:"token"`
	}
	res, err := anon.do(ctx, http.MethodPost, "/auth/login", map[string]string{"email": u.Email, "password": demoPassword})
	if err != nil {
		return account{}, err
	}
	if res.ok() {
		if err := res.decode(&login); err == nil && login.Token != "" {
			client := newAPI(s.cfg.APIURL)
			client.token = login.Token
			id, err := s.userID(ctx, client)
			if err != nil {
				return account{}, err
			}
			return account{client: client, userID: id}, nil
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	// 401/404 = no existe; 409 = existe con otra clave (no la pisamos a la
	// fuerza: es un dato de alguien). Cualquier otro error se reporta.
	if res.status != http.StatusUnauthorized && res.status != http.StatusNotFound && res.status != http.StatusConflict {
		return account{}, fmt.Errorf("login %s: HTTP %d: %s", u.Email, res.status, res.errorText())
	}
	reg, err := anon.do(ctx, http.MethodPost, "/auth/register", map[string]string{
		"name": u.Name, "email": u.Email, "password": demoPassword,
	})
	if err != nil {
		return account{}, err
	}
	if !reg.ok() {
		return account{}, fmt.Errorf("registro %s: HTTP %d: %s", u.Email, reg.status, reg.errorText())
	}
	if err := reg.decode(&created); err != nil {
		return account{}, err
	}
	token, err := anon.do(ctx, http.MethodPost, "/auth/login", map[string]string{"email": u.Email, "password": demoPassword})
	if err != nil {
		return account{}, err
	}
	if !token.ok() {
		return account{}, fmt.Errorf("login %s: HTTP %d: %s", u.Email, token.status, token.errorText())
	}
	if err := token.decode(&login); err != nil {
		return account{}, err
	}
	client := newAPI(s.cfg.APIURL)
	client.token = login.Token
	return account{client: client, userID: created.ID}, nil
}

type account struct {
	client *apiClient
	userID string
}

// userID lee /me.
func (s *Seed) userID(ctx context.Context, client *apiClient) (string, error) {
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := client.must(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		return "", err
	}
	if me.User.ID == "" {
		return "", fmt.Errorf("seed: /me no devolvió el usuario")
	}
	return me.User.ID, nil
}

// resolveSalon crea el salón de la demo o toma el que ya tiene la dueña.
func (s *Seed) resolveSalon(ctx context.Context) error {
	res, err := s.api.do(ctx, http.MethodPost, "/salons", map[string]string{
		"name": "Salón Aurelia", "slug": demoSalonSlug,
	})
	if err != nil {
		return err
	}
	if res.ok() {
		var created struct {
			ID string `json:"id"`
		}
		if err := res.decode(&created); err == nil && created.ID != "" {
			s.salonID, s.salonSlug = created.ID, demoSalonSlug
			return nil
		}
	}
	var me struct {
		Salons []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"salons"`
	}
	if err := s.api.must(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		return err
	}
	for _, salon := range me.Salons {
		if salon.Slug == demoSalonSlug {
			s.salonID, s.salonSlug = salon.ID, salon.Slug
			return nil
		}
	}
	if len(me.Salons) > 0 {
		s.salonID, s.salonSlug = me.Salons[0].ID, me.Salons[0].Slug
		return nil
	}
	return fmt.Errorf("seed: no pude crear ni encontrar el salón %q: HTTP %d: %s", demoSalonSlug, res.status, res.errorText())
}

// checkAlreadySeeded avisa (sin abortar) si el salón tiene datos que el seed no
// reconoce: un salón de la demo con clientas propias.
func (s *Seed) checkAlreadySeeded(ctx context.Context) error {
	n, err := s.db.count(ctx, "clients", "salon_id=$1 AND email NOT LIKE $2", s.salonID, "%"+demoEmailDomain)
	if err != nil {
		return err
	}
	if n > 0 {
		s.log.Printf("aviso: el salón ya tenía %d fichas que no son de la demo (las dejo como están)", n)
	}
	return nil
}

// isSeeded: la marca de la demo es la última tanda (las reseñas con ids
// deterministas). Si están, la corrida anterior terminó.
func (s *Seed) isSeeded(ctx context.Context) (bool, error) {
	n, err := s.db.count(ctx, "reviews", "salon_id=$1 AND id LIKE 'seed-rv-%'", s.salonID)
	if err != nil {
		return false, err
	}
	return n >= int64(len(demoReviews)), nil
}

// printSummary imprime el antes/después y el resumen final con los logins.
func (s *Seed) printSummary(ctx context.Context, before, after tableCounts) error {
	fmt.Println()
	fmt.Println("== filas por tabla (antes → después) ==")
	for i := range before.Tables {
		name := before.Tables[i].Name
		prev := before.Tables[i].Count
		next := prev
		if i < len(after.Tables) {
			next = after.Tables[i].Count
		}
		mark := ""
		if next > prev {
			mark = fmt.Sprintf("+%d", next-prev)
		}
		fmt.Printf("  %-18s %6d → %6d  %s\n", name, prev, next, mark)
	}
	if before.Tables[0].Count == after.Tables[0].Count && sameCounts(before, after) {
		fmt.Println("  (sin cambios: la segunda corrida no escribió nada)")
	}
	fmt.Println()
	fmt.Println("== entrá al panel con ==")
	fmt.Printf("  dueña          %-26s %s\n", demoUsers[0].Email, demoPassword)
	for _, u := range demoUsers[1:] {
		fmt.Printf("  %-14s %-26s %s\n", roleLabel(u.Role), u.Email, demoPassword)
	}
	fmt.Printf("  landing        %s/salon/%s\n", s.cfg.APIURL, s.salonSlug)
	return nil
}

func sameCounts(a, b tableCounts) bool {
	if len(a.Tables) != len(b.Tables) {
		return false
	}
	for i := range a.Tables {
		if a.Tables[i].Count != b.Tables[i].Count {
			return false
		}
	}
	return true
}

// roleLabel traduce el rol para la tabla de logins del reporte.
func roleLabel(role string) string {
	switch role {
	case "admin":
		return "administración"
	case "reception":
		return "recepción"
	case "professional":
		return "profesional"
	}
	return role
}

// --- helpers que usan los pasos -------------------------------------------

// seedID arma un id determinista: es lo que hace idempotente a cada INSERT.
func seedID(prefix string, n int) string {
	return fmt.Sprintf("seed-%s-%04d", prefix, n)
}

// existsIn cuenta filas de una tabla por id (para no repetir trabajo).
func (s *Seed) existsIn(ctx context.Context, table, id string) (bool, error) {
	n, err := s.db.count(ctx, table, "id=$1", id)
	return n > 0, err
}

// jsonID lee la clave "id" de una respuesta JSON suelta.
func jsonID(obj map[string]any) string { return stringField(obj, "id") }

// lookup mapea por nombre para no duplicar lo que ya existe.
func (s *Seed) listByKey(ctx context.Context, path, key, field string) (map[string]string, error) {
	list, err := s.api.getList(ctx, path, key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, obj := range list {
		out[strings.TrimSpace(stringField(obj, field))] = jsonID(obj)
	}
	return out, nil
}
