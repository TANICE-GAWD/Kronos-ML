// Package db holds the SQL schema files and applies the Fraud Shield migration.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/003_risk.sql
var riskMigration string

// ApplyRiskMigration creates the Fraud Shield tables and statuses. It is idempotent.
func ApplyRiskMigration(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, riskMigration); err != nil {
		return fmt.Errorf("apply 003_risk.sql: %w", err)
	}
	return nil
}
