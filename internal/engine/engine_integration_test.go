//go:build integration

package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	quackridge "github.com/pondpilot/quackridge"
)

func TestQuackIdentityAndShutdown(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	runtime := New()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})

	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = runtime.db.ExecContext(ctx, "CALL enable_logging('Quack')")
	attach := "ATTACH '" + strings.ReplaceAll(endpoint, "'", "''") + "' AS ridge (TYPE quack, TOKEN '" + runtime.Token() + "')"
	if _, err := client.ExecContext(ctx, attach); err != nil {
		rows, _ := runtime.db.QueryContext(ctx, `SELECT query, error FROM duckdb_logs_parsed('Quack')
			WHERE message_type = 'PREPARE_REQUEST' ORDER BY timestamp`)
		defer rows.Close()
		var decisions []string
		for rows.Next() {
			var query, serverError sql.NullString
			_ = rows.Scan(&query, &serverError)
			decisions = append(decisions, query.String+" => "+serverError.String)
		}
		t.Fatalf("%v; server decisions=%q", err, decisions)
	}
	var name string
	var metaValue any
	if err := client.QueryRowContext(ctx, "SELECT name, meta FROM ridge.query('FROM whoami()')").Scan(&name, &metaValue); err != nil {
		var serverQuery, serverError sql.NullString
		_ = runtime.db.QueryRowContext(ctx, `SELECT query, error FROM duckdb_logs_parsed('Quack')
			WHERE message_type = 'PREPARE_REQUEST' ORDER BY timestamp DESC LIMIT 1`).Scan(&serverQuery, &serverError)
		t.Fatalf("%v; server query=%q error=%q", err, serverQuery.String, serverError.String)
	}
	metaBytes, err := json.Marshal(metaValue)
	if err != nil {
		t.Fatal(err)
	}
	meta := string(metaBytes)
	if name != "QuackRidge" || !strings.Contains(meta, `"protocol_version":1`) {
		t.Fatalf("identity name=%q meta=%q", name, meta)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimPrefix(endpoint, "quack:")
	connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatalf("Quack port %s remained open after Stop", address)
	}
}

func TestQuackCancellationAndAbandonedStream(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	runtime := New()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	endpoint, err := runtime.Start(ctx, quackridge.Options{ExtensionDir: extensionDir})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop(context.Background())

	client, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, client, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	attach := "ATTACH '" + endpoint + "' AS ridge (TYPE quack, TOKEN '" + runtime.Token() + "')"
	if _, err := client.ExecContext(ctx, attach); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		var total uint64
		done <- client.QueryRowContext(ctx,
			`SELECT total FROM ridge.query('/* quackridge-query-id:test-cancel */ SELECT sum(i)::UBIGINT AS total FROM range(1000000000000) t(i)')`).Scan(&total)
	}()

	cancelClient, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancelClient.Close()
	for _, extension := range []string{"httpfs", "quack"} {
		if err := loadExtension(ctx, cancelClient, extensionDir+"/"+extension+".duckdb_extension"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cancelClient.ExecContext(ctx,
		"ATTACH '"+endpoint+"' AS cancelridge (TYPE quack, TOKEN '"+runtime.Token()+"')"); err != nil {
		t.Fatal(err)
	}
	var connectionID string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = cancelClient.QueryRowContext(ctx, `SELECT connection_id FROM cancelridge.query(
			'SELECT connection_id FROM quackridge_active_queries_v1() WHERE query_id = ''test-cancel''')`).Scan(&connectionID)
		if err == nil && connectionID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if connectionID == "" {
		var rawID, rawQuery, rawState string
		_ = runtime.QueryRow(ctx, `SELECT connection_id, query, state FROM quack_active_connections()
			WHERE state = 'active' LIMIT 1`).Scan(&rawID, &rawQuery, &rawState)
		t.Fatalf("active query was not discoverable for cancellation: %v; raw id=%q query=%q state=%q", err, rawID, rawQuery, rawState)
	}
	if _, err := cancelClient.ExecContext(ctx, "FROM quack_cancel('cancelridge', ?)", connectionID); err != nil {
		t.Fatalf("send Quack cancellation: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "interrupt")) {
			t.Fatalf("long query cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long query did not stop within the 3s cancellation threshold")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}

	rows, err := client.QueryContext(ctx,
		`SELECT i FROM ridge.query('SELECT i FROM range(1000000000) t(i)')`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("stream returned no first row")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	quickCtx, cancelQuick := context.WithTimeout(ctx, 3*time.Second)
	defer cancelQuick()
	var answer int
	if err := client.QueryRowContext(quickCtx, `SELECT answer FROM ridge.query('SELECT 42 AS answer')`).Scan(&answer); err != nil {
		t.Fatalf("server did not reclaim abandoned stream: %v", err)
	}
	if answer != 42 {
		t.Fatalf("quick answer = %d", answer)
	}
}
