package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"

	"modernc.org/sqlite"
)

var (
	sqlQueriesIssued atomic.Int64
	sqlRowsRead      atomic.Int64
)

// SQLQueriesIssued answers how many SQL queries this process has issued through store
// connections. It is observation, for tests that hold a face to a query budget that
// does not grow with finished history.
func SQLQueriesIssued() int64 { return sqlQueriesIssued.Load() }

// SQLRowsRead answers how many SQL result rows this process has read from store
// connections across all tables. It is observation, for tests that hold a face to a
// row-read budget that does not grow with finished history.
func SQLRowsRead() int64 { return sqlRowsRead.Load() }

// EventRowsRead answers how many event rows this process has read from SQLite.
func EventRowsRead() int64 { return eventRowsRead.Load() }

func openSQLite(dsn string) (*sql.DB, error) {
	base, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(countingConnector{base}), nil
}

type countingConnector struct {
	driver.Connector
}

func (c countingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return countingConn{conn}, nil
}

type countingConn struct {
	driver.Conn
}

func (c countingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return countingStmt{stmt}, nil
}

func (c countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := p.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return countingStmt{stmt}, nil
	}
	return c.Prepare(query)
}

func (c countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err := q.QueryContext(ctx, query, args)
		if err != nil {
			return nil, err
		}
		sqlQueriesIssued.Add(1)
		return countingRows{rows}, nil
	}
	return nil, driver.ErrSkip
}

func (c countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c countingConn) CheckNamedValue(nv *driver.NamedValue) error {
	if nvc, ok := c.Conn.(driver.NamedValueChecker); ok {
		return nvc.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c countingConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c countingConn) ResetSession(ctx context.Context) error {
	if s, ok := c.Conn.(driver.SessionResetter); ok {
		return s.ResetSession(ctx)
	}
	return nil
}

func (c countingConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

type countingStmt struct {
	driver.Stmt
}

func (s countingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		return e.ExecContext(ctx, args)
	}
	return nil, driver.ErrSkip
}

func (s countingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err := q.QueryContext(ctx, args)
		if err != nil {
			return nil, err
		}
		sqlQueriesIssued.Add(1)
		return countingRows{rows}, nil
	}
	return nil, driver.ErrSkip
}

func (s countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := s.Stmt.Query(args)
	if err != nil {
		return nil, err
	}
	sqlQueriesIssued.Add(1)
	return countingRows{rows}, nil
}

func (s countingStmt) CheckNamedValue(nv *driver.NamedValue) error {
	if nvc, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return nvc.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type countingRows struct {
	driver.Rows
}

func (r countingRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		sqlRowsRead.Add(1)
	}
	return err
}
