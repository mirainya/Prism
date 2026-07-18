# Database Migrations

`database/migrations` is the only production schema source. The server never runs GORM AutoMigrate.

## Commands

```bash
prism migrate status
prism migrate up
prism migrate adopt
```

- `status` reports applied and pending managed migrations.
- `up` applies pending managed migrations under a MySQL named lock.
- `adopt` registers a verified legacy database at the managed baseline.

The server refuses to start when the database is legacy, dirty, or has pending migrations.

## Baseline

`20260718_150000_schema_baseline.sql` contains the complete schema for a new database. Earlier SQL files are retained as immutable legacy upgrade history and are not applied to new databases.

An installation created before the baseline must:

1. Stop every Prism HTTP and Worker process.
2. Back up the database.
3. Apply every missing legacy migration through `20260718_120000_add_conversation_turn_context_mode.sql`.
4. Run `prism migrate adopt`.
5. Run `prism migrate up` for later managed migrations.

## Rules

- Never edit or rename an applied migration.
- Use `YYYYMMDD_HHMMSS_description.sql` for every new migration.
- Keep migrations idempotent because MySQL DDL cannot be fully transactional.
- Include the reason, requirement, impact, and deployment constraints in the file header.
- Test both a fresh baseline and repeated execution before release.
