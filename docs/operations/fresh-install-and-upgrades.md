# Fresh installation

The supported shape is a new PostgreSQL 16 database. Migrations create the
runtime projections (`wallets` and `positions`) on that empty database. An
existing, partially shaped database that still lacks either projection is
rejected; no legacy database migration or cutover path is defined by this
installation procedure.

Compose uses a one-shot `bootstrap` job before starting `app`. Bootstrap runs
all migrations, creates/updates the restricted runtime, ledger, and parity
logins, and then exits. The long-lived app has no migration-admin DSN.

Create mode-0600 secret files and point the path-only variables documented in
`.env.example` at them. Password files contain the respective raw passwords.
DSN files contain the complete PostgreSQL URL for their named principal. The
migration DSN must name an administrator able to create roles and schema
objects. Runtime/ledger/parity DSNs must match the login passwords supplied to
bootstrap. Do not commit any of these files.

Validate the configuration without starting services:

```bash
docker compose -f docker-compose.yml config
```

For a non-Compose installation, set the same `*_FILE` variables and run:

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/bootstrap
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/server
```

Bootstrap must complete before server startup. The server verifies each live
principal and exits before workers or listeners if a connection has excessive
authority or cannot assume its dedicated writer role.
