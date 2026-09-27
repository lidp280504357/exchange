// Package config loads service settings from the environment.
//
// Sources, in increasing priority:
//
//  1. defaults: whatever the destination struct holds before Load;
//  2. the .env file in the working directory, for local development only;
//  3. the process environment.
//
// A key is an upper-case environment variable name in lower case
// (POSTGRES_DSN is "postgres_dsn") and maps to a struct field through its
// `koanf` tag. The key space is flat, like the environment itself, so a nested
// section is either embedded or tagged `koanf:",squash"`. An empty value
// counts as unset and keeps the default. Comma-separated values fill slice
// fields; durations use time.ParseDuration syntax; types implementing
// encoding.TextUnmarshaler (Env, slog.Level) parse themselves.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/joho/godotenv"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

// Env names a deployment environment, read from APP_ENV.
type Env string

// Known environments.
const (
	EnvLocal   Env = "local"   // developer machine, infrastructure on the test server
	EnvTest    Env = "test"    // shared test server
	EnvStaging Env = "staging" // canary, reserved for phase 4
	EnvProd    Env = "prod"    // production, reserved for phase 4
)

// UnmarshalText accepts only the known environments.
func (e *Env) UnmarshalText(b []byte) error {
	switch v := Env(b); v {
	case EnvLocal, EnvTest, EnvStaging, EnvProd:
		*e = v
		return nil
	default:
		return fmt.Errorf("unknown environment %q, want local, test, staging or prod", v)
	}
}

// DefaultEnvFile is the dotenv file Load reads when it exists.
const DefaultEnvFile = ".env"

// Loader reads settings from an optional dotenv file and the process
// environment. The zero value reads the process environment only.
type Loader struct {
	// EnvFile is a dotenv file; a missing file is not an error. It is skipped
	// when the process environment sets APP_ENV to anything but local, so a
	// stray file on a server can never supply settings.
	EnvFile string
	// Environ returns the process environment; nil means os.Environ.
	Environ func() []string
}

// Load fills dst from DefaultEnvFile and the process environment.
func Load(dst any) error {
	return Loader{EnvFile: DefaultEnvFile}.Load(dst)
}

// Load fills dst, a pointer to a struct, and then calls dst.Validate if dst
// has that method.
func (l Loader) Load(dst any) error {
	environ := os.Environ
	if l.Environ != nil {
		environ = l.Environ
	}
	vars := environ()

	k := koanf.New(".")
	if l.EnvFile != "" && dotenvAllowed(vars) {
		if err := k.Load(dotenvFile(l.EnvFile), nil); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	processEnv := env.Provider("", env.Opt{
		TransformFunc: normalize,
		EnvironFunc:   func() []string { return vars },
	})
	if err := k.Load(processEnv, nil); err != nil {
		return fmt.Errorf("config: environment: %w", err)
	}

	if err := k.UnmarshalWithConf("", dst, koanf.UnmarshalConf{DecoderConfig: decoderConfig()}); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if v, ok := dst.(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}

// dotenvAllowed reports whether the dotenv file may be read: only when the
// process environment leaves APP_ENV unset or sets it to local.
func dotenvAllowed(environ []string) bool {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "APP_ENV="); ok {
			return strings.TrimSpace(v) == "" || Env(v) == EnvLocal
		}
	}
	return true
}

// normalize turns an environment variable into a config key and value. It
// returns an empty key, which drops the variable, when the value is blank or
// the name is not an upper-case identifier: lower-case duplicates such as
// http_proxy would otherwise race their upper-case twins for the same key.
func normalize(name, value string) (string, any) {
	if strings.TrimSpace(value) == "" || !isEnvName(name) {
		return "", nil
	}
	return strings.ToLower(name), value
}

func isEnvName(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// dotenvFile is a koanf.Provider for an optional dotenv file.
type dotenvFile string

func (f dotenvFile) Read() (map[string]any, error) {
	vars, err := godotenv.Read(string(f))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", string(f), err)
	}
	out := make(map[string]any, len(vars))
	for name, value := range vars {
		if key, v := normalize(name, value); key != "" {
			out[key] = v
		}
	}
	return out, nil
}

func (f dotenvFile) ReadBytes() ([]byte, error) {
	return nil, errors.New("dotenv provider does not support ReadBytes")
}

func decoderConfig() *mapstructure.DecoderConfig {
	return &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),
			mapstructure.StringToTimeDurationHookFunc(),
			splitList,
		),
		WeaklyTypedInput: true,
		Squash:           true,
	}
}

// splitList turns "a, b,,c" into []string{"a", "b", "c"} for slice fields
// other than []byte.
func splitList(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to.Kind() != reflect.Slice || to.Elem().Kind() == reflect.Uint8 {
		return data, nil
	}
	var out []string
	for _, s := range strings.Split(reflect.ValueOf(data).String(), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
