package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	migrationfiles "github.com/mirainya/Prism/database/migrations"
	"gorm.io/gorm"
)

const (
	migrationTableName = "prism_schema_migrations"
	migrationLockName  = "prism_schema_migrations"
	migrationLockWait  = 60
)

const createMigrationTableSQL = `CREATE TABLE IF NOT EXISTS prism_schema_migrations (
    version VARCHAR(32) NOT NULL,
    name VARCHAR(255) NOT NULL,
    checksum CHAR(64) NOT NULL,
    dirty TINYINT(1) NOT NULL DEFAULT 0,
    applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

var (
	ErrLegacyDatabase    = errors.New("legacy database is not registered with the migration manager")
	ErrPendingMigrations = errors.New("database has pending migrations")
)

var requiredApplicationTables = []string{
	"users",
	"tokens",
	"channels",
	"channel_accounts",
	"models",
	"endpoints",
	"tasks",
	"channel_request_logs",
	"token_channel_priorities",
	"conversations",
	"messages",
	"conversation_turns",
	"conversation_items",
	"conversation_projection_outbox",
	"billing_logs",
	"api_calls",
	"api_call_attempts",
	"api_call_payloads",
	"balance_entries",
	"api_access_logs",
	"audit_events",
	"account_model_states",
	"account_models",
	"gw_channels",
	"gw_channel_keys",
	"gw_abilities",
	"gw_ability_transports",
	"gw_route_states",
	"gw_model_meta",
	"ai_responses",
	"ai_response_idempotency_cache",
	"ai_files",
}

var requiredBaselineColumns = map[string][]string{
	"api_calls":                      {"project_conversation"},
	"conversations":                  {"canonical_item_count", "canonical_bytes", "canonical_match_hash", "canonical_state_version"},
	"conversation_projection_outbox": {"input_prepared", "context_mode"},
	"conversation_turns":             {"context_mode"},
}

var forbiddenLegacyColumns = map[string][]string{
	"tokens":          {"plain_key"},
	"gw_channel_keys": {"user_id", "token_id", "filename", "purpose", "bytes", "mime_type", "content"},
}

type AppliedMigration struct {
	Version   string    `gorm:"column:version"`
	Name      string    `gorm:"column:name"`
	Checksum  string    `gorm:"column:checksum"`
	Dirty     bool      `gorm:"column:dirty"`
	AppliedAt time.Time `gorm:"column:applied_at"`
}

type Status struct {
	Legacy  bool
	Applied []AppliedMigration
	Pending []Migration
}

func EnsureCurrent(ctx context.Context, db *gorm.DB) error {
	status, err := Inspect(ctx, db)
	if err != nil {
		return err
	}
	if status.Legacy {
		return fmt.Errorf("%w: apply legacy SQL through 20260718_120000 and run `prism migrate adopt`", ErrLegacyDatabase)
	}
	if len(status.Pending) > 0 {
		return fmt.Errorf("%w: next=%s; run `prism migrate up`", ErrPendingMigrations, status.Pending[0].Filename)
	}
	return nil
}

func Inspect(ctx context.Context, db *gorm.DB) (Status, error) {
	migrations, err := Load()
	if err != nil {
		return Status{}, err
	}
	if !db.Migrator().HasTable(migrationTableName) {
		legacy, err := hasAnyApplicationTable(db)
		if err != nil {
			return Status{}, err
		}
		return Status{Legacy: legacy, Pending: migrations}, nil
	}
	applied, err := loadAppliedWithGORM(ctx, db)
	if err != nil {
		return Status{}, err
	}
	if len(applied) == 0 {
		legacy, err := hasAnyApplicationTable(db)
		if err != nil {
			return Status{}, err
		}
		if legacy {
			return Status{Legacy: true, Pending: migrations}, nil
		}
	}
	pending, err := reconcileMigrations(migrations, applied, false)
	if err != nil {
		return Status{}, err
	}
	return Status{Applied: applied, Pending: pending}, nil
}

func Up(ctx context.Context, db *gorm.DB) ([]Migration, error) {
	migrations, err := Load()
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get migration database: %w", err)
	}
	connection, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get migration connection: %w", err)
	}
	defer connection.Close()
	if err := acquireMigrationLock(ctx, connection); err != nil {
		return nil, err
	}
	defer releaseMigrationLock(connection)

	migrationTableExists, err := hasTableWithConnection(ctx, connection, migrationTableName)
	if err != nil {
		return nil, err
	}
	if !migrationTableExists {
		legacy, err := hasAnyApplicationTableWithConnection(ctx, connection)
		if err != nil {
			return nil, err
		}
		if legacy {
			return nil, fmt.Errorf("%w: run `prism migrate adopt` after applying the legacy SQL files", ErrLegacyDatabase)
		}
	}
	if _, err := connection.ExecContext(ctx, createMigrationTableSQL); err != nil {
		return nil, fmt.Errorf("create migration table: %w", err)
	}
	applied, err := loadAppliedWithConnection(ctx, connection)
	if err != nil {
		return nil, err
	}
	if migrationTableExists && len(applied) == 0 {
		legacy, err := hasAnyApplicationTableWithConnection(ctx, connection)
		if err != nil {
			return nil, err
		}
		if legacy {
			return nil, fmt.Errorf("%w: empty migration history on a non-empty database", ErrLegacyDatabase)
		}
	}
	pending, err := reconcileMigrations(migrations, applied, true)
	if err != nil {
		return nil, err
	}
	appliedByVersion := make(map[string]AppliedMigration, len(applied))
	for _, record := range applied {
		appliedByVersion[record.Version] = record
	}
	completed := make([]Migration, 0, len(pending))
	for _, migration := range pending {
		record, exists := appliedByVersion[migration.Version]
		if !exists {
			if _, err := connection.ExecContext(
				ctx,
				"INSERT INTO prism_schema_migrations (version, name, checksum, dirty) VALUES (?, ?, ?, 1)",
				migration.Version,
				migration.Name,
				migration.Checksum,
			); err != nil {
				return completed, fmt.Errorf("mark migration %s dirty: %w", migration.Filename, err)
			}
		} else if !record.Dirty {
			return completed, fmt.Errorf("migration %s unexpectedly became pending", migration.Filename)
		}
		if _, err := connection.ExecContext(ctx, migration.SQL); err != nil {
			return completed, fmt.Errorf("apply migration %s: %w", migration.Filename, err)
		}
		result, err := connection.ExecContext(
			ctx,
			"UPDATE prism_schema_migrations SET dirty = 0, applied_at = CURRENT_TIMESTAMP(3) WHERE version = ? AND checksum = ?",
			migration.Version,
			migration.Checksum,
		)
		if err != nil {
			return completed, fmt.Errorf("mark migration %s applied: %w", migration.Filename, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return completed, fmt.Errorf("read migration %s update result: %w", migration.Filename, err)
		}
		if rowsAffected != 1 {
			return completed, fmt.Errorf("mark migration %s applied: affected %d rows", migration.Filename, rowsAffected)
		}
		completed = append(completed, migration)
	}
	return completed, nil
}

func Adopt(ctx context.Context, db *gorm.DB) (Migration, error) {
	migrations, err := Load()
	if err != nil {
		return Migration{}, err
	}
	baseline := migrations[0]
	sqlDB, err := db.DB()
	if err != nil {
		return Migration{}, fmt.Errorf("get migration database: %w", err)
	}
	connection, err := sqlDB.Conn(ctx)
	if err != nil {
		return Migration{}, fmt.Errorf("get migration connection: %w", err)
	}
	defer connection.Close()
	if err := acquireMigrationLock(ctx, connection); err != nil {
		return Migration{}, err
	}
	defer releaseMigrationLock(connection)

	migrationTableExists, err := hasTableWithConnection(ctx, connection, migrationTableName)
	if err != nil {
		return Migration{}, err
	}
	if migrationTableExists {
		applied, err := loadAppliedWithConnection(ctx, connection)
		if err != nil {
			return Migration{}, err
		}
		if _, err := reconcileMigrations(migrations, applied, false); err != nil {
			return Migration{}, err
		}
		for _, record := range applied {
			if record.Version == baseline.Version {
				return baseline, nil
			}
		}
	}
	if err := validateLegacyBaselineWithConnection(ctx, connection); err != nil {
		return Migration{}, err
	}
	if _, err := connection.ExecContext(ctx, createMigrationTableSQL); err != nil {
		return Migration{}, fmt.Errorf("create migration table: %w", err)
	}
	if _, err := connection.ExecContext(
		ctx,
		"INSERT INTO prism_schema_migrations (version, name, checksum, dirty) VALUES (?, ?, ?, 0)",
		baseline.Version,
		baseline.Name,
		baseline.Checksum,
	); err != nil {
		return Migration{}, fmt.Errorf("record adopted baseline: %w", err)
	}
	return baseline, nil
}

func reconcileMigrations(available []Migration, applied []AppliedMigration, retryDirty bool) ([]Migration, error) {
	availableByVersion := make(map[string]Migration, len(available))
	for _, migration := range available {
		availableByVersion[migration.Version] = migration
	}
	appliedByVersion := make(map[string]AppliedMigration, len(applied))
	for _, record := range applied {
		migration, exists := availableByVersion[record.Version]
		if !exists {
			return nil, fmt.Errorf("database migration %s is newer than or unknown to this binary", record.Version)
		}
		if record.Name != migration.Name {
			return nil, fmt.Errorf("migration name mismatch for %s", migration.Filename)
		}
		if record.Checksum != migration.Checksum {
			return nil, fmt.Errorf("migration checksum mismatch for %s", migration.Filename)
		}
		if record.Dirty {
			if retryDirty {
				appliedByVersion[record.Version] = record
				continue
			}
			return nil, fmt.Errorf("migration %s is dirty; run `prism migrate up` to retry", migration.Filename)
		}
		appliedByVersion[record.Version] = record
	}
	pending := make([]Migration, 0, len(available)-len(applied))
	foundPending := false
	for _, migration := range available {
		record, exists := appliedByVersion[migration.Version]
		if !exists || record.Dirty {
			foundPending = true
			pending = append(pending, migration)
			continue
		}
		if foundPending {
			return nil, fmt.Errorf("migration history is out of order at %s", migration.Filename)
		}
	}
	return pending, nil
}

func loadAppliedWithGORM(ctx context.Context, db *gorm.DB) ([]AppliedMigration, error) {
	var applied []AppliedMigration
	if err := db.WithContext(ctx).Table(migrationTableName).Order("version").Find(&applied).Error; err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	return applied, nil
}

func loadAppliedWithConnection(ctx context.Context, connection *sql.Conn) ([]AppliedMigration, error) {
	rows, err := connection.QueryContext(ctx, "SELECT version, name, checksum, dirty, applied_at FROM prism_schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()
	var applied []AppliedMigration
	for rows.Next() {
		var record AppliedMigration
		if err := rows.Scan(&record.Version, &record.Name, &record.Checksum, &record.Dirty, &record.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied = append(applied, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func acquireMigrationLock(ctx context.Context, connection *sql.Conn) error {
	var acquired sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", migrationLockName, migrationLockWait).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return errors.New("timed out waiting for the migration lock")
	}
	return nil
}

func releaseMigrationLock(connection *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released sql.NullInt64
	_ = connection.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&released)
}

func hasAnyApplicationTable(db *gorm.DB) (bool, error) {
	for _, table := range requiredApplicationTables {
		if db.Migrator().HasTable(table) {
			return true, nil
		}
	}
	return false, nil
}

func hasTableWithConnection(ctx context.Context, connection *sql.Conn, table string) (bool, error) {
	var count int
	err := connection.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ? AND table_type = 'BASE TABLE'",
		table,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return count > 0, nil
}

func hasAnyApplicationTableWithConnection(ctx context.Context, connection *sql.Conn) (bool, error) {
	for _, table := range requiredApplicationTables {
		exists, err := hasTableWithConnection(ctx, connection, table)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func hasColumnWithConnection(ctx context.Context, connection *sql.Conn, table, column string) (bool, error) {
	var count int
	err := connection.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?",
		table,
		column,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	return count > 0, nil
}

func hasIndexWithConnection(ctx context.Context, connection *sql.Conn, table, index string) (bool, error) {
	var count int
	err := connection.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?",
		table,
		index,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check index %s.%s: %w", table, index, err)
	}
	return count > 0, nil
}

func validateLegacyBaselineWithConnection(ctx context.Context, connection *sql.Conn) error {
	for _, table := range requiredApplicationTables {
		exists, err := hasTableWithConnection(ctx, connection, table)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("legacy database is missing required table %s", table)
		}
	}
	for table, columns := range requiredBaselineColumns {
		for _, column := range columns {
			exists, err := hasColumnWithConnection(ctx, connection, table, column)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("legacy database is missing required column %s.%s", table, column)
			}
		}
	}
	for table, columns := range forbiddenLegacyColumns {
		for _, column := range columns {
			exists, err := hasColumnWithConnection(ctx, connection, table, column)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("legacy database still contains retired column %s.%s", table, column)
			}
		}
	}
	exists, err := hasIndexWithConnection(ctx, connection, "conversations", "idx_conversations_canonical_match")
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("legacy database is missing index idx_conversations_canonical_match")
	}
	return nil
}

func BaselineVersion() string {
	return migrationfiles.BaselineVersion
}
