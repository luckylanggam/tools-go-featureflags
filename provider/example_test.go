package provider_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	"github.com/luckylanggam/tools-go-featureflags/provider"
)

func ExampleNewProvider_env() {
	os.Setenv("EXAMPLE_FEATURE_NEW_CASHIER", "true")
	defer os.Unsetenv("EXAMPLE_FEATURE_NEW_CASHIER")

	ff, err := provider.NewProvider(context.Background(), provider.Config{
		ProviderType: provider.Env,
		ClientName:   "cashier",
		EnvPrefix:    "EXAMPLE_FEATURE_",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer ff.Close()

	on, err := ff.IsEnabled(context.Background(), "new_cashier", nil)
	fmt.Println(on, err, ff.Snapshot().Source)
	// Output: true <nil> env
}

// The Helm values of a service set FEATUREFLAGS_* next to its FEATURE_*
// fallback values; the service maps them to the provider config.
func ExampleNewProvider_appConfig() {
	ctx := context.Background()

	reg := engine.NewRegistry()
	// reg.RegisterOperator / reg.RegisterResolver for the catalog's entries.

	ff, err := provider.NewProvider(ctx,
		provider.Config{
			ProviderType: provider.ProviderType(os.Getenv("FEATUREFLAGS_PROVIDER")),
			ClientName:   "xtremepush-updater",
			EnvPrefix:    "FEATURE_",
			Registry:     reg,
		},
		provider.AppConfigConfig{
			Application: os.Getenv("FEATUREFLAGS_APPCONFIG_APPLICATION"),
			Environment: os.Getenv("FEATUREFLAGS_APPCONFIG_ENVIRONMENT"),
			Region:      os.Getenv("FEATUREFLAGS_APPCONFIG_REGION"),
		})
	if err != nil {
		log.Fatal(err)
	}
	defer ff.Close()

	d, err := ff.Evaluate(ctx, "xp_multi_tenant_routing", engine.Attributes{"site_id": "7"})
	if err != nil {
		log.Printf("feature flag: %v", err)
	}
	s := ff.Snapshot()
	log.Printf("xp_multi_tenant_routing=%t reason=%s source=%s flags=%s age=%s",
		d.Enabled, d.Reason, s.Source, s.FlagsVersion, time.Since(s.SyncedAt))
}
