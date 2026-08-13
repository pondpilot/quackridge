//go:build integration

package engine

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	protocol "github.com/pondpilot/quackridge/protocol/v2"
)

func protocolClient(t *testing.T, logger *slog.Logger) (context.Context, *Runtime, *sql.DB) {
	t.Helper()
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	runtime := New()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.ExecContext(ctx, "ATTACH '"+endpoint+"' AS ridge (TYPE quack, TOKEN '"+runtime.Token()+"')"); err != nil {
		t.Fatal(err)
	}
	return ctx, runtime, client
}

func remoteQuery(query string) string {
	return "FROM ridge.query('" + strings.ReplaceAll(query, "'", "''") + "')"
}

func executeRemote(ctx context.Context, connection *sql.Conn, query string) error {
	rows, err := connection.QueryContext(ctx, remoteQuery(query))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func TestQuackSessionPreservesTransactionsAndTemporaryObjects(t *testing.T) {
	ctx, _, client := protocolClient(t, nil)
	connection, err := client.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, query := range []string{
		"BEGIN TRANSACTION",
		"CREATE TEMP TABLE qr_tmp_rollback AS SELECT 99 AS value",
		"ROLLBACK",
	} {
		if err := executeRemote(ctx, connection, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if err := executeRemote(ctx, connection, "SELECT * FROM qr_tmp_rollback"); err == nil {
		t.Fatal("rolled-back temporary table remained visible")
	}
	if err := executeRemote(ctx, connection, "CREATE TEMP TABLE qr_tmp_session AS SELECT 21 AS value UNION ALL SELECT 21"); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := connection.QueryRowContext(ctx, remoteQuery("SELECT sum(value)::INTEGER AS total FROM qr_tmp_session")).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 42 {
		t.Fatalf("temporary table total = %d", total)
	}
}

func TestProtocolMetadataTypesQueryIDsAndStableFailures(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, _, client := protocolClient(t, logger)

	rows, err := client.QueryContext(ctx, remoteQuery("DESCRIBE SELECT * FROM quackridge_metadata_v2()"))
	if err != nil {
		t.Fatal(err)
	}
	var columns []protocol.Column
	for rows.Next() {
		var name, columnType, null string
		var key sql.NullString
		var defaultValue, extra any
		if err := rows.Scan(&name, &columnType, &null, &key, &defaultValue, &extra); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, protocol.Column{Name: name, DuckDBType: columnType})
	}
	_ = rows.Close()
	if len(columns) != len(protocol.MetadataColumns) {
		t.Fatalf("metadata columns = %#v", columns)
	}
	for index := range columns {
		if columns[index] != protocol.MetadataColumns[index] {
			t.Fatalf("metadata column %d = %#v, want %#v", index, columns[index], protocol.MetadataColumns[index])
		}
	}

	const queryID = "pondpilot_q_123"
	var answer int
	if err := client.QueryRowContext(ctx, remoteQuery("/* quackridge-query-id:"+queryID+" */ SELECT 42 AS answer")).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != 42 || !strings.Contains(logs.String(), `"query_id":"`+queryID+`"`) {
		t.Fatalf("query ID was not correlated: answer=%d logs=%s", answer, logs.String())
	}

	_, err = client.ExecContext(ctx, remoteQuery("SELECT * FROM read_text('/private/secret')"))
	classified := quackridge.ClassifyError(err)
	if err == nil || !quackridge.IsCode(classified, quackridge.CodeRejectedStatement) {
		t.Fatalf("rejected server error = %v, classified = %v", err, classified)
	}
}

func TestQuackResultContractAndLargeStreamingBatch(t *testing.T) {
	ctx, _, client := protocolClient(t, nil)
	query := `SELECT [1, 2] AS nested_list, {'value': 7} AS nested_struct,
		123.45::DECIMAL(10,2) AS decimal_value,
		TIMESTAMP '2026-08-11 12:34:56' AS timestamp_value,
		TIMESTAMPTZ '2026-08-11 12:34:56-04' AS timestamptz_value,
		INTERVAL '1 day 2 hours' AS interval_value,
		UUID '550e8400-e29b-41d4-a716-446655440000' AS uuid_value,
		NULL::INTEGER AS null_value, 1 AS duplicate_name, 2 AS duplicate_name`
	rows, err := client.QueryContext(ctx, remoteQuery(query))
	if err != nil {
		t.Fatal(err)
	}
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 10 || names[8] != "duplicate_name" || names[9] != "duplicate_name_1" {
		t.Fatalf("result columns = %v", names)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	typeNames := make([]string, len(columnTypes))
	for index, columnType := range columnTypes {
		typeNames[index] = strings.ToUpper(columnType.DatabaseTypeName())
	}
	for index, fragment := range []string{"[]", "STRUCT", "DECIMAL", "TIMESTAMP", "TIMESTAMP", "INTERVAL", "UUID", "INTEGER"} {
		if !strings.Contains(typeNames[index], fragment) {
			t.Fatalf("result type %d = %q, want fragment %q; all=%v", index, typeNames[index], fragment, typeNames)
		}
	}
	values := make([]any, len(names))
	destinations := make([]any, len(names))
	for index := range values {
		destinations[index] = &values[index]
	}
	if !rows.Next() || rows.Scan(destinations...) != nil {
		t.Fatalf("complex result scan failed: %v", rows.Err())
	}
	if values[7] != nil {
		t.Fatalf("NULL result = %#v", values[7])
	}
	_ = rows.Close()

	empty, err := client.QueryContext(ctx, remoteQuery("SELECT 1 AS value WHERE false"))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Next() {
		t.Fatal("empty result returned a row")
	}
	_ = empty.Close()

	large, err := client.QueryContext(ctx, remoteQuery("SELECT i FROM range(70000) AS values(i)"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for large.Next() {
		var value int64
		if err := large.Scan(&value); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 70000 {
		t.Fatalf("streamed rows = %d", count)
	}
}
