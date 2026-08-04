package postgrescore

import (
	"context"
	"database/sql/driver"
	"strconv"
	"strings"
)

// rebindConnector keeps the core repositories on database/sql while adapting
// their long-standing SQLite-style placeholders to PostgreSQL. It is scoped to
// the PostgreSQL core connection; SQLite connections never pass through it.
type rebindConnector struct {
	base driver.Connector
}

func (c *rebindConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rebindConn{Conn: conn}, nil
}

func (c *rebindConnector) Driver() driver.Driver {
	return &rebindDriver{base: c.base.Driver()}
}

type rebindDriver struct {
	base driver.Driver
}

func (d *rebindDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &rebindConn{Conn: conn}, nil
}

type rebindConn struct {
	driver.Conn
}

func (c *rebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rewritePostgresSQL(query))
}

func (c *rebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, rewritePostgresSQL(query))
	}
	return c.Prepare(query)
}

func (c *rebindConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *rebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, rewritePostgresSQL(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, rewritePostgresSQL(query), args)
	}
	return nil, driver.ErrSkip
}

func (c *rebindConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}

func (c *rebindConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		return conn.ResetSession(ctx)
	}
	return nil
}

func (c *rebindConn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}

func (c *rebindConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func rewritePostgresSQL(query string) string {
	// The inspection lease deliberately uses the database clock so a connection
	// wait cannot shorten a newly issued lease. Preserve that property on PG.
	query = strings.ReplaceAll(
		query,
		"cast(unixepoch('subsec') * 1000 as integer)",
		"cast(extract(epoch from clock_timestamp()) * 1000 as bigint)",
	)
	return rebindQuestionMarks(query)
}

func rebindQuestionMarks(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	next := maxDollarPlaceholder(query) + 1
	var output strings.Builder
	output.Grow(len(query) + 16)
	for index := 0; index < len(query); {
		switch {
		case query[index] == '\'':
			end := quotedEnd(query, index, '\'')
			output.WriteString(query[index:end])
			index = end
		case query[index] == '"':
			end := quotedEnd(query, index, '"')
			output.WriteString(query[index:end])
			index = end
		case index+1 < len(query) && query[index:index+2] == "--":
			end := lineCommentEnd(query, index)
			output.WriteString(query[index:end])
			index = end
		case index+1 < len(query) && query[index:index+2] == "/*":
			end := blockCommentEnd(query, index)
			output.WriteString(query[index:end])
			index = end
		case query[index] == '$':
			if delimiter, ok := dollarQuoteDelimiter(query[index:]); ok {
				end := dollarQuoteEnd(query, index, delimiter)
				output.WriteString(query[index:end])
				index = end
				continue
			}
			output.WriteByte(query[index])
			index++
		case query[index] == '?':
			output.WriteByte('$')
			output.WriteString(strconv.Itoa(next))
			next++
			index++
		default:
			output.WriteByte(query[index])
			index++
		}
	}
	return output.String()
}

func maxDollarPlaceholder(query string) int {
	maxValue := 0
	for index := 0; index < len(query); {
		switch {
		case query[index] == '\'':
			index = quotedEnd(query, index, '\'')
		case query[index] == '"':
			index = quotedEnd(query, index, '"')
		case index+1 < len(query) && query[index:index+2] == "--":
			index = lineCommentEnd(query, index)
		case index+1 < len(query) && query[index:index+2] == "/*":
			index = blockCommentEnd(query, index)
		case query[index] == '$':
			if delimiter, ok := dollarQuoteDelimiter(query[index:]); ok {
				index = dollarQuoteEnd(query, index, delimiter)
				continue
			}
			end := index + 1
			for end < len(query) && query[end] >= '0' && query[end] <= '9' {
				end++
			}
			if end > index+1 {
				if value, err := strconv.Atoi(query[index+1 : end]); err == nil && value > maxValue {
					maxValue = value
				}
				index = end
				continue
			}
			index++
		default:
			index++
		}
	}
	return maxValue
}

func quotedEnd(query string, start int, quote byte) int {
	for index := start + 1; index < len(query); index++ {
		if query[index] != quote {
			continue
		}
		if index+1 < len(query) && query[index+1] == quote {
			index++
			continue
		}
		return index + 1
	}
	return len(query)
}

func lineCommentEnd(query string, start int) int {
	if newline := strings.IndexByte(query[start:], '\n'); newline >= 0 {
		return start + newline + 1
	}
	return len(query)
}

func blockCommentEnd(query string, start int) int {
	depth := 1
	for index := start + 2; index < len(query); {
		switch {
		case index+1 < len(query) && query[index:index+2] == "/*":
			depth++
			index += 2
		case index+1 < len(query) && query[index:index+2] == "*/":
			depth--
			index += 2
			if depth == 0 {
				return index
			}
		default:
			index++
		}
	}
	return len(query)
}

func dollarQuoteDelimiter(fragment string) (string, bool) {
	if len(fragment) < 2 || fragment[0] != '$' {
		return "", false
	}
	if fragment[1] == '$' {
		return "$$", true
	}
	if !isIdentifierStart(fragment[1]) {
		return "", false
	}
	for index := 2; index < len(fragment); index++ {
		if fragment[index] == '$' {
			return fragment[:index+1], true
		}
		if !isIdentifierPart(fragment[index]) {
			return "", false
		}
	}
	return "", false
}

func dollarQuoteEnd(query string, start int, delimiter string) int {
	contentStart := start + len(delimiter)
	if relative := strings.Index(query[contentStart:], delimiter); relative >= 0 {
		return contentStart + relative + len(delimiter)
	}
	return len(query)
}

func isIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isIdentifierPart(value byte) bool {
	return isIdentifierStart(value) || value >= '0' && value <= '9'
}
