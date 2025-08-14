/*
Package transaction provides mockable interfaces of sql package struct types.
*/
package transaction

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/rancher/steve/pkg/otel"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type QueryStatement struct {
	Query string
	Stmt  *sql.Stmt
}

func (q QueryStatement) Close() error {
	return q.Stmt.Close()
}

// Client is an interface over a subset of sql.Tx methods
// rationale 1: explicitly forbid direct access to Commit and Rollback functionality
// as that is exclusively dealt with by WithTransaction in ../db
// rationale 2: allow mocking
type Client interface {
	Exec(query string, args ...any) (sql.Result, error)
	Stmt(stmt QueryStatement) Stmt
}

// client is the main implementation of Client, delegates to sql.Tx
// other implementations exist for testing purposes
type client struct {
	tx *sql.Tx
}

func NewClient(tx *sql.Tx) Client {
	return &client{tx: tx}
}

func (c client) Exec(query string, args ...any) (sql.Result, error) {
	return c.tx.Exec(query, args...)
}

func (c client) Stmt(stmt QueryStatement) Stmt {
	traced := &tracedStmt{
		inner: c.tx.Stmt(stmt.Stmt),
		query: stmt.Query,
	}
	return traced
}

// Stmt is an interface over a subset of sql.Stmt methods
// rationale: allow mocking
type Stmt interface {
	Exec(args ...any) (sql.Result, error)
	Query(args ...any) (*sql.Rows, error)
	QueryContext(ctx context.Context, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, args ...any) *sql.Row
}

type tracedStmt struct {
	inner *sql.Stmt
	query string
}

func (s *tracedStmt) Exec(args ...any) (sql.Result, error) {
	return s.inner.Exec(args...)
}

func (s *tracedStmt) Query(args ...any) (*sql.Rows, error) {
	return s.inner.Query(args...)
}

func (s *tracedStmt) QueryContext(ctx context.Context, args ...any) (*sql.Rows, error) {
	ctx, span := otel.Start(ctx, "QueryContext",
		trace.WithAttributes(attribute.String("query", s.query)),
		trace.WithAttributes(attribute.String("params", fmt.Sprintf("%v", args))),
	)
	defer span.End()

	now := time.Now()

	defer func() {
		elapsed := time.Since(now)
		if elapsed > 5*time.Millisecond {
			logrus.Infof("Long query: %s", s.query)
		}
	}()

	return s.inner.QueryContext(ctx, args...)
}

func (s *tracedStmt) QueryRowContext(ctx context.Context, args ...any) *sql.Row {
	return s.inner.QueryRowContext(ctx, args...)
}
