package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	appconfigdatatypes "github.com/aws/aws-sdk-go-v2/service/appconfigdata/types"
	"github.com/luckylanggam/tools-logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/validate"
)

const (
	appID       = "app1"
	environment = "uat"

	catalogJSON = `{
  "schema_version": 1,
  "attributes": {
    "network_id": { "type": "string" },
    "stake": { "type": "number" }
  }
}`

	flagsJSON = `{
  "schema_version": 1,
  "flags": {
    "new_withdraw_flow": {
      "owner": "ivo", "jira": "ATOM-59", "remove_after": "2027-03-01",
      "enabled": true,
      "rules": [{"name": "big stakes", "if": [{"attr": "stake", "op": "gt", "value": 100}], "then": true}],
      "default": false
    }
  }
}`

	// Buckets by an attribute without comparing its value.
	rolloutFlagsJSON = `{
  "schema_version": 1,
  "flags": {
    "new_withdraw_flow": {
      "owner": "ivo", "jira": "ATOM-59", "remove_after": "2027-03-01",
      "enabled": true,
      "rules": [{"name": "10% rollout", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "network_id"}}],
      "default": false
    }
  }
}`

	// Uses an attribute the catalog does not declare.
	badFlagsJSON = `{
  "schema_version": 1,
  "flags": {
    "new_withdraw_flow": {
      "owner": "ivo", "jira": "ATOM-59", "remove_after": "2027-03-01",
      "enabled": true,
      "rules": [{"name": "by site", "if": [{"attr": "site_id", "op": "eq", "value": "s1"}], "then": true}],
      "default": false
    }
  }
}`
)

// fakeProfiles maps profile IDs to names.
type fakeProfiles struct {
	names map[string]string
	err   error
}

func (f fakeProfiles) GetConfigurationProfile(_ context.Context, in *appconfig.GetConfigurationProfileInput, _ ...func(*appconfig.Options)) (*appconfig.GetConfigurationProfileOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &appconfig.GetConfigurationProfileOutput{Name: aws.String(f.names[aws.ToString(in.ConfigurationProfileId)])}, nil
}

// fakeData serves deployed content per profile name. A profile missing from
// deployed answers a Deployment not-found, as AppConfig does when the profile
// has never been deployed. notFound fails every session with that resource
// type instead.
type fakeData struct {
	deployed map[string]string
	err      error
	notFound string
	sessions []appconfigdata.StartConfigurationSessionInput
}

func (f *fakeData) StartConfigurationSession(_ context.Context, in *appconfigdata.StartConfigurationSessionInput, _ ...func(*appconfigdata.Options)) (*appconfigdata.StartConfigurationSessionOutput, error) {
	f.sessions = append(f.sessions, *in)
	if f.err != nil {
		return nil, f.err
	}
	if f.notFound != "" {
		return nil, &appconfigdatatypes.ResourceNotFoundException{
			Message:      aws.String(f.notFound + " not found"),
			ResourceType: appconfigdatatypes.ResourceType(f.notFound),
		}
	}
	profile := aws.ToString(in.ConfigurationProfileIdentifier)
	if _, ok := f.deployed[profile]; !ok {
		return nil, &appconfigdatatypes.ResourceNotFoundException{
			Message:      aws.String("Deployment not found"),
			ResourceType: appconfigdatatypes.ResourceTypeDeployment,
		}
	}
	return &appconfigdata.StartConfigurationSessionOutput{InitialConfigurationToken: aws.String(profile)}, nil
}

func (f *fakeData) GetLatestConfiguration(_ context.Context, in *appconfigdata.GetLatestConfigurationInput, _ ...func(*appconfigdata.Options)) (*appconfigdata.GetLatestConfigurationOutput, error) {
	return &appconfigdata.GetLatestConfigurationOutput{Configuration: []byte(f.deployed[aws.ToString(in.ConfigurationToken)])}, nil
}

var profileNames = map[string]string{"p-flags": flagsProfile, "p-catalog": catalogProfile, "p-other": "settings"}

func newValidator(data *fakeData) *Validator {
	return NewValidator(fakeProfiles{names: profileNames}, data, environment, logger.Mock())
}

func event(profileID, content string) Event {
	return Event{
		ApplicationID:          appID,
		ConfigurationProfileID: profileID,
		ConfigurationVersion:   "7",
		Content:                base64.StdEncoding.EncodeToString([]byte(content)),
	}
}

func TestHandle_FlagsVersion(t *testing.T) {
	cases := map[string]struct {
		content  string
		deployed map[string]string
		wantErr  []string
	}{
		"valid against the deployed catalog": {
			content:  flagsJSON,
			deployed: map[string]string{catalogProfile: catalogJSON},
		},
		"unknown attribute": {
			content:  badFlagsJSON,
			deployed: map[string]string{catalogProfile: catalogJSON},
			wantErr: []string{
				"feature flags are invalid (1 problems):",
				`flags.new_withdraw_flow.rules[0] "by site".if[0].attr: attribute "site_id" is not in the catalog`,
			},
		},
		"no catalog deployed": {
			content:  flagsJSON,
			deployed: map[string]string{},
			wantErr:  []string{"no catalog is deployed in environment uat; deploy the catalog first"},
		},
		"catalog deployed but empty": {
			content:  flagsJSON,
			deployed: map[string]string{catalogProfile: ""},
			wantErr:  []string{"no catalog is deployed in environment uat"},
		},
		"not a flags document": {
			content:  `{"schema_version": 1, "flags": {}, "extra": true}`,
			deployed: map[string]string{catalogProfile: catalogJSON},
			wantErr:  []string{"parse feature-flags document"},
		},
		"deployed catalog does not parse": {
			content:  flagsJSON,
			deployed: map[string]string{catalogProfile: `{"schema_version": 2, "attributes": {}}`},
			wantErr:  []string{"deployed catalog in environment uat does not parse"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := &fakeData{deployed: tc.deployed}
			err := newValidator(data).Handle(context.Background(), event("p-flags", tc.content))
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestHandle_CatalogVersion(t *testing.T) {
	cases := map[string]struct {
		content  string
		deployed map[string]string
		wantErr  []string
	}{
		"no flags deployed yet": {
			content:  catalogJSON,
			deployed: map[string]string{},
		},
		"keeps the deployed flags valid": {
			content:  catalogJSON,
			deployed: map[string]string{flagsProfile: flagsJSON},
		},
		"removes an attribute a deployed flag uses": {
			content:  `{"schema_version": 1, "attributes": {"network_id": {"type": "string"}}}`,
			deployed: map[string]string{flagsProfile: flagsJSON},
			wantErr: []string{
				"this catalog breaks the feature flags deployed in environment uat:",
				`flags.new_withdraw_flow.rules[0] "big stakes".if[0].attr: attribute "stake" is not in the catalog`,
			},
		},
		"retypes an attribute a deployed flag buckets by": {
			content:  `{"schema_version": 1, "attributes": {"network_id": {"type": "integer"}, "stake": {"type": "number"}}}`,
			deployed: map[string]string{flagsProfile: rolloutFlagsJSON, catalogProfile: catalogJSON},
			wantErr: []string{
				"this catalog breaks the feature flags deployed in environment uat:",
				`attribute "network_id" changed type from string to integer`,
			},
		},
		"no catalog deployed yet skips the type comparison": {
			content:  `{"schema_version": 1, "attributes": {"network_id": {"type": "integer"}, "stake": {"type": "number"}}}`,
			deployed: map[string]string{flagsProfile: rolloutFlagsJSON},
		},
		"deployed catalog does not parse": {
			content:  catalogJSON,
			deployed: map[string]string{flagsProfile: flagsJSON, catalogProfile: `{"schema_version": 2, "attributes": {}}`},
			wantErr:  []string{"deployed catalog in environment uat does not parse"},
		},
		"invalid on its own": {
			content:  `{"schema_version": 1, "attributes": {}, "operators": {"eq": {"value_type": "string"}}}`,
			deployed: map[string]string{flagsProfile: flagsJSON},
			wantErr:  []string{`custom operator "eq" has the name of a built-in operator`},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := &fakeData{deployed: tc.deployed}
			err := newValidator(data).Handle(context.Background(), event("p-catalog", tc.content))
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestHandle_ReadsTheOtherProfileInTheConfiguredEnvironment(t *testing.T) {
	data := &fakeData{deployed: map[string]string{catalogProfile: catalogJSON}}
	require.NoError(t, newValidator(data).Handle(context.Background(), event("p-flags", flagsJSON)))

	require.Len(t, data.sessions, 1)
	assert.Equal(t, appID, aws.ToString(data.sessions[0].ApplicationIdentifier))
	assert.Equal(t, environment, aws.ToString(data.sessions[0].EnvironmentIdentifier))
	assert.Equal(t, catalogProfile, aws.ToString(data.sessions[0].ConfigurationProfileIdentifier))
}

func TestHandle_Rejects(t *testing.T) {
	oversize := `{"schema_version": 1, "flags": {}, "pad": "` + strings.Repeat("x", validate.MaxDocumentBytes) + `"}`
	cases := map[string]struct {
		event   Event
		wantErr string
	}{
		"missing application": {
			event:   Event{ConfigurationProfileID: "p-flags", ConfigurationVersion: "7"},
			wantErr: "validator: event is missing applicationId",
		},
		"missing version": {
			event:   Event{ApplicationID: appID, ConfigurationProfileID: "p-flags"},
			wantErr: "validator: event is missing configurationVersion",
		},
		"content not base64": {
			event:   Event{ApplicationID: appID, ConfigurationProfileID: "p-flags", ConfigurationVersion: "7", Content: "%%%"},
			wantErr: "validator: content of version 7 is not base64",
		},
		"another profile": {
			event:   event("p-other", flagsJSON),
			wantErr: `validator: profile "settings" is neither feature-flags nor catalog`,
		},
		"over the AppConfig size limit": {
			event:   event("p-flags", oversize),
			wantErr: "over the AppConfig limit",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := &fakeData{deployed: map[string]string{catalogProfile: catalogJSON}}
			err := newValidator(data).Handle(context.Background(), tc.event)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// AWS errors fail the deployment: the validator never lets a version
// through unchecked.
func TestHandle_AWSErrorsFailClosed(t *testing.T) {
	boom := errors.New("throttled")

	t.Run("profile lookup", func(t *testing.T) {
		v := NewValidator(fakeProfiles{err: boom}, &fakeData{}, environment, logger.Mock())
		err := v.Handle(context.Background(), event("p-flags", flagsJSON))
		require.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "validator: look up configuration profile p-flags")
	})

	t.Run("reading the deployed catalog", func(t *testing.T) {
		v := newValidator(&fakeData{err: boom})
		err := v.Handle(context.Background(), event("p-flags", flagsJSON))
		require.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "validator: read deployed catalog in environment uat")
	})

	// A wrong environment, application or profile is a misconfiguration,
	// not "nothing deployed".
	versions := map[string]Event{"flags": event("p-flags", flagsJSON), "catalog": event("p-catalog", catalogJSON)}
	for _, resource := range []string{"Environment", "Application", "ConfigurationProfile"} {
		for kind, ev := range versions {
			t.Run(resource+" not found validating "+kind, func(t *testing.T) {
				v := newValidator(&fakeData{notFound: resource})
				err := v.Handle(context.Background(), ev)
				var notFound *appconfigdatatypes.ResourceNotFoundException
				require.ErrorAs(t, err, &notFound)
				assert.Contains(t, err.Error(), "validator: read deployed")
			})
		}
	}
}

func assertErr(t *testing.T, err error, want []string) {
	t.Helper()
	if len(want) == 0 {
		assert.NoError(t, err)
		return
	}
	require.Error(t, err)
	for _, w := range want {
		assert.Contains(t, err.Error(), w)
	}
}
