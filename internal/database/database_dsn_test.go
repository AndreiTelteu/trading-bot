package database

import "testing"

func TestLoginFromDSN(t *testing.T) {
	for _, tc := range []struct {
		name string
		dsn  string
		want string
		ok   bool
	}{
		{name: "postgres URL", dsn: "postgres://runtime%20login:secret@db/trading_bot", want: "runtime login", ok: true},
		{name: "postgresql URL", dsn: "postgresql://runtime@db/trading_bot", want: "runtime", ok: true},
		{name: "keyword user", dsn: "service=record user=runtime", want: "runtime", ok: true},
		{name: "quoted and escaped keyword user", dsn: `service=record user='runtime \'quoted\\ login'`, want: "runtime 'quoted\\ login", ok: true},
		{name: "quoted user cannot inject a parameter", dsn: `service=record user='runtime\' service=admin'`, want: "runtime' service=admin", ok: true},
		{name: "escaped unquoted keyword user", dsn: `service=record user=runtime\ login`, want: "runtime login", ok: true},
		{name: "service only", dsn: "service=record", ok: false},
		{name: "URL without user", dsn: "postgres://db/trading_bot", ok: false},
		{name: "malformed keyword", dsn: "service=record user='runtime", ok: false},
		{name: "malformed escape", dsn: "service=record user=runtime\\", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loginFromDSN(tc.dsn)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("loginFromDSN() = %q, %v; want %q, nil", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("loginFromDSN(%q) succeeded with %q", tc.dsn, got)
			}
		})
	}
}
