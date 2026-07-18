package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
)

// pg_dump/pg_restore can cause PostgreSQL to re-deparse a text array literal as
// either `'value'::character varying::text` instead of
// `'value'::character varying`, or `ARRAY[...]::text[]` instead of the
// equivalent uncast ARRAY. Those trailing casts are implicit in the array's
// text context, so they carry no constraint semantics. Keep every other cast
// and definition detail intact.
var redundantVarcharTextCast = regexp.MustCompile(`::character varying::text\b`)
var redundantVarcharArrayTextCast = regexp.MustCompile(`ARRAY\[(?:'(?:''|[^'])*'::character varying(?:,\s*)?)+\]::text\[\]`)
var backupHexDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var backupIdentityToken = regexp.MustCompile(`^[a-f0-9]{32,128}$`)
var backupToolName = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,80}$`)

func canonicalConstraintDefinition(definition string) string {
	definition = redundantVarcharTextCast.ReplaceAllString(definition, "::character varying")
	return redundantVarcharArrayTextCast.ReplaceAllStringFunc(definition, func(array string) string {
		return strings.TrimSuffix(array, "::text[]")
	})
}

type BackupVerificationManifest struct {
	SchemaVersion, SourceBefore, SourceAfter, TargetFingerprint, DumpChecksum, ManifestChecksum, TargetIdentityToken string
	ToolVersions                                                                                                     map[string]string
	VerifiedAt                                                                                                       time.Time
}

func (s Service) RecordBackupVerification(ctx context.Context, manifest BackupVerificationManifest, principal string) (database.BackupVerification, error) {
	var out database.BackupVerification
	if principal == "" || len(principal) > 200 || manifest.SchemaVersion != "stage08-backup-verification-v2" || !backupHexDigest.MatchString(manifest.SourceBefore) || manifest.SourceBefore != manifest.SourceAfter || manifest.SourceBefore != manifest.TargetFingerprint || !backupHexDigest.MatchString(manifest.DumpChecksum) || !backupHexDigest.MatchString(manifest.ManifestChecksum) || !backupIdentityToken.MatchString(manifest.TargetIdentityToken) || manifest.VerifiedAt.IsZero() || manifest.VerifiedAt.Nanosecond() != 0 {
		return out, fmt.Errorf("complete successful isolated backup verification manifest required")
	}
	if manifest.ToolVersions == nil || len(manifest.ToolVersions) > 50 {
		return out, fmt.Errorf("backup verification tool metadata exceeds 50 entries")
	}
	for name, version := range manifest.ToolVersions {
		if !backupToolName.MatchString(name) || len(version) == 0 || len(version) > 200 {
			return out, fmt.Errorf("backup verification tool metadata is malformed")
		}
	}
	manifestMaterial := strings.Join([]string{manifest.SourceBefore, manifest.DumpChecksum, manifest.TargetIdentityToken, manifest.VerifiedAt.UTC().Format(time.RFC3339)}, "|")
	sum := sha256.Sum256([]byte(manifestMaterial))
	if manifest.ManifestChecksum != hex.EncodeToString(sum[:]) {
		return out, fmt.Errorf("backup verification manifest checksum mismatch")
	}
	tools, err := json.Marshal(manifest.ToolVersions)
	if err != nil {
		return out, fmt.Errorf("marshal backup verification tool metadata: %w", err)
	}
	state, snapshot, _, err := s.loadPersistedAuthority(ctx)
	if err != nil {
		return out, err
	}
	// The database derives the evidence identity and bindings, and validates the
	// persisted cutover authority under definer rights. Runtime never receives
	// table INSERT or SELECT rights for this immutable evidence table.
	if err := s.DB.WithContext(ctx).Raw(`SELECT * FROM record_verified_backup_evidence(?,?,?,?,?,?,?,?,?,?,?)`,
		manifest.SourceBefore, manifest.SourceAfter, manifest.TargetFingerprint, manifest.DumpChecksum,
		manifest.ManifestChecksum, manifest.TargetIdentityToken, string(tools), manifest.VerifiedAt.UTC(), principal,
		snapshot.ID, state.TransitionID,
	).Scan(&out).Error; err != nil {
		return out, err
	}
	if out.ID == "" || out.Status != "verified" {
		return out, fmt.Errorf("backup verification evidence function returned incomplete row")
	}
	return out, nil
}

type CanonicalDatabaseFingerprint struct {
	SchemaVersion string                                `json:"schema_version"`
	Tables        map[string]CanonicalTableFingerprint  `json:"tables"`
	SchemaObjects map[string]CanonicalObjectFingerprint `json:"schema_objects"`
	Digest        string                                `json:"digest"`
}
type CanonicalTableFingerprint struct {
	Count      int      `json:"count"`
	RowDigests []string `json:"row_digests"`
	Digest     string   `json:"digest"`
}
type CanonicalObjectFingerprint struct {
	Count   int      `json:"count"`
	Objects []string `json:"objects"`
	Digest  string   `json:"digest"`
}

func CanonicalRowsDigest(rows map[string][]json.RawMessage) (CanonicalDatabaseFingerprint, error) {
	out := CanonicalDatabaseFingerprint{SchemaVersion: "stage08-canonical-database-v4", Tables: map[string]CanonicalTableFingerprint{}, SchemaObjects: map[string]CanonicalObjectFingerprint{}}
	tables := make([]string, 0, len(rows))
	for table := range rows {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		digests := make([]string, 0, len(rows[table]))
		for _, raw := range rows[table] {
			var value any
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			// json.Number retains the lexical numeric token.  Converting through
			// float64 made distinct large integers and precise NUMERIC values hash
			// identically before the canonical marshal.
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return out, fmt.Errorf("%s: %w", table, err)
			}
			canonical, err := json.Marshal(value)
			if err != nil {
				return out, err
			}
			sum := sha256.Sum256(canonical)
			digests = append(digests, hex.EncodeToString(sum[:]))
		}
		sort.Strings(digests)
		sum := sha256.Sum256([]byte(strings.Join(digests, "\n")))
		out.Tables[table] = CanonicalTableFingerprint{Count: len(digests), RowDigests: digests, Digest: hex.EncodeToString(sum[:])}
	}
	copyOut := out
	copyOut.Digest = ""
	payload, _ := json.Marshal(copyOut)
	sum := sha256.Sum256(payload)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}

func FingerprintDatabase(ctx context.Context, db *gorm.DB) (CanonicalDatabaseFingerprint, error) {
	rows := map[string][]json.RawMessage{}
	var tables []string
	if err := db.WithContext(ctx).Raw(`SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='public' ORDER BY tablename`).Scan(&tables).Error; err != nil {
		return CanonicalDatabaseFingerprint{}, err
	}
	for _, table := range tables {
		var values []string
		query := fmt.Sprintf(`SELECT to_jsonb(t)::text FROM %s t ORDER BY to_jsonb(t)::text`, quoteIdentifier(table))
		if err := db.WithContext(ctx).Raw(query).Scan(&values).Error; err != nil {
			return CanonicalDatabaseFingerprint{}, err
		}
		for _, value := range values {
			rows[table] = append(rows[table], json.RawMessage(value))
		}
		if rows[table] == nil {
			rows[table] = []json.RawMessage{}
		}
	}
	out, err := CanonicalRowsDigest(rows)
	if err != nil {
		return out, err
	}
	objectQueries := map[string]string{
		"schemas":     `SELECT format('%I|owner=%I|acl=%s', n.nspname,r.rolname,COALESCE(n.nspacl::text,'')) FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public' ORDER BY 1`,
		"relations":   `SELECT format('%I|kind=%s|owner=%I|acl=%s|rls=%s|force_rls=%s', c.relname,c.relkind,r.rolname,COALESCE(c.relacl::text,''),c.relrowsecurity,c.relforcerowsecurity) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_roles r ON r.oid=c.relowner WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S','f') ORDER BY 1`,
		"columns":     `SELECT format('%I.%I|ordinal=%s|type=%s|null=%s|default=%s|collation=%s|identity=%s|generated=%s|storage=%s|compression=%s', c.relname,a.attname,a.attnum,pg_catalog.format_type(a.atttypid,a.atttypmod),NOT a.attnotnull,COALESCE(pg_get_expr(ad.adbin,ad.adrelid),''),COALESCE(coll.collname,''),a.attidentity,a.attgenerated,a.attstorage,COALESCE(a.attcompression::text,'')) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_attrdef ad ON ad.adrelid=a.attrelid AND ad.adnum=a.attnum LEFT JOIN pg_catalog.pg_collation coll ON coll.oid=a.attcollation WHERE n.nspname='public' AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum`,
		"constraints": `SELECT format('%I|%I|%s|%s', c.conrelid::regclass::text,c.conname,c.contype,pg_get_constraintdef(c.oid,true)) FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='public' ORDER BY 1`,
		"indexes":     `SELECT format('%I|%I|%s', t.relname,i.relname,pg_get_indexdef(i.oid)) FROM pg_catalog.pg_class t JOIN pg_catalog.pg_index x ON x.indrelid=t.oid JOIN pg_catalog.pg_class i ON i.oid=x.indexrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public' ORDER BY 1`,
		"triggers":    `SELECT format('%I|%I|%s', c.relname,t.tgname,pg_get_triggerdef(t.oid,true)) FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal ORDER BY 1`,
		// A NULL proacl means PostgreSQL's default function ACL. Canonicalize to
		// the effective ACL so it matches an explicitly stored equivalent ACL,
		// while still fingerprinting every grantee, grantor, privilege, and grant
		// option.
		"functions": `SELECT format('%I|%s|owner=%I|acl=%s|security_definer=%s|volatility=%s|strict=%s|config=%s',
			p.proname, pg_get_function_identity_arguments(p.oid), r.rolname,
			COALESCE((SELECT string_agg(format('grantee=%I|grantor=%I|privilege=%s|grantable=%s', COALESCE(grantee.rolname, 'PUBLIC'), grantor.rolname, a.privilege_type, a.is_grantable), ',' ORDER BY COALESCE(grantee.rolname, 'PUBLIC'), grantor.rolname, a.privilege_type, a.is_grantable)
				FROM aclexplode(COALESCE(p.proacl, acldefault('f', p.proowner))) a
				LEFT JOIN pg_catalog.pg_roles grantee ON grantee.oid=a.grantee
				JOIN pg_catalog.pg_roles grantor ON grantor.oid=a.grantor), ''),
			p.prosecdef, p.provolatile, p.proisstrict, COALESCE(array_to_string(p.proconfig, ','), '')) || E'\n' || pg_get_functiondef(p.oid)
			FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace JOIN pg_catalog.pg_roles r ON r.oid=p.proowner WHERE n.nspname='public' ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)`,
		"views":              `SELECT format('%I|%s', c.relname,pg_get_viewdef(c.oid,false)) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('v','m') ORDER BY 1`,
		"sequences":          `SELECT format('%I|type=%s|start=%s|min=%s|max=%s|increment=%s|cycle=%s|cache=%s|last=%s', sequencename,data_type,start_value,min_value,max_value,increment_by,cycle,cache_size,COALESCE(last_value::text,'')) FROM pg_catalog.pg_sequences WHERE schemaname='public' ORDER BY sequencename`,
		"rls_policies":       `SELECT format('%I|%I|permissive=%s|roles=%s|cmd=%s|qual=%s|check=%s', c.relname,p.polname,p.polpermissive,COALESCE(array_to_string(p.polroles,','),''),p.polcmd,COALESCE(pg_get_expr(p.polqual,p.polrelid),''),COALESCE(pg_get_expr(p.polwithcheck,p.polrelid),'')) FROM pg_catalog.pg_policy p JOIN pg_catalog.pg_class c ON c.oid=p.polrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' ORDER BY 1`,
		"default_privileges": `SELECT format('owner=%I|schema=%I|type=%s|acl=%s', r.rolname,COALESCE(n.nspname,''),d.defaclobjtype,COALESCE(d.defaclacl::text,'')) FROM pg_catalog.pg_default_acl d JOIN pg_catalog.pg_roles r ON r.oid=d.defaclrole LEFT JOIN pg_catalog.pg_namespace n ON n.oid=d.defaclnamespace WHERE n.nspname='public' OR d.defaclnamespace=0 ORDER BY 1`,
		"privileges": `SELECT value FROM (
			SELECT format('table|%I|%I|%s|%s', table_name,grantee,privilege_type,is_grantable) value FROM information_schema.role_table_grants WHERE table_schema='public'
			UNION ALL SELECT format('column|%I.%I|%I|%s|%s', table_name,column_name,grantee,privilege_type,is_grantable) FROM information_schema.column_privileges WHERE table_schema='public'
			UNION ALL SELECT format('routine|%I|%I|%s|%s', routine_name,grantee,privilege_type,is_grantable) FROM information_schema.routine_privileges WHERE routine_schema='public'
			UNION ALL SELECT format('usage|%s|%I|%I|%s|%s', object_type,object_name,grantee,privilege_type,is_grantable) FROM information_schema.role_usage_grants WHERE object_schema='public'
			UNION ALL SELECT format('role_membership|%I|%I|admin=%s', member.rolname,parent.rolname,m.admin_option) FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles parent ON parent.oid=m.roleid JOIN pg_catalog.pg_roles member ON member.oid=m.member WHERE parent.rolname LIKE 'trading_bot_%' OR member.rolname LIKE 'trading_bot_%'
		) inventory ORDER BY value`,
		"roles": `SELECT format('%I|login=%s|inherit=%s|superuser=%s|createdb=%s|createrole=%s|replication=%s|bypassrls=%s', rolname,rolcanlogin,rolinherit,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls) FROM pg_catalog.pg_roles WHERE rolname LIKE 'trading_bot_%' ORDER BY rolname`,
	}
	for kind, query := range objectQueries {
		var objects []string
		if err := db.WithContext(ctx).Raw(query).Scan(&objects).Error; err != nil {
			return CanonicalDatabaseFingerprint{}, fmt.Errorf("fingerprint %s: %w", kind, err)
		}
		if kind == "constraints" {
			for i := range objects {
				objects[i] = canonicalConstraintDefinition(objects[i])
			}
		}
		sort.Strings(objects)
		sum := sha256.Sum256([]byte(strings.Join(objects, "\n")))
		out.SchemaObjects[kind] = CanonicalObjectFingerprint{Count: len(objects), Objects: objects, Digest: hex.EncodeToString(sum[:])}
	}
	copyOut := out
	copyOut.Digest = ""
	payload, _ := json.Marshal(copyOut)
	sum := sha256.Sum256(payload)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
