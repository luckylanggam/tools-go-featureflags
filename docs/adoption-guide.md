# Adopting feature flags v1

How to move a Go service from the v0 library (Helm `FEATURE_*` variables, `IsEnabled(name)`) to v1 (flags in AWS AppConfig, evaluated per caller). The [README](../README.md) is the API reference; this guide is the order of work.

This library is for engineering flags: switches that ship, roll out and then get removed with the code behind them. Tenant configuration (which sites a tenant has, what they are entitled to) is not a flag: services read it through the Tenant Management shared reader, which does not use AppConfig. Don't model tenant settings as flags here.

Services and libraries that need AppConfig flag data must go through this library, not call the AppConfig APIs directly.

Three things shape the migration:

- **One version per workspace.** Each project builds every service through one `go.work`, and Go picks the highest version of this module that any service asks for. Bumping one service bumps them all. That is why v1 keeps the v0 API: upgrading the module changes nothing until a service switches.
- **An adapter for v0 code.** `featureflags.FromProvider(p)` puts a v1 provider behind the v0 `FeatureFlags` interface, so a service can move to AppConfig without touching its call sites, mocks or shared packages.
- **Call sites move at your pace.** A call through the adapter checks the flag for everyone (no attributes). Move a call site to `p.IsEnabled(ctx, flag, attrs)` when its flag needs targeting.

So a service moves in two releases, each easy to roll back:

1. **Swap the constructor.** Build a `provider.Provider` on the env provider and pass `featureflags.FromProvider(p)` where the manager went. Same flags, same Helm values, same behaviour.
2. **Switch to AppConfig.** The service reads its project's AppConfig document and keeps its `FEATURE_*` values only as the boot fallback.

## v0 and v1 side by side

| v0 (deprecated, removed in v2) | v1 |
| --- | --- |
| `featureflags.NewFeatureFlagManager(&featureflags.Config{Prefix, ClientName})` | `provider.NewProvider(ctx, provider.Config{ProviderType, ClientName, EnvPrefix, Registry}, provider.AppConfigConfig{...})` |
| `ff.IsEnabled("NEW_CASHIER")` | `p.IsEnabled(ctx, "new_cashier", attrs)`: names are lower case, and every check gets a context and the caller's attributes |
| `featureflags.FeatureFlags` | still there for v0 code; wrap a provider with `featureflags.FromProvider(p)`. New code takes `provider.Provider` or `featureflags.Checker` |
| `AreAllEnabled`, `AllFlags` | `AreAllEnabled` works through the adapter; `AllFlags` doesn't (log `p.Snapshot()` instead) |
| `middleware.PreCheckFeatureFlags(*FeatureFlagManager, flags)` | `middleware.RequireFlags(featureflags.Checker, flags)`, evaluated with the request context |
| Flags change on deploy | Flags change when a new AppConfig version is deployed; services pick it up within one poll (30 s by default) |

## Release 1: swap the constructor

### 1. Bump the module

The library needs Go 1.25 or later (since v1.1.0). Move the project's `go.work`, build image and CI images to Go 1.25 first: the workspace selects one version for every module, and the official `golang` images don't download a newer toolchain.

Once per project, in every module (the workspace runs one version anyway):

```bash
go get github.com/luckylanggam/tools-go-featureflags@latest
go mod tidy
```

This changes nothing on its own: v0 code keeps compiling and behaving as before.

### 2. Replace the manager with a provider

```go
p, err := provider.NewProvider(ctx, provider.Config{
    ProviderType: provider.Env,
    ClientName:   "free-spins-service",
    EnvPrefix:    "FEATURE_",
})
if err != nil {
    log.Fatalf("failed to initialize feature flags: %v", err)
}
defer p.Close()

ffMgr := featureflags.FromProvider(p) // where NewFeatureFlagManager's result went
```

Everything that took the manager or a `FeatureFlags` (config, services, shared packages with their own `IsEnabled(string) (bool, error)` interface) gets `ffMgr` and works unchanged. The adapter lower-cases names, so `IsEnabled("XP_MULTI_TENANT_ROUTING")` checks `xp_multi_tenant_routing`, the name the env provider gives `FEATURE_XP_MULTI_TENANT_ROUTING`. Swap `middleware.PreCheckFeatureFlags` for `middleware.RequireFlags(p, flags)` with lower-case names.

Release this on its own. Helm values and flag behaviour are unchanged, so any difference in production is an upgrade bug, not a flag change.

### 3. Move call sites when they need targeting

A call through the adapter is evaluated without attributes, so a flag with rules on attributes gives its `default`. When a flag needs targeting (by site, network, player), move that call site to the provider:

- Keep `p` (`provider.Provider`, or `featureflags.Checker` if the code only checks flags) next to, or instead of, the v0 `FeatureFlags`.
- On a request path, pass the request context and what the code knows about the caller: `p.IsEnabled(ctx, "new_cashier", engine.Attributes{"site_id": siteID})`. At boot or in background jobs, pass the job's context and `nil`.
- An unknown flag is still an error, and the flag is off. Keep logging it.
- In tests, an env provider or an `engine.Engine` built from a small document is often simpler than a mock (see `middleware/require_test.go`).

## Release 2: switch to AppConfig

### 4. Check the AppConfig resources exist

The project's AppConfig module (infra) creates, per account:

- an application named after the project (`atom`, `phoenix`, ...) and an environment named after the account (`uat`, `prod`);
- the `feature-flags` and `catalog` profiles, with their JSON Schemas and the validator;
- `reader_policy_arn`, the IAM policy a service needs to read both profiles.

If your project has none yet, ask whoever owns the feature-flags epic before going further.

### 5. Declare the attributes in the catalog

Each project keeps one catalog at `config/featureflags/catalog.json`. It lists every attribute a rule may target, with its type, plus the project's custom operators and resolvers:

```json
{
  "schema_version": 1,
  "attributes": {
    "site_id":    { "type": "string",  "description": "Tenant site" },
    "player_id":  { "type": "string" },
    "network_id": { "type": "string" }
  },
  "operators": {},
  "resolvers": {
    "network_id": { "requires": ["player_id"], "description": "Looks up the player's network" }
  }
}
```

- Names are `lower_snake_case`, up to 64 characters. Types are `string`, `integer`, `number` or `boolean`. Custom operator names may not start with `not_`: every operator already has a `not_` form.
- The catalog lives in the repo because an entry is only true once the code behind it is deployed. A shared CI workflow publishes it to the `catalog` profile on merge; the Flag Console only reads it.
- Use one name for one thing across all of a project's services. Rules match on names, so `site_id` in one service and `siteId` in another are two attributes.

### 6. Send attributes once per request

Set the request's common attributes in middleware, so call sites only pass what is specific to them:

```go
// HTTP
r.Use(middleware.Attributes(func(r *http.Request) engine.Attributes {
    return engine.Attributes{"site_id": r.Header.Get("X-Site-Id")}
}))

// gRPC (ffgrpc = .../middleware/grpc)
grpc.NewServer(
    grpc.ChainUnaryInterceptor(ffgrpc.UnaryServerInterceptor(attrsFromMetadata)),
    grpc.ChainStreamInterceptor(ffgrpc.StreamServerInterceptor(attrsFromMetadata)),
)
```

For Kafka consumers and jobs, call `engine.WithAttributes(ctx, attrs)` once per message. Attributes passed to `IsEnabled` win over those in the context. A rule that needs an attribute nobody sent doesn't match (only `exists` / `not_exists` look at absence), so a missing attribute falls through to the next rule or the default. `Evaluate` lists such attributes in `Decision.MissingAttrs` (a failed `exists` included), which is the first thing to check when a rule doesn't match as expected.

### 7. Register custom operators and resolvers (only if the catalog declares any)

```go
reg := engine.NewRegistry()
if err := reg.RegisterResolver("network_id", lookupNetwork); err != nil {
    log.Fatalf("feature flags: %v", err)
}
```

Pass `reg` as `provider.Config.Registry`. A document that uses an operator the service has not registered is rejected, and the service keeps its previous snapshot. The README's [custom operators section](../README.md#custom-operators-resolvers-and-request-attributes) covers the contracts, caching and metrics.

### 8. Put every flag the service reads in the `feature-flags` document

Once AppConfig loads, its document **replaces** the `FEATURE_*` values: a flag that is only in Helm is unknown, and therefore off with an error. Before switching a service, add each of its flags to the document with the value it has today:

```json
"xp_multi_tenant_routing": {
  "owner": "atom", "jira": "ATOM-123", "remove_after": "2027-06-30",
  "enabled": true, "default": true
}
```

`owner`, `jira` (the ticket that removes the flag) and `remove_after` are required on every flag. A flag with no `rules` is the same for every caller, which is what Phase 1 flags are. Add rules later, when you need targeting. The document format is in the README.

### 9. Give the service read access (IRSA)

Attach the module's `reader_policy_arn` to the service's IRSA role, the one in `serviceAccount.annotations."eks.amazonaws.com/role-arn"` of its Helm values. It grants only `appconfig:StartConfigurationSession` and `appconfig:GetLatestConfiguration` on the two profiles. Without it, the service starts on its `FEATURE_*` values, logs a warning and counts every poll in `featureflags.poll.failures`.

### 10. Configure Helm

Use the same variable names in every project, and keep the `FEATURE_*` values: they are what the service serves if AppConfig is unreachable at boot.

```yaml
env:
  - name: FEATUREFLAGS_PROVIDER              # env | appconfig
    value: appconfig
  - name: FEATUREFLAGS_APPCONFIG_APPLICATION # the project
    value: atom
  - name: FEATUREFLAGS_APPCONFIG_ENVIRONMENT # the account's environment
    value: uat
  - name: FEATUREFLAGS_APPCONFIG_REGION      # optional; default chain otherwise
    value: eu-west-2
  - name: FEATURE_XP_MULTI_TENANT_ROUTING    # boot fallback
    value: 'true'
```

The library does not read these variables itself. Map them in the service's config:

```go
ff, err := provider.NewProvider(ctx,
    provider.Config{
        ProviderType: provider.ProviderType(cfg.FeatureFlagsProvider), // FEATUREFLAGS_PROVIDER
        ClientName:   "xtremepush-updater",
        EnvPrefix:    "FEATURE_",
        Registry:     reg,
    },
    provider.AppConfigConfig{
        Application: cfg.FeatureFlagsApplication, // FEATUREFLAGS_APPCONFIG_APPLICATION
        Environment: cfg.FeatureFlagsEnvironment, // FEATUREFLAGS_APPCONFIG_ENVIRONMENT
        Region:      cfg.FeatureFlagsRegion,      // FEATUREFLAGS_APPCONFIG_REGION
    })
```

Setting `FEATUREFLAGS_PROVIDER` back to `env` is the rollback: it needs no code change.

### 11. Release and check

After the deploy, check each pod:

- **Log:** a warning `AppConfig <app>/<env> not loaded at boot` means the pod is serving its `FEATURE_*` values. Check IRSA (step 9) and the application and environment names (step 10).
- **`Snapshot()`:** log it at startup, or expose it on an internal endpoint. `Source` should read `appconfig`, with the deployed `FlagsVersion`.
- **Metrics:** `featureflags.snapshot.age` should stay below about two poll intervals, and `featureflags.poll.failures` should stay flat. `featureflags.evaluations` by `reason` shows unknown flags (`unknown_flag`) and rules matching (`rule`). To check `featureflags.evaluations` for a flag that is evaluated rarely, query it with time aggregation `max`: `rate` and `increase` need two points in a series and show nothing for a flag evaluated once.

Then change one flag's value in AppConfig and confirm the service follows within a poll.

### 12. Alert

On each service, at minimum:

- `featureflags.snapshot.age` above 5 minutes for 5 minutes: the service is serving a stale or fallback snapshot.
- `featureflags.poll.failures` increasing for 5 minutes, any `failure`: AppConfig can't be read (`fetch`, usually IAM or a network problem), or a version reached AppConfig that this service rejects (`invalid`), usually a custom operator that is not registered, a catalog that is behind, or a catalog that changes the type of an attribute the flags use.

Keep both. A pod restarted while AppConfig is unreachable starts its snapshot age from boot, so the stale alert goes quiet for up to about 10 minutes after a restart. The poll-failures alert keeps firing through it.

## Checklist

- [ ] Release 1: module on v1 across the workspace, env provider behind `FromProvider`, released with no behaviour change.
- [ ] Call sites whose flags need targeting moved to `p.IsEnabled(ctx, flag, attrs)`.
- [ ] `config/featureflags/catalog.json` lists every attribute, operator and resolver the project's rules use.
- [ ] Request attributes set once per request (HTTP, gRPC, consumers).
- [ ] Every flag the service reads is in the `feature-flags` document, with owner, ticket and removal date.
- [ ] IRSA role has `reader_policy_arn`.
- [ ] Helm sets `FEATUREFLAGS_*` and keeps `FEATURE_*` as the fallback.
- [ ] After the deploy, `Snapshot().Source` is `appconfig`, a flag change reaches the service, and alerts are in place.
