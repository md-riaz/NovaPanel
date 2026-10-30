package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

var ErrStoreClosed = errors.New("store closed")

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type operation struct{ cancel context.CancelFunc }

func (s *Store) begin(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, nil, ErrStoreClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	op := &operation{cancel}
	s.operations[op] = struct{}{}
	s.pending.Add(1)
	var once sync.Once
	return ctx, func() {
		once.Do(func() { cancel(); s.mu.Lock(); delete(s.operations, op); s.mu.Unlock(); s.pending.Done() })
	}, nil
}

type trackedDB struct {
	raw *sql.DB
	s   *Store
}

func (d *trackedDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	ctx, end, e := d.s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer end()
	return d.raw.ExecContext(ctx, q, args...)
}
func (d *trackedDB) Exec(q string, args ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), q, args...)
}

type Rows struct {
	*sql.Rows
	end  func()
	once sync.Once
	done chan struct{}
}

func (r *Rows) Close() error {
	e := r.Rows.Close()
	r.once.Do(func() { close(r.done); r.end() })
	return e
}
func (r *Rows) Next() bool {
	ok := r.Rows.Next()
	if !ok {
		r.Close()
	}
	return ok
}
func (d *trackedDB) QueryContext(ctx context.Context, q string, args ...any) (*Rows, error) {
	ctx, end, e := d.s.begin(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := d.raw.QueryContext(ctx, q, args...)
	if e != nil {
		end()
		return nil, e
	}
	r := &Rows{Rows: rows, end: end, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-r.done:
		}
	}()
	return r, nil
}
func (d *trackedDB) Query(q string, args ...any) (*Rows, error) {
	return d.QueryContext(context.Background(), q, args...)
}

type row struct {
	rows *Rows
	err  error
}

func (r *row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if e := r.rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	return r.rows.Scan(dest...)
}
func (d *trackedDB) QueryRowContext(ctx context.Context, q string, args ...any) *row {
	r, e := d.QueryContext(ctx, q, args...)
	return &row{r, e}
}
func (d *trackedDB) QueryRow(q string, args ...any) *row {
	return d.QueryRowContext(context.Background(), q, args...)
}

// Tests can force pool replacement without exposing the untracked SQL handle.
func (d *trackedDB) SetMaxIdleConns(n int) { d.raw.SetMaxIdleConns(n) }
