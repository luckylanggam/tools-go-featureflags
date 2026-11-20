# tools-go-featureflags

Feature flags for Pretty Technical Go services. Flags live in AWS AppConfig, one document per project, and are evaluated in memory per caller: kill switch, then ordered rules on the caller's attributes, then a default. Services keep working on the last good version, or on their Helm `FEATURE_*` values, when AppConfig is unreachable.

Moving a service from v0 (Helm-only `IsEnabled(name)`)? Follow the [adoption guide](docs/adoption-guide.md).

---

## 📦 Installation

```bash
go get github.com/luckylanggam/tools-go-featureflags@latest
```

Requires Go 1.25 or later (since v1.1.0).

v1 adds the new API and keeps the v0 one (`NewFeatureFlagManager`, `FeatureFlags`, `PreCheckFeatureFlags`), so upgrading the module changes nothing until a service switches. Services pinned to a v0 pseudo-version keep building too. The v0 constructor, manager and middleware are deprecated and go away in v2.

---

## 🚀 Quick start

```go
import (
    "github.com/luckylanggam/tools-go-featureflags/engine"
    "github.com/luckylanggam/tools-go-featureflags/provider"
)

ff, err := provider.NewProvider(ctx,
    provider.Config{ProviderType: provider.AppConfig, ClientName: "cashier", EnvPrefix: "FEATURE_"},
    provider.AppConfigConfig{Application: "atom", Environment: "uat"},
)
if err != nil {
    log.Fatalf("feature flags: %v", err)
}
defer ff.Close()

on, err := ff.IsEnabled(ctx, "new_cashier", engine.Attributes{"site_id": siteID})
```

- New code depends on `provider.Provider`, or on the one-method `featureflags.Checker` (`IsEnabled(ctx, flag, attrs)`) where that is all it needs; `*engine.Engine` implements both calls too, which makes fixed-value test doubles easy.
- `IsEnabled` and `Evaluate` read an in-memory snapshot and never wait on the network.
- An unknown flag is off and returns an error. Attributes passed in the call win over request attributes in `ctx` (see [request attributes](#custom-operators-resolvers-and-request-attributes)); boot-time checks pass `nil`.

---

## 🔌 Providers (`provider` package)

`provider.NewProvider(ctx, config, providerConfig...)` picks the provider from `Config.ProviderType`:

| Provider | Loads from | Use |
| --- | --- | --- |
| `provider.Env` | `EnvPrefix` variables, read once: `FEATURE_NEW_CASHIER=true` becomes flag `new_cashier`, on for everyone. Any value other than `true` (any case) is off. | Local runs, tests, services not on AppConfig yet. |
| `provider.AppConfig` | The `feature-flags` and `catalog` profiles of `AppConfigConfig.Application` / `Environment`, through the AppConfig Data API. | Services on AppConfig. |

`AppConfigConfig` defaults: `FlagsProfile` `feature-flags`, `CatalogProfile` `catalog`, `PollInterval` 30 s (at least 15 s), `BootTimeout` 5 s, region from the AWS default chain unless `Region` is set. `Fetcher` replaces the Data API client (tests, or the AppConfig Agent).

How the AppConfig provider behaves:

- **Boot:** the first load waits at most `BootTimeout`. If AppConfig cannot be reached by then, the service starts on its `FEATURE_*` values and keeps polling. `NewProvider` fails only on bad configuration.
- **The AppConfig document replaces the env values** once it loads: a flag that is only in `FEATURE_*` is then unknown. Put every flag the service reads in the document before switching it to AppConfig.
- **Polling:** every `PollInterval` in the background. A new version is size-checked, parsed, validated against the catalog served last (`validate.ValidateCatalogChange`) and compiled with the service's registry, then swapped in atomically. A version that fails any step is not served: the previous snapshot stays and `featureflags.poll.failures` counts it. Validation warnings are logged.
- **`Snapshot()`** says what is being served: `Source` (`appconfig`, `cache` after failed polls, `env` before the first successful load), `FlagsVersion`, `CatalogVersion`, `LoadedAt`, `SyncedAt`.
- **`Close()`** stops polling; evaluations keep working on the last snapshot.

Metrics (OpenTelemetry, meter `engine.MeterName`; global meter provider unless `Config.MeterProvider` is set):

| Metric | Type | Attributes |
| --- | --- | --- |
| `featureflags.evaluations` | counter | `flag`, `enabled`, `reason` |
| `featureflags.poll.failures` | counter | `profile`, `failure` (`fetch`, `invalid`) |
| `featureflags.snapshot.age` | gauge, seconds since the source last confirmed the snapshot | `source`, `flags_version`, `catalog_version` |
| `featureflags.operator.duration`, `featureflags.resolver.duration` | histograms, seconds | see below |

The service's IRSA role needs `appconfig:StartConfigurationSession` and `appconfig:GetLatestConfiguration` on both profiles: attach the `reader_policy_arn` of the project's AppConfig module.

---

## 🛡️ HTTP middleware

```go
r.Use(middleware.Attributes(attrsFromRequest))                  // request attributes for every check below
r.With(middleware.RequireFlags(ff, []string{"new_cashier"})).Get("/v2/cashier", h)
```

`RequireFlags` lets a request through only when every listed flag is on for it, evaluated with the request context: 403 when one is off, 500 when one cannot be evaluated (for example, an unknown flag). The v0 `PreCheckFeatureFlags` keeps working on a `FeatureFlagManager`.

---

## 🔁 Moving from v0

Each project builds its services through one `go.work`, so the whole workspace runs one version of this module: services can't move one at a time by pinning versions. v1 therefore keeps the v0 API, and `featureflags.FromProvider(p)` puts a v1 provider behind the v0 `FeatureFlags` interface:

```go
p, err := provider.NewProvider(ctx, provider.Config{ProviderType: provider.AppConfig, ClientName: "cashier", EnvPrefix: "FEATURE_"}, acCfg)
ff := featureflags.FromProvider(p) // pass ff wherever a FeatureFlags went before
```

The adapter lower-cases v0 names (`USER_SIGNUP` checks `user_signup`) and evaluates without attributes, so flags with attribute rules get their default until the call site moves to `p.IsEnabled(ctx, flag, attrs)`. `AllFlags` is not supported. The [adoption guide](docs/adoption-guide.md) has the steps.

---

## 📄 AppConfig documents (`document` package)

Phase 2 stores flags in AWS AppConfig as two freeform JSON documents per project, both `schema_version: 1`:

- **`feature-flags`**: every flag and group of the project in one version, edited in the Flag Console.
- **`catalog`**: the attributes (with types), custom operators and resolvers the project's flags may use, kept in `config/featureflags/catalog.json` and published by CI.

```json
{
  "schema_version": 1,
  "groups": { "canary-1": { "attr": "network_id", "values": ["N-1", "N-2"] } },
  "flags": {
    "new_withdraw_flow": {
      "owner": "marco", "jira": "PHOENIX-900", "remove_after": "2027-03-01",
      "enabled": true,
      "rules": [
        { "name": "canary", "if": [{"attr": "network_id", "op": "in_group", "value": "canary-1"}], "then": true },
        { "name": "10% rollout", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "network_id"} }
      ],
      "default": false
    }
  }
}
```

- `enabled: false` is the kill switch: off for everyone. Otherwise the first matching rule decides, then `default`.
- A rule matches when every condition in `if` holds; an empty `if` matches everyone. Without `rollout`, every matching caller gets `then`; with it, only that stable percentage does and the rest continue to the next rule.
- Names: flags and attributes `lower_snake_case`, groups `lower-kebab-case`, up to 64 characters. Groups hold at most 200 values.

`document.FlagsSchema()` and `document.CatalogSchema()` return the JSON Schemas (draft-04, as AppConfig requires). They check shape only. `document.ParseFlags` and `document.ParseCatalog` decode a document strictly (unknown fields and other schema versions are rejected). Valid and invalid examples are in `document/testdata`.

---

## ⚙️ Rule engine (`engine` package)

`engine.New(flags, catalog, opts...)` compiles a `feature-flags` document against its catalog once; the result is immutable and safe for concurrent use. Evaluation is in memory, with no network or other I/O of its own: only the custom operators and resolvers a service registers run its code.

```go
e, err := engine.New(flagsDoc, catalogDoc)

d, err := e.Evaluate(ctx, "new_withdraw_flow", engine.Attributes{"network_id": "N-1", "player_id": 42})
// d.Enabled, d.Reason ("kill_switch", "rule", "default", "unknown_flag", "decided_by_service"), d.Rule,
// d.MissingAttrs, d.InvalidAttrs, d.FailedOperators, d.FailedResolvers, d.UndecidedOperators

on, err := e.IsEnabled(ctx, "new_withdraw_flow", attrs)
```

- **Order:** kill switch → rules in order (first match wins) → `default`. An unknown flag is off and returns an error.
- **Missing attributes never match**, negated operators included; only `exists` / `not_exists` look at absence. A rollout whose `bucket_by` attribute is missing skips its rule.
- **Types come from the catalog.** Values are converted before comparing: for a string attribute `101` and `"101"` are equal; an integer attribute accepts Go ints, integral floats and integer strings; booleans accept `bool` only. A value that does not convert never matches and is listed in `InvalidAttrs`.
- **Operators:** `eq`, `in`, `in_group`, `gt`, `lt` (integer / number), `starts_with`, `contains`, `matches` (string; Go RE2, unanchored, so write `^…$` to anchor), `exists`, each with a `not_` form.
- **Rollout:** a caller is in when `fnv64a(flag + "\x00" + value) % 100 < percentage`. Raising the percentage only adds callers; the same caller gets the same bucket in every rule of a flag.
- `New` rejects documents it cannot evaluate (unknown attribute, group or operator, wrong value types, bad regex, a custom operator that is not registered). A provider that gets an error keeps its previous engine.

### Custom operators, resolvers and request attributes

Logic that is not data goes in code. Declare each custom operator and resolver in the catalog, register it once at startup and pass the registry to every `New`:

```json
"operators": { "is_vip": { "value_type": "boolean" } },
"resolvers": { "network_id": { "requires": ["player_id"] } }
```

```go
reg := engine.NewRegistry()
err := reg.RegisterOperator("is_vip", func(ctx context.Context, playerID, want any) (bool, error) {
    vip, err := vips.IsVIP(ctx, playerID.(string))
    return vip == want.(bool), err
})
err = reg.RegisterResolver("network_id", func(ctx context.Context, in engine.Attributes) (any, error) {
    return players.NetworkID(ctx, in["player_id"].(string))
})

e, err := engine.New(flagsDoc, catalogDoc, engine.WithRegistry(reg))
```

- **Custom operators** get values already converted to the attribute's type and the operator's `value_type`, and a `not_` form like the built-ins, so their names may not start with `not_`. An error counts as no match and is listed in `FailedOperators`. A document that uses an operator the catalog does not declare, or one the service has not registered, is rejected by `New`.
- **Resolvers** run only when a rule needs their attribute and the caller did not send it, with the attributes listed in `requires`. Within a request each resolver runs at most once per set of inputs, even across goroutines; errors are cached too, leave the attribute missing and are listed in `FailedResolvers`. Without a request scope (below) the cache lasts one `Evaluate`. Resolvers do not feed each other.
- **Request attributes:** `engine.WithAttributes(ctx, attrs)` sets the common attributes once per request and starts its resolver cache; arguments passed to `Evaluate` win over them. Middleware does it for you:

```go
r.Use(middleware.Attributes(func(r *http.Request) engine.Attributes {
    return engine.Attributes{"site_id": r.Header.Get("X-Site-Id")}
}))

grpc.NewServer(
    grpc.ChainUnaryInterceptor(ffgrpc.UnaryServerInterceptor(attrsFromMetadata)),
    grpc.ChainStreamInterceptor(ffgrpc.StreamServerInterceptor(attrsFromMetadata)),
) // ffgrpc = .../middleware/grpc
```

- **Metrics:** OpenTelemetry histograms `featureflags.operator.duration` (`operator`, `outcome`) and `featureflags.resolver.duration` (`attribute`, `outcome`), in seconds, on meter `engine.MeterName`. The global meter provider is used unless `engine.WithMeterProvider` sets one.
- **Tools without the service's code** (the Flag Console) pass `engine.WithUnregisteredOperatorsUndecided()`: a declared but unregistered operator compiles, and a rule that needs it, once its other checks pass, ends evaluation with reason `decided_by_service` and the operators in `UndecidedOperators`. `Enabled` means nothing then. Services must not use this option.

---

## ✅ Validating documents (`validate` package)

The JSON Schemas check shape. `validate` checks everything that crosses references, and is what the Flag Console runs before saving, the AppConfig validator Lambda runs at deployment, and the library runs when a new version arrives:

```go
flags, err := document.ParseFlags(flagsJSON)
// ...
catalog, err := document.ParseCatalog(catalogJSON)
// ...
warnings, err := validate.Validate(flags, catalog)
var invalid *validate.Error
if errors.As(err, &invalid) {
    for _, p := range invalid.Problems {
        fmt.Println(p.Path, p.Message) // flags.new_withdraw_flow.rules[0] "canary".if[0].value group "canary-2" does not exist
    }
}
```

- Rejects: unknown groups, a group over another attribute than the condition's, groups outside 1..200 values or with repeated values, attributes and operators missing from the catalog, values of the wrong type or shape for the operator, invalid regular expressions, bad names, missing owner, bad `jira` / `remove_after`, percentages outside 0..100, and catalog problems (`validate.ValidateCatalog`).
- Values must have the attribute's exact JSON type: a `string` attribute takes `"101"`, not `101`; an `integer` takes `5` or `5.0`, not `5.5`.
- Every problem is reported, in a stable order, each with the path of the flag, rule, condition or group.
- Warnings never block a save; today: two rules of one flag share a name.
- A catalog change is checked with `validate.ValidateCatalogChange(prev, next, flags)`: a removed attribute or operator, or an attribute whose type changed, fails with every flag that uses it. Rollout buckets and `exists` conditions hold no value, so only the previous catalog shows a type change. With no previous catalog (`prev == nil`) it is the same as `Validate`.
- `validate.CheckSize(raw)` checks the stored bytes: an error over the AppConfig quota (2,048 KB), a warning over 75% of it.

---

## 🛡️ AppConfig validator Lambda (`cmd/validator-lambda`)

A `LAMBDA` validator registered on both the `feature-flags` and `catalog` profiles, one per AWS account. AppConfig calls it before every deployment, so an invalid version is rejected whatever path it came from (Flag Console, AWS console, CLI, CI). It runs the same `validate` version as this library:

- **feature-flags version:** size check, then `Validate` against the catalog deployed in the environment. With no catalog deployed it fails: deploy the catalog first.
- **catalog version:** size check, `ValidateCatalog`, then `ValidateCatalogChange` of the deployed flags from the deployed catalog to the new one, so a catalog cannot remove or retype something a live flag uses.

A failed check fails the deployment; AppConfig shows the error text, one `path: message` line per problem. Warnings go to the Lambda's logs. Errors reading AWS also fail the deployment.

| Environment variable | Meaning |
| --- | --- |
| `FEATURE_FLAGS_ENVIRONMENT` | AppConfig environment to read the deployed documents from (e.g. `uat`) |

The execution role needs `appconfig:GetConfigurationProfile` on the application's profiles, and `appconfig:StartConfigurationSession` + `appconfig:GetLatestConfiguration` on both profiles in that environment. AppConfig needs `lambda:InvokeFunction` on the function.

Build for the `provided.al2023` runtime (arm64):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap ./cmd/validator-lambda
zip validator-lambda.zip bootstrap
```

---

## ❗ Errors

Error message formats live in the `errors` package. They are format strings, not sentinel values: to branch on why a flag is off, use `Evaluate` and its `Decision.Reason` (for example `unknown_flag`), and log the error.

---

## 📁 Project structure

```
tools-go-featureflags/
├── ifflags.go              // v0 FeatureFlags interface
├── adapter.go              // Checker, FromProvider (v1 provider behind FeatureFlags)
├── feature_flag_manager.go // v0 env manager (deprecated)
├── provider/               // env and AppConfig providers, snapshot, metrics
├── engine/                 // rule engine, registry, request attributes
├── document/               // AppConfig document types, parsing, JSON Schemas
│   ├── schemas/
│   └── testdata/
├── validate/               // cross-reference checks of the documents
├── cmd/
│   └── validator-lambda/   // AppConfig LAMBDA validator
├── middleware/             // Attributes, RequireFlags, v0 PreCheckFeatureFlags; grpc/ holds the interceptors
├── errors/                 // error message formats
└── docs/
    └── adoption-guide.md   // moving a service to v1
```

---

## 📃 License

License © [Luckylanggam](https://github.com/luckylanggam)
