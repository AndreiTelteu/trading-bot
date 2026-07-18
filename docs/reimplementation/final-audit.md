# Final remediation audit

Review baseline: repository commit `1d90166`. This file describes the current
uncommitted remediation only; it does not claim that the change is committed,
pushed, deployed, or independently accepted.

Implemented boundaries:

- The runtime, ledger, parity, and migration principals are separate. The
  long-lived server does not receive or invoke the migration connection.
- The ledger role has an explicit economic-table allowlist. It has no parity,
  settings, governance, operational-evidence, or blanket public-schema DML.
  Deferred database triggers require wallet/position economic deltas to match
  immutable ledger events inserted by the same PostgreSQL transaction.
- Runtime has column-level authority for the operational `exit_pending`
  claim/release lifecycle, but no wallet or position economic DML.
- Fresh installation is performed by the one-shot Compose bootstrap job. Login
  passwords and DSNs come from mounted secret files; none are embedded in SQL,
  source, Compose defaults, or `.env.example`.
- Missing `wallets` or `positions` is an unsupported schema shape and fails
  migration with a precise error. This remediation supports a new database; it
  does not define a legacy-database cutover procedure.
- Restore verification sets both runtime and migration DSNs to the isolated
  target. Source fingerprint and backup-record actions are open-only and cannot
  invoke migrations.
- Canonical fingerprint v4 includes rows, schemas, relation/function owners and
  ACLs, columns, constraints, indexes, triggers, functions, views, sequences,
  RLS flags/policies, default privileges, and protected-role membership.
- Strategy governance digests are computed from embedded checked-in Go source
  artifacts used by the executable. Positions retain their opening strategy
  identity, and closing fills copy that identity rather than `ModelVersion`.

Verification results must be taken from the completion report for the current
working session. PostgreSQL, restore-client, Compose-render, frontend, race,
deployment, exchange, and operational claims are not implied by this document.
