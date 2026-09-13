package config_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getoptimum/optimum-common/internal/endpoints"
	"github.com/getoptimum/optimum-common/pkg/config"
	"github.com/getoptimum/optimum-common/pkg/entities"
	"github.com/getoptimum/optimum-common/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestRenewConfig(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	l := logger.NewAppSLogger(logger.Debug)
	var cfg testConfig
	require.NoError(t, config.Load(&cfg))
	cfg.ChainID = "hoodi"
	cfg.ClusterID = "optimum_hoodi_v0_2"
	cfg.MeshDegreeMin = 1
	cfg.MeshDegreeMax = 1

	received := make(chan *entities.DynamicConfig, 10)
	updater := func(dcCfg *entities.DynamicConfig) {
		received <- dcCfg
	}

	// when (short interval for test)
	cfgRotator := config.NewConfigRotator(
		ctx,
		l,
		&cfg.OptimumConfig,
		cfg.ChainID,
		cfg.ClusterID,
		updater,
		config.WithBootstrapBaseURL(endpoints.DevBootstrapBaseURL),
		config.WithServiceVersion("v0.0.1-rc11"),
		config.WithRenewInterval(2*time.Second),
	)

	// then
	var cfgReceived *entities.DynamicConfig
	select {
	case cfgReceived = <-received:
		require.Equal(t, "hoodi", cfgReceived.ChainID)
		require.Equal(t, "optimum_hoodi_v0_2", cfgReceived.ClusterID)
	case <-time.After(12 * time.Second):
		require.Failf(t, "timeout waiting for config update", "boot node unavailable or config missing for cluster: %s", cfg.ClusterID)
	}
	require.Equal(t, "hoodi", cfgRotator.Get().ChainID)
	require.Equal(t, "optimum_hoodi_v0_2", cfgRotator.Get().ClusterID)
	require.Equal(t, cfgReceived.MeshDegreeMin, cfgRotator.Get().MeshDegreeMin)
	require.Equal(t, cfgReceived.MeshDegreeMax, cfgRotator.Get().MeshDegreeMax)
}

func TestConfigRotatorConcurrentlyTest(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	l := logger.NewAppSLogger(logger.Debug)
	var cfg testConfig
	require.NoError(t, config.Load(&cfg))
	cfg.ClusterID = "optimum_hoodi_v0_2"

	// when
	cfgRotator := config.NewConfigRotator(
		ctx,
		l,
		&cfg.OptimumConfig,
		cfg.ChainID,
		cfg.ClusterID,
		nil,
		config.WithServiceVersion("optimum-common-v0.0.1-rc1"),
	)

	// then
	var wg sync.WaitGroup
	for range 1000 {
		wg.Go(func() {
			for j := range 10000 {
				cfgRotator.RenewConfig(&entities.DynamicConfig{
					MeshDegreeMin: int64(4 + j%10),
				})
			}
		})
	}
	for range 1000 {
		wg.Go(func() {
			for range 10000 {
				_ = cfgRotator.Get().MeshDegreeMin
			}
		})
	}
	wg.Wait()
}

// TestConfigRotatorDoesNotReapplyUnchangedBootConfig checks that a config which has
// not changed since the boot fetch is not applied a second time on the first tick.
func TestConfigRotatorDoesNotReapplyUnchangedBootConfig(t *testing.T) {
	// given a bootstrap endpoint that always answers with the same dynamic config
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chain_id":"hoodi","cluster_id":"optimum_test","mesh_degree_min":4,"mesh_degree_max":8}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var updates atomic.Int64
	baseCfg := &entities.OptimumConfig{ChainID: "hoodi", ClusterID: "optimum_test"}

	// when the rotator has fetched that same config several times
	config.NewConfigRotator(
		ctx,
		logger.NewAppSLogger(logger.Debug),
		baseCfg,
		"hoodi",
		"optimum_test",
		func(*entities.DynamicConfig) { updates.Add(1) },
		config.WithBootstrapBaseURL(srv.URL),
		config.WithRenewInterval(20*time.Millisecond),
	)
	require.Eventually(t, func() bool { return fetches.Load() >= 4 }, 5*time.Second, 10*time.Millisecond)

	// then the updater has been called once, for the boot fetch only
	require.EqualValues(t, 1, updates.Load())
}
