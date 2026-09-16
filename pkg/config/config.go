// Package config provides a tiny loader that populates a
// configuration struct from multiple sources with clear precedence:
//
//  1. YAML file (if provided via WithYAML)
//  2. Environment variables (optionally prefixed via WithEnvPrefix)
//  3. Flags from a flag.FlagSet (default flag.CommandLine unless overridden via WithFlagSet)
//
// Later sources override earlier ones (flags > env > YAML)
package config

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type loader struct {
	fs        *flag.FlagSet
	path      string
	envPrefix string
}

// Option configures the loader used by Load
// Helpers (WithFlagSet, WithYAML, and WithEnvPrefix) can be used to customize how
// configuration sources are discovered and merged
type Option func(*loader)

// WithFlagSet sets the flag.FlagSet that will be consulted for overrides
// Flags are evaluated last, giving them the highest precedence over
// environment variables and YAML values
func WithFlagSet(fs *flag.FlagSet) Option {
	return func(l *loader) { l.fs = fs }
}

// WithYAML specifies the path to a YAML file to load first (lowest precedence)
// If the file is missing, it is ignored
func WithYAML(path string) Option {
	return func(l *loader) { l.path = path }
}

// WithEnvPrefix sets a prefix for all environment variables
// WithEnvPrefix("APP") causes "HOST" to be read from "APP_HOST"
func WithEnvPrefix(p string) Option {
	return func(l *loader) { l.envPrefix = p }
}

// Load populates cfg by merging configuration from (in order):
// YAML (if provided), environment variables, and flags
func Load(cfg any, opts ...Option) error {
	l := loader{fs: flag.CommandLine}
	for _, o := range opts {
		o(&l)
	}

	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("cfg must be pointer")
	}
	v = v.Elem()
	// we populate defaults first, so they can be overridden by YAML/env/flags
	fields := collectFields(v, nil, l.envPrefix)
	for _, fi := range fields {
		if fi.defaultValue == "" {
			continue
		}
		if err := setValue(fi.value, fi.defaultValue); err != nil {
			return fmt.Errorf("setting default for %q: %w", fi.flagName, err)
		}
	}

	if l.path != "" {
		b, err := os.ReadFile(l.path)
		if err != nil {
			return fmt.Errorf("failed to read config file: %w", err) // fail if YAML path is specified but cannot be read by any reason
		}
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return err
		}
	}

	for _, fi := range fields {
		if val, ok := os.LookupEnv(fi.envName); ok {
			if err := setValue(fi.value, val); err != nil {
				return err
			}
		}
	}

	if !l.fs.Parsed() {
		if err := l.fs.Parse(os.Args[1:]); err != nil {
			return err
		}
	}
	flagMap := make(map[string]reflect.Value)
	for _, fi := range fields {
		flagMap[fi.flagName] = fi.value
	}
	// Visit takes a callback with no error return, so the first failure is captured
	// here and reported after the walk.
	var flagErr error
	l.fs.Visit(func(f *flag.Flag) {
		if flagErr != nil {
			return
		}
		fv, ok := flagMap[f.Name]
		if !ok {
			return
		}
		if err := setValue(fv, f.Value.String()); err != nil {
			flagErr = fmt.Errorf("setting flag %q: %w", f.Name, err)
		}
	})
	if flagErr != nil {
		return flagErr
	}

	return nil
}

type fieldInfo struct {
	value        reflect.Value
	flagName     string
	envName      string
	defaultValue string
}

func collectFields(v reflect.Value, path []string, envPrefix string) []fieldInfo {
	t := v.Type()
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		name := f.Name
		newPath := append(append([]string(nil), path...), name)
		if fv.Kind() == reflect.Struct && f.Type != reflect.TypeFor[time.Time]() {
			out = append(out, collectFields(fv, newPath, envPrefix)...)
			continue
		}
		envName := f.Tag.Get("env")
		if envName == "" {
			parts := make([]string, 0, len(newPath))
			for _, p := range newPath {
				parts = append(parts, strings.ToUpper(p))
			}
			envName = strings.Join(parts, "_")
		}
		if envPrefix != "" {
			envName = envPrefix + "_" + envName
		}
		flagName := f.Tag.Get("flag")
		if flagName == "" {
			parts := make([]string, 0, len(newPath))
			for _, p := range newPath {
				parts = append(parts, strings.ToLower(p))
			}
			flagName = strings.Join(parts, "-")
		}
		out = append(out, fieldInfo{
			value:        fv,
			flagName:     flagName,
			envName:      envName,
			defaultValue: f.Tag.Get("default"),
		})
	}
	return out
}

func setValue(v reflect.Value, s string) error {
	if !v.CanSet() {
		return nil
	}
	switch v.Kind() {
	case reflect.Slice:
		parts := strings.Split(s, ",")
		slice := reflect.MakeSlice(v.Type(), len(parts), len(parts))
		for i, p := range parts {
			if err := setValue(slice.Index(i), strings.TrimSpace(p)); err != nil {
				return err
			}
		}
		v.Set(slice)
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Type() == reflect.TypeFor[time.Duration]() {
			d, err := time.ParseDuration(s)
			if err != nil {
				return err
			}
			v.SetInt(int64(d))
			return nil
		}
		i, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(i)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(u)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetFloat(f)
	default:
		return fmt.Errorf("unsupported kind %s", v.Kind())
	}
	return nil
}
