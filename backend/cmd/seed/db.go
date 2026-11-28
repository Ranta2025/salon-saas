package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// db es el acceso directo a Postgres: SOLO para lo que la API no puede crear
// (turnos y ventas con fecha pasada, cierres de caja, liquidaciones viejas,
// reseñas, avisos). Todo lo demás pasa por la API, que aplica las reglas.
//
// Las filas que escribe el seed llevan ids deterministas (`seed-...`), así una
// segunda corrida no duplica nada: cada INSERT es ON CONFLICT DO NOTHING.
type db struct {
	pool *pgxpool.Pool
}

func openDB(ctx context.Context, url string) (*db, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("seed: DATABASE_URL inválida: %w", err)
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("seed: conectar a Postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("seed: Postgres no responde: %w", err)
	}
	return &db{pool: pool}, nil
}

func (d *db) close() { d.pool.Close() }

// exec corre una sentencia (INSERT/UPDATE) y devuelve las filas afectadas.
func (d *db) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := d.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("seed: %w (sql: %s)", err, firstLine(sql))
	}
	return tag.RowsAffected(), nil
}

// scalar corre una consulta que devuelve un único valor.
func (d *db) scalar(ctx context.Context, sql string, args ...any) (int64, error) {
	var n int64
	if err := d.pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("seed: %w (sql: %s)", err, firstLine(sql))
	}
	return n, nil
}

// count cuenta filas de una tabla con una condición.
func (d *db) count(ctx context.Context, table, where string, args ...any) (int64, error) {
	sql := "SELECT count(*) FROM " + table
	if where != "" {
		sql += " WHERE " + where
	}
	return d.scalar(ctx, sql, args...)
}

// firstLine recorta la sentencia para que el error sea legible.
func firstLine(sql string) string {
	for i, r := range sql {
		if r == '\n' {
			return sql[:i] + " …"
		}
	}
	return sql
}

// tableCounts es lo que el seed imprime antes y después: la prueba de que la
// segunda corrida no cambió nada.
type tableCounts struct {
	Tables []tableCount
}

type tableCount struct {
	Name  string
	Count int64
}

// countTables cuenta las tablas del salón de la demo.
func (s *Seed) countTables(ctx context.Context) (tableCounts, error) {
	type query struct {
		name string
		sql  string
		args []any
	}
	queries := []query{
		{"users", `SELECT count(*) FROM users WHERE email LIKE '%@salon.local'`, nil},
		{"salons", `SELECT count(*) FROM salons WHERE slug=$1`, []any{demoSalonSlug}},
		{"memberships", `SELECT count(*) FROM memberships WHERE salon_id=$1`, []any{s.salonID}},
		{"services", `SELECT count(*) FROM services WHERE salon_id=$1`, []any{s.salonID}},
		{"service_packages", `SELECT count(*) FROM service_packages WHERE salon_id=$1`, []any{s.salonID}},
		{"products", `SELECT count(*) FROM products WHERE salon_id=$1`, []any{s.salonID}},
		{"stock_movements", `SELECT count(*) FROM stock_movements WHERE salon_id=$1`, []any{s.salonID}},
		{"resources(retos)", `SELECT count(*) FROM resource_types WHERE salon_id=$1`, []any{s.salonID}},
		{"resource_units", `SELECT count(*) FROM resource_units WHERE salon_id=$1`, []any{s.salonID}},
		{"salon_weekly_hours", `SELECT count(*) FROM salon_weekly_hours WHERE salon_id=$1`, []any{s.salonID}},
		{"professional_hours", `SELECT count(*) FROM professional_hours WHERE salon_id=$1`, []any{s.salonID}},
		{"time_off", `SELECT count(*) FROM time_off WHERE salon_id=$1`, []any{s.salonID}},
		{"salon_photos", `SELECT count(*) FROM salon_photos WHERE salon_id=$1`, []any{s.salonID}},
		{"clients", `SELECT count(*) FROM clients WHERE salon_id=$1`, []any{s.salonID}},
		{"client_photos", `SELECT count(*) FROM client_photos WHERE salon_id=$1`, []any{s.salonID}},
		{"bookings", `SELECT count(*) FROM bookings WHERE salon_id=$1`, []any{s.salonID}},
		{"bookings(futuros)", `SELECT count(*) FROM bookings WHERE salon_id=$1 AND starts_at > now()`, []any{s.salonID}},
		{"sales", `SELECT count(*) FROM sales WHERE salon_id=$1`, []any{s.salonID}},
		{"sale_items", `SELECT count(*) FROM sale_items si JOIN sales s ON s.id=si.sale_id WHERE s.salon_id=$1`, []any{s.salonID}},
		{"sale_products", `SELECT count(*) FROM sale_products sp JOIN sales s ON s.id=sp.sale_id WHERE s.salon_id=$1`, []any{s.salonID}},
		{"cash_closes", `SELECT count(*) FROM cash_closes WHERE salon_id=$1`, []any{s.salonID}},
		{"expenses", `SELECT count(*) FROM expenses WHERE salon_id=$1`, []any{s.salonID}},
		{"member_pay", `SELECT count(*) FROM member_pay WHERE salon_id=$1`, []any{s.salonID}},
		{"payroll_runs", `SELECT count(*) FROM payroll_runs WHERE salon_id=$1`, []any{s.salonID}},
		{"payroll_lines", `SELECT count(*) FROM payroll_lines pl JOIN payroll_runs pr ON pr.id=pl.run_id WHERE pr.salon_id=$1`, []any{s.salonID}},
		{"client_packages", `SELECT count(*) FROM client_packages WHERE salon_id=$1`, []any{s.salonID}},
		{"package_movements", `SELECT count(*) FROM package_movements WHERE salon_id=$1`, []any{s.salonID}},
		{"loyalty_programs", `SELECT count(*) FROM loyalty_programs WHERE salon_id=$1`, []any{s.salonID}},
		{"loyalty_rewards", `SELECT count(*) FROM loyalty_rewards WHERE salon_id=$1`, []any{s.salonID}},
		{"waitlist_entries", `SELECT count(*) FROM waitlist_entries WHERE salon_id=$1`, []any{s.salonID}},
		{"reviews", `SELECT count(*) FROM reviews WHERE salon_id=$1`, []any{s.salonID}},
		{"review_requests", `SELECT count(*) FROM review_requests WHERE salon_id=$1`, []any{s.salonID}},
		{"deposit_policies", `SELECT count(*) FROM deposit_policies WHERE salon_id=$1`, []any{s.salonID}},
		{"booking_deposits", `SELECT count(*) FROM booking_deposits WHERE salon_id=$1`, []any{s.salonID}},
		{"deposit_events", `SELECT count(*) FROM deposit_events de JOIN booking_deposits bd ON bd.id=de.deposit_id WHERE bd.salon_id=$1`, []any{s.salonID}},
		{"notifications", `SELECT count(*) FROM notifications WHERE salon_id=$1`, []any{s.salonID}},
		{"audit_events", `SELECT count(*) FROM audit_events WHERE salon_id=$1`, []any{s.salonID}},
	}
	out := tableCounts{Tables: make([]tableCount, 0, len(queries))}
	for _, q := range queries {
		n, err := s.db.scalar(ctx, q.sql, q.args...)
		if err != nil {
			return tableCounts{}, err
		}
		out.Tables = append(out.Tables, tableCount{Name: q.name, Count: n})
	}
	return out, nil
}

// print muestra el conteo en una tabla alineada.
func (c tableCounts) print(prefix string) {
	for _, t := range c.Tables {
		fmt.Printf("%s %-18s %6d\n", prefix, t.Name, t.Count)
	}
}
