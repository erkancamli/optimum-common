package config_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/getoptimum/optimum-common/pkg/config"
	"github.com/getoptimum/optimum-common/pkg/entities"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Host         string   `yaml:"host" env:"HOST" flag:"host" default:"localhost"`
	Port         int      `yaml:"port" env:"PORT" flag:"port" default:"8080"`
	Debug        bool     `yaml:"debug" env:"DEBUG" flag:"debug" default:"false"`
	Items        []string `yaml:"items" env:"ITEMS" flag:"items" default:"item1,item2"`
	DefaultParam string   `yaml:"default_param" env:"DEFAULT_PARAM" flag:"default_param" default:"defaultValue"`

	entities.OptimumConfig
}

var testConfigEnvKeys = []string{
	"HOST",
	"PORT",
	"DEBUG",
	"ITEMS",
	"DEFAULT_PARAM",
	"CLUSTER_ID",
	"CHAIN_ID",
	"OPTIMUM_MAX_MSG_SIZE",
	"OPTIMUM_RANDOM_MSG_SIZE",
	"OPTIMUM_SHARD_FACTOR",
	"OPTIMUM_SHARD_MULT",
	"OPTIMUM_THRESHOLD",
	"OPTIMUM_MESH_TARGET",
	"OPTIMUM_MESH_MIN",
	"OPTIMUM_MESH_MAX",
	"BOOTSTRAP_PEERS",
}

func unsetTestConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range testConfigEnvKeys {
		if val, ok := os.LookupEnv(key); ok {
			require.NoError(t, os.Unsetenv(key))
			t.Cleanup(func() {
				_ = os.Setenv(key, val)
			})
			continue
		}
		t.Cleanup(func() {
			_ = os.Unsetenv(key)
		})
	}
}

func TestLoadPriority(t *testing.T) {
	unsetTestConfigEnv(t)

	// given
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	yamlData := `
host: yamlhost
port: 8080
debug: false
items:
  - a
  - b
`
	require.NoError(t, os.WriteFile(p, []byte(yamlData), 0o600))

	t.Run("yml should parse correctly", func(t *testing.T) {
		// when
		cfg := testConfig{}
		require.NoError(t, config.Load(&cfg, config.WithYAML(p)))

		// then
		require.Equal(t, "yamlhost", cfg.Host)
		require.Equal(t, 8080, cfg.Port)
		require.False(t, cfg.Debug)
		require.Equal(t, []string{"a", "b"}, cfg.Items)
		require.Equal(t, "defaultValue", cfg.DefaultParam)
	})

	t.Run("env should override yml", func(t *testing.T) {
		t.Setenv("HOST", "envhost")
		t.Setenv("PORT", "9090")
		t.Setenv("DEBUG", "true")
		t.Setenv("ITEMS", "x,y,z")

		// when
		cfg := testConfig{}
		require.NoError(t, config.Load(&cfg, config.WithYAML(p)))

		// then
		require.Equal(t, "envhost", cfg.Host)
		require.Equal(t, 9090, cfg.Port)
		require.True(t, cfg.Debug)
		require.Equal(t, []string{"x", "y", "z"}, cfg.Items)
		require.Equal(t, "defaultValue", cfg.DefaultParam)
	})

	t.Run("flags should override env and yml", func(t *testing.T) {
		t.Setenv("HOST", "envhost")
		t.Setenv("PORT", "9090")
		t.Setenv("DEBUG", "true")
		t.Setenv("ITEMS", "x,y,z")

		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.String("host", "", "")
		fs.Int("port", 0, "")
		fs.Bool("debug", false, "")
		fs.String("items", "", "")
		os.Args = []string{"cmd", "-host", "flaghost", "-debug=true", "-port=9010", "-items=m,n"}

		// when
		cfg := testConfig{}
		require.NoError(t, config.Load(&cfg, config.WithYAML(p), config.WithFlagSet(fs)))

		// then
		require.Equal(t, "flaghost", cfg.Host)
		require.Equal(t, 9010, cfg.Port)
		require.True(t, cfg.Debug)
		require.Equal(t, []string{"m", "n"}, cfg.Items)
	})
}

func TestEnvPrefix(t *testing.T) {
	t.Setenv("APP_HOST", "prefixedhost")
	t.Setenv("APP_PORT", "4242")
	t.Setenv("APP_DEBUG", "true")

	cfg := testConfig{}
	require.NoError(t, config.Load(&cfg, config.WithEnvPrefix("APP")))

	require.Equal(t, "prefixedhost", cfg.Host)
	require.Equal(t, 4242, cfg.Port)
	require.True(t, cfg.Debug)
}

func TestNestedStruct(t *testing.T) {
	type NestedConfig struct {
		LogLevel string `env:"LOG_LEVEL" flag:"log-level"`
	}
	type ComplexConfig struct {
		AppName string `env:"APP_NAME" flag:"app-name"`
		Nested  NestedConfig
	}

	t.Setenv("APP_NAME", "myapp")
	t.Setenv("LOG_LEVEL", "debug")

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("app-name", "", "")
	fs.String("log-level", "", "")
	os.Args = []string{"cmd", "-log-level=info"}

	cfg := ComplexConfig{}
	require.NoError(t, config.Load(&cfg, config.WithFlagSet(fs)))

	require.Equal(t, "myapp", cfg.AppName)
	require.Equal(t, "info", cfg.Nested.LogLevel) // Flag overrides env
}

func TestInvalidTypeConversion(t *testing.T) {
	t.Setenv("PORT", "notanumber")

	cfg := testConfig{}
	err := config.Load(&cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid syntax")
}

func TestInvalidFlagConversion(t *testing.T) {
	type flagConfig struct {
		Port int `flag:"port"`
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("port", "", "port")
	require.NoError(t, fs.Parse([]string{"-port", "notanumber"}))

	cfg := flagConfig{}
	err := config.Load(&cfg, config.WithFlagSet(fs))
	require.Error(t, err)
	require.Contains(t, err.Error(), "port")
	require.Contains(t, err.Error(), "invalid syntax")
	require.Zero(t, cfg.Port)
}

// Visit walks in lexical order, so the first failing flag is the one reported.
func TestInvalidFlagConversionReportsFirstFailure(t *testing.T) {
	type flagConfig struct {
		Alpha int `flag:"alpha"`
		Omega int `flag:"omega"`
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("alpha", "", "alpha")
	fs.String("omega", "", "omega")
	require.NoError(t, fs.Parse([]string{"-alpha", "bad-alpha", "-omega", "bad-omega"}))

	cfg := flagConfig{}
	err := config.Load(&cfg, config.WithFlagSet(fs))
	require.Error(t, err)
	require.Contains(t, err.Error(), "alpha")
	require.NotContains(t, err.Error(), "omega")
}

func TestUnsupportedType(t *testing.T) {
	type UnsupportedConfig struct {
		Data map[string]string `env:"DATA"` // maps not supported
	}

	t.Setenv("DATA", "foo,bar")

	cfg := UnsupportedConfig{}
	err := config.Load(&cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported kind")
}
