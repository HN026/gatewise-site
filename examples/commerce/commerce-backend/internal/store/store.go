package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	_ "github.com/jackc/pgx/v5/stdlib"
	"time"
)

type Store struct {
	DB      *sql.DB
	Timeout time.Duration
	Role    string
}
type Order struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount_cents"`
	Status string `json:"status"`
}

var ErrConflict = errors.New("idempotency key reused with a different amount")

func Open(ctx context.Context, url string, pool int, timeout time.Duration, role string) (*Store, error) {
	db, e := sql.Open("pgx", url)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(pool)
	db.SetMaxIdleConns(pool)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	schemas := map[string][]string{
		"orders":    {`CREATE TABLE IF NOT EXISTS orders(id text PRIMARY KEY,idempotency_key text UNIQUE NOT NULL,amount_cents bigint NOT NULL CHECK(amount_cents>0),status text NOT NULL,created_at timestamptz NOT NULL DEFAULT now())`, `INSERT INTO orders(id,idempotency_key,amount_cents,status) SELECT 'seed-'||i,'seed-'||i,100+(i%10000),'confirmed' FROM generate_series(1,20000) i ON CONFLICT DO NOTHING`},
		"catalog":   {`CREATE TABLE IF NOT EXISTS products(id int PRIMARY KEY,price_cents bigint NOT NULL,description text NOT NULL)`, `INSERT INTO products SELECT i,100+(i%10000),repeat(md5(i::text),4) FROM generate_series(1,20000) i ON CONFLICT DO NOTHING`},
		"inventory": {`CREATE TABLE IF NOT EXISTS stock(id int PRIMARY KEY,available bigint NOT NULL CHECK(available>=0))`, `INSERT INTO stock SELECT i,1000 FROM generate_series(1,20000) i ON CONFLICT DO NOTHING`, `CREATE TABLE IF NOT EXISTS reservations(id text PRIMARY KEY,idempotency_key text UNIQUE NOT NULL,product_id int NOT NULL REFERENCES stock(id),quantity int NOT NULL CHECK(quantity>0))`},
	}
	// Idempotent schema initialization; seed only newly created databases.
	for _, q := range append([]string{`CREATE TABLE IF NOT EXISTS reports(name text PRIMARY KEY,total bigint NOT NULL,updated_at timestamptz NOT NULL DEFAULT now())`}, schemas[role]...) {
		if _, e = db.ExecContext(ctx, q); e != nil {
			db.Close()
			return nil, e
		}
	}
	return &Store{DB: db, Timeout: timeout, Role: role}, nil
}

func (s *Store) begin(ctx context.Context, delay time.Duration, fail bool) (*sql.Tx, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	if delay > 0 {
		_, e = tx.ExecContext(ctx, `SELECT pg_sleep($1)`, delay.Seconds())
	}
	if e == nil && fail {
		_, e = tx.ExecContext(ctx, `SELECT 1/0`)
	}
	if e != nil {
		tx.Rollback()
		return nil, e
	}
	return tx, nil
}
func (s *Store) Checkout(ctx context.Context, key string, amount int64, delay time.Duration, fail bool) (Order, bool, error) {
	tx, e := s.begin(ctx, delay, fail)
	if e != nil {
		return Order{}, false, e
	}
	defer tx.Rollback()
	var b [12]byte
	if _, e = rand.Read(b[:]); e != nil {
		return Order{}, false, e
	}
	o := Order{ID: hex.EncodeToString(b[:]), Amount: amount, Status: "confirmed"}
	e = tx.QueryRowContext(ctx, `INSERT INTO orders(id,idempotency_key,amount_cents,status) VALUES($1,$2,$3,'confirmed') ON CONFLICT(idempotency_key) DO NOTHING RETURNING id,amount_cents,status`, o.ID, key, amount).Scan(&o.ID, &o.Amount, &o.Status)
	created := e == nil
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT id,amount_cents,status FROM orders WHERE idempotency_key=$1`, key).Scan(&o.ID, &o.Amount, &o.Status)
	}
	if e != nil {
		return Order{}, false, e
	}
	if o.Amount != amount {
		return Order{}, false, ErrConflict
	}
	e = tx.Commit()
	return o, created, e
}
func (s *Store) Get(ctx context.Context, id string) (Order, error) {
	var o Order
	e := s.DB.QueryRowContext(ctx, `SELECT id,amount_cents,status FROM orders WHERE id=$1`, id).Scan(&o.ID, &o.Amount, &o.Status)
	return o, e
}
func (s *Store) Rebuild(ctx context.Context, multiplier int, delay time.Duration, fail bool) (int64, error) {
	tx, e := s.begin(ctx, delay, fail)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var total int64
	queries := map[string]string{"orders": `SELECT sum(amount_cents)::bigint FROM orders CROSS JOIN generate_series(1,$1) AS batches`, "catalog": `SELECT sum(price_cents+length(description))::bigint FROM products CROSS JOIN generate_series(1,$1) AS batches`, "inventory": `SELECT sum(available)::bigint FROM stock CROSS JOIN generate_series(1,$1) AS batches`}
	e = tx.QueryRowContext(ctx, queries[s.Role], multiplier).Scan(&total)
	if e != nil {
		return 0, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO reports(name,total) VALUES($2,$1) ON CONFLICT(name) DO UPDATE SET total=excluded.total,updated_at=now()`, total, s.Role)
	if e != nil {
		return 0, e
	}
	e = tx.Commit()
	return total, e
}

type Product struct {
	ID    int   `json:"id"`
	Price int64 `json:"price_cents"`
}
type Reservation struct {
	ID        string `json:"id"`
	ProductID int    `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

var ErrStock = errors.New("insufficient stock")

func (s *Store) Products(ctx context.Context, limit int, delay time.Duration, fail bool) ([]Product, error) {
	tx, e := s.begin(ctx, delay, fail)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,price_cents FROM products ORDER BY id LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	out := []Product{}
	for rows.Next() {
		var p Product
		if e = rows.Scan(&p.ID, &p.Price); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
func (s *Store) Stock(ctx context.Context, id int) (int64, error) {
	var n int64
	e := s.DB.QueryRowContext(ctx, `SELECT available FROM stock WHERE id=$1`, id).Scan(&n)
	return n, e
}
func (s *Store) Reserve(ctx context.Context, key string, id, qty int, delay time.Duration, fail bool) (Reservation, bool, error) {
	tx, e := s.begin(ctx, delay, fail)
	if e != nil {
		return Reservation{}, false, e
	}
	defer tx.Rollback()
	var b [12]byte
	if _, e = rand.Read(b[:]); e != nil {
		return Reservation{}, false, e
	}
	r := Reservation{hex.EncodeToString(b[:]), id, qty}
	e = tx.QueryRowContext(ctx, `INSERT INTO reservations(id,idempotency_key,product_id,quantity) VALUES($1,$2,$3,$4) ON CONFLICT(idempotency_key) DO NOTHING RETURNING id,product_id,quantity`, r.ID, key, id, qty).Scan(&r.ID, &r.ProductID, &r.Quantity)
	created := e == nil
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT id,product_id,quantity FROM reservations WHERE idempotency_key=$1`, key).Scan(&r.ID, &r.ProductID, &r.Quantity)
	}
	if e != nil {
		return Reservation{}, false, e
	}
	if r.ProductID != id || r.Quantity != qty {
		return Reservation{}, false, ErrConflict
	}
	if created {
		result, err := tx.ExecContext(ctx, `UPDATE stock SET available=available-$2 WHERE id=$1 AND available >= $2`, id, qty)
		if err != nil {
			return Reservation{}, false, err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return Reservation{}, false, ErrStock
		}
	}
	return r, created, tx.Commit()
}
