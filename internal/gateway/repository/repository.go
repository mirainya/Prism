// Package repository contains the only SQL write boundary for the unified
// gateway schema.  Domain packages stay database agnostic; this package
// applies their validated state transitions in short, explicit transactions.
package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

func validHexDigest(value string, bytesLen int) bool {
	if len(value) != bytesLen*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

var (
	ErrNotFound      = errors.New("gateway repository: row not found")
	ErrConflict      = errors.New("gateway repository: optimistic concurrency conflict")
	ErrInvalidInput  = errors.New("gateway repository: invalid input")
	ErrAlreadyExists = errors.New("gateway repository: row already exists")
	ErrInsufficient  = errors.New("gateway repository: insufficient balance or budget")
)

// DB is implemented by *sql.DB and *sql.Tx. It makes every repository method
// usable inside the caller's transaction without hiding transaction scope.
type DB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, ErrInvalidInput
	}
	return &Store{db: db}, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if fn == nil {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin gateway transaction: %w", err)
	}
	// Rollback is idempotent after Commit and prevents a panic or context
	// cancellation from leaving a transaction open on a pooled connection.
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit gateway transaction: %w", err)
	}
	return nil
}

func nowUTC() time.Time { return time.Now().UTC() }

func lastID(result sql.Result) (uint64, error) {
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	if id <= 0 {
		return 0, ErrConflict
	}
	return uint64(id), nil
}

func affected(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	return n == 1, err
}
