package policy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/duckdb/duckdb-go/v2/mapping"
)

var tempTablePattern = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?TEMP(?:ORARY)?\s+TABLE\s+(qr_tmp_[a-z0-9_]{1,55})\s+AS\s+((?:SELECT|WITH)\b.*)$`)

var forbiddenFunctions = map[string]struct{}{
	"getenv": {}, "glob": {}, "http_get": {}, "http_post": {},
	"postgres_attach": {}, "postgres_execute": {}, "postgres_query": {}, "postgres_scan": {},
	"query": {}, "query_table": {}, "read_blob": {}, "read_csv": {}, "read_csv_auto": {},
	"read_json": {}, "read_json_auto": {}, "read_ndjson": {}, "read_parquet": {}, "read_text": {},
	"setvariable": {}, "which_secret": {},
}

var allowedTableFunctions = map[string]struct{}{
	"duckdb_columns": {}, "duckdb_databases": {}, "duckdb_schemas": {}, "duckdb_tables": {}, "duckdb_types": {}, "duckdb_views": {},
	"generate_series": {}, "quackridge_active_queries_v1": {}, "quackridge_metadata_v1": {}, "range": {}, "unnest": {}, "whoami": {},
}

// Evaluator owns an isolated DuckDB parser. It never executes submitted SQL.
// DuckDB's C parser proves there is exactly one statement; SELECT trees are then
// serialized and recursively inspected before the callback returns.
type Evaluator struct {
	mu       sync.Mutex
	jsonDB   *sql.DB
	parseDB  mapping.Database
	parseCon mapping.Connection
}

func NewEvaluator() (*Evaluator, error) {
	jsonDB, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, err
	}
	jsonDB.SetMaxOpenConns(1)
	var parseDB mapping.Database
	if mapping.Open("", &parseDB) != mapping.StateSuccess {
		_ = jsonDB.Close()
		return nil, errors.New("open policy parser")
	}
	var parseCon mapping.Connection
	if mapping.Connect(parseDB, &parseCon) != mapping.StateSuccess {
		mapping.Close(&parseDB)
		_ = jsonDB.Close()
		return nil, errors.New("connect policy parser")
	}
	return &Evaluator{jsonDB: jsonDB, parseDB: parseDB, parseCon: parseCon}, nil
}

func (e *Evaluator) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	mapping.Disconnect(&e.parseCon)
	mapping.Close(&e.parseDB)
	return e.jsonDB.Close()
}

func (e *Evaluator) Allow(ctx context.Context, query string) bool {
	if len(query) == 0 || len(query) > 1<<20 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isSingleStatement(query) {
		return false
	}
	if tree, ok := e.selectTree(ctx, query); ok {
		return inspectTree(tree)
	}
	if matches := tempTablePattern.FindStringSubmatch(query); matches != nil {
		tree, ok := e.selectTree(ctx, matches[2])
		return ok && inspectTree(tree)
	}
	return e.isTransaction(query)
}

func (e *Evaluator) isSingleStatement(query string) bool {
	var extracted mapping.ExtractedStatements
	count := mapping.ExtractStatements(e.parseCon, query, &extracted)
	defer mapping.DestroyExtracted(&extracted)
	return count == 1
}

func (e *Evaluator) isTransaction(query string) bool {
	var extracted mapping.ExtractedStatements
	count := mapping.ExtractStatements(e.parseCon, query, &extracted)
	defer mapping.DestroyExtracted(&extracted)
	if count != 1 {
		return false
	}
	var prepared mapping.PreparedStatement
	if mapping.PrepareExtractedStatement(e.parseCon, extracted, 0, &prepared) != mapping.StateSuccess {
		mapping.DestroyPrepare(&prepared)
		return false
	}
	defer mapping.DestroyPrepare(&prepared)
	return mapping.PreparedStatementType(prepared) == mapping.StatementTypeTransaction
}

func (e *Evaluator) selectTree(ctx context.Context, query string) (map[string]any, bool) {
	var serialized string
	err := e.jsonDB.QueryRowContext(ctx,
		"SELECT CAST(json_serialize_sql(CAST(? AS VARCHAR)) AS VARCHAR)", query).Scan(&serialized)
	if err != nil {
		return nil, false
	}
	var tree map[string]any
	if json.Unmarshal([]byte(serialized), &tree) != nil {
		return nil, false
	}
	parseError, _ := tree["error"].(bool)
	statements, _ := tree["statements"].([]any)
	return tree, !parseError && len(statements) == 1
}

func inspectTree(value any) bool {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			if !inspectTree(child) {
				return false
			}
		}
	case map[string]any:
		if functionName, ok := node["function_name"].(string); ok {
			name := strings.ToLower(functionName)
			if _, denied := forbiddenFunctions[name]; denied || strings.HasPrefix(name, "postgres_") || strings.HasPrefix(name, "read_") || strings.HasPrefix(name, "http_") {
				return false
			}
		}
		if nodeType, _ := node["type"].(string); nodeType == "TABLE_FUNCTION" {
			function, _ := node["function"].(map[string]any)
			name, _ := function["function_name"].(string)
			if _, allowed := allowedTableFunctions[strings.ToLower(name)]; !allowed {
				return false
			}
		}
		if nodeType, _ := node["type"].(string); nodeType == "BASE_TABLE" {
			for _, field := range []string{"catalog_name", "schema_name", "table_name"} {
				if unsafeObjectName(node[field]) {
					return false
				}
			}
		}
		for _, child := range node {
			if !inspectTree(child) {
				return false
			}
		}
	}
	return true
}

func unsafeObjectName(value any) bool {
	name, _ := value.(string)
	lower := strings.ToLower(name)
	return strings.ContainsAny(name, `/\\`) || strings.Contains(lower, "://") ||
		strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".parquet") || strings.HasSuffix(lower, ".duckdb")
}

type Handle struct {
	connection *sql.Conn
	evaluator  *Evaluator
}

func Install(ctx context.Context, db *sql.DB) (*Handle, error) {
	evaluator, err := NewEvaluator()
	if err != nil {
		return nil, err
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		_ = evaluator.Close()
		return nil, err
	}
	function := &authorizationFunction{evaluator: evaluator}
	if err := duckdb.RegisterScalarUDF(connection, "quackridge_authorize", function); err != nil {
		_ = connection.Close()
		_ = evaluator.Close()
		return nil, err
	}
	return &Handle{connection: connection, evaluator: evaluator}, nil
}

func (h *Handle) Close() error {
	return errors.Join(h.connection.Close(), h.evaluator.Close())
}

type authorizationFunction struct{ evaluator *Evaluator }

func (*authorizationFunction) Config() duckdb.ScalarFuncConfig {
	varchar, _ := duckdb.NewTypeInfo(duckdb.TYPE_VARCHAR)
	boolean, _ := duckdb.NewTypeInfo(duckdb.TYPE_BOOLEAN)
	return duckdb.ScalarFuncConfig{InputTypeInfos: []duckdb.TypeInfo{varchar, varchar}, ResultTypeInfo: boolean}
}

func (f *authorizationFunction) Executor() duckdb.ScalarFuncExecutor {
	return duckdb.ScalarFuncExecutor{RowExecutor: func(values []driver.Value) (any, error) {
		query, ok := values[1].(string)
		if !ok {
			return false, nil
		}
		return f.evaluator.Allow(context.Background(), query), nil
	}}
}
