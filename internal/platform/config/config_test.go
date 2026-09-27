package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type section struct {
	DSN string `koanf:"postgres_dsn"`
}

type embedded struct {
	Brokers []string `koanf:"kafka_brokers"`
}

type settings struct {
	embedded
	Postgres section `koanf:",squash"`

	Env     Env           `koanf:"app_env"`
	Addr    string        `koanf:"http_addr"`
	Timeout time.Duration `koanf:"shutdown_timeout"`
	Level   slog.Level    `koanf:"log_level"`
	Retries int           `koanf:"retries"`
	Debug   bool          `koanf:"debug"`
}

type validated struct {
	Addr string `koanf:"http_addr"`
}

var errNoAddr = errors.New("HTTP_ADDR is required")

func (v *validated) Validate() error {
	if v.Addr == "" {
		return errNoAddr
	}
	return nil
}

func environ(vars ...string) func() []string {
	return func() []string { return vars }
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadKeepsDefaultsWithoutSources(t *testing.T) {
	got := settings{Addr: ":8080", Timeout: 10 * time.Second}
	if err := (Loader{Environ: environ()}).Load(&got); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != ":8080" || got.Timeout != 10*time.Second {
		t.Fatalf("defaults lost: %+v", got)
	}
}

func TestLoadDecodesTypes(t *testing.T) {
	var got settings
	err := Loader{Environ: environ(
		"APP_ENV=test",
		"HTTP_ADDR=:9000",
		"SHUTDOWN_TIMEOUT=1500ms",
		"LOG_LEVEL=debug",
		"RETRIES=3",
		"DEBUG=true",
		"KAFKA_BROKERS= a:9092, b:9092,,",
		"POSTGRES_DSN=postgres://u@h/db?sslmode=disable",
	)}.Load(&got)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := settings{
		embedded: embedded{Brokers: []string{"a:9092", "b:9092"}},
		Postgres: section{DSN: "postgres://u@h/db?sslmode=disable"},
		Env:      EnvTest,
		Addr:     ":9000",
		Timeout:  1500 * time.Millisecond,
		Level:    slog.LevelDebug,
		Retries:  3,
		Debug:    true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestLoadPriority(t *testing.T) {
	file := writeEnvFile(t, `
# comment
APP_ENV=local
HTTP_ADDR=:7000
RETRIES=5
POSTGRES_DSN="postgres://from-file"
`)
	var got settings
	err := Loader{EnvFile: file, Environ: environ("HTTP_ADDR=:7001")}.Load(&got)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != ":7001" {
		t.Fatalf("process environment must override the file: addr = %q", got.Addr)
	}
	if got.Env != EnvLocal || got.Retries != 5 || got.Postgres.DSN != "postgres://from-file" {
		t.Fatalf("file values missing: %+v", got)
	}
}

func TestLoadTreatsEmptyValuesAsUnset(t *testing.T) {
	file := writeEnvFile(t, "HTTP_ADDR=:7000\nSHUTDOWN_TIMEOUT=\nLOG_LEVEL=\"\"\n")
	got := settings{Timeout: 10 * time.Second, Level: slog.LevelWarn}
	err := Loader{EnvFile: file, Environ: environ("HTTP_ADDR=", "RETRIES=  ")}.Load(&got)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != ":7000" {
		t.Fatalf("empty process value must not hide the file value: addr = %q", got.Addr)
	}
	if got.Timeout != 10*time.Second || got.Level != slog.LevelWarn || got.Retries != 0 {
		t.Fatalf("empty values must keep defaults: %+v", got)
	}
}

func TestLoadIgnoresNonUpperCaseNames(t *testing.T) {
	var got settings
	err := Loader{Environ: environ("http_addr=:1", "Http_Addr=:2", "HTTP.ADDR=:3", "1HTTP_ADDR=:4")}.Load(&got)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != "" {
		t.Fatalf("only upper-case names may set keys: addr = %q", got.Addr)
	}
}

func TestLoadEnvFileOnlyForLocal(t *testing.T) {
	file := writeEnvFile(t, "APP_ENV=local\nHTTP_ADDR=:7000\n")
	cases := map[string]struct {
		environ  []string
		wantAddr string
	}{
		"app env unset":  {nil, ":7000"},
		"app env empty":  {[]string{"APP_ENV="}, ":7000"},
		"app env local":  {[]string{"APP_ENV=local"}, ":7000"},
		"app env test":   {[]string{"APP_ENV=test"}, ""},
		"app env prod":   {[]string{"APP_ENV=prod"}, ""},
		"app env typo'd": {[]string{"APP_ENV=Local"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var got validated
			err := Loader{EnvFile: file, Environ: environ(tc.environ...)}.Load(&got)
			if tc.wantAddr == "" {
				if !errors.Is(err, errNoAddr) {
					t.Fatalf("file must be skipped: err = %v, addr = %q", err, got.Addr)
				}
				return
			}
			if err != nil || got.Addr != tc.wantAddr {
				t.Fatalf("file must be read: err = %v, addr = %q", err, got.Addr)
			}
		})
	}
}

func TestLoadMissingEnvFileIsFine(t *testing.T) {
	var got settings
	err := Loader{EnvFile: filepath.Join(t.TempDir(), "absent.env"), Environ: environ("HTTP_ADDR=:1")}.Load(&got)
	if err != nil || got.Addr != ":1" {
		t.Fatalf("err = %v, addr = %q", err, got.Addr)
	}
}

func TestLoadMalformedEnvFile(t *testing.T) {
	file := writeEnvFile(t, "HTTP_ADDR\n")
	var got settings
	if err := (Loader{EnvFile: file, Environ: environ()}).Load(&got); err == nil {
		t.Fatal("malformed file must fail")
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"unknown env":  "APP_ENV=qa",
		"bad duration": "SHUTDOWN_TIMEOUT=10",
		"bad level":    "LOG_LEVEL=verbose",
		"bad int":      "RETRIES=three",
		"bad bool":     "DEBUG=maybe",
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			var got settings
			err := Loader{Environ: environ(kv)}.Load(&got)
			if err == nil {
				t.Fatalf("%s must fail, got %+v", kv, got)
			}
			if !strings.HasPrefix(err.Error(), "config: ") {
				t.Fatalf("error must be prefixed: %v", err)
			}
		})
	}
}

func TestLoadCallsValidate(t *testing.T) {
	var got validated
	err := Loader{Environ: environ()}.Load(&got)
	if !errors.Is(err, errNoAddr) {
		t.Fatalf("want errNoAddr, got %v", err)
	}
	if err := (Loader{Environ: environ("HTTP_ADDR=:1")}).Load(&got); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestEnvUnmarshalText(t *testing.T) {
	for _, s := range []string{"local", "test", "staging", "prod"} {
		var e Env
		if err := e.UnmarshalText([]byte(s)); err != nil || string(e) != s {
			t.Fatalf("%s: env = %q, err = %v", s, e, err)
		}
	}
	var e Env
	if err := e.UnmarshalText([]byte("production")); err == nil {
		t.Fatal("unknown environment must fail")
	}
}
