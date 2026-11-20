package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// Fetcher fetches the latest deployed content of an AppConfig configuration
// profile. It is the only part of the appconfig provider that talks to AWS,
// so another transport (for example the AppConfig Agent on localhost) can
// replace the Data API without changing parsing, validation or evaluation.
type Fetcher interface {
	// Fetch returns the content and version of profile. When the content is
	// unchanged since this fetcher's previous successful call for profile,
	// it returns changed=false and no data.
	Fetch(ctx context.Context, profile string) (data []byte, version string, changed bool, err error)
}

// DataAPI is the part of the AppConfig Data client the fetcher uses.
type DataAPI interface {
	StartConfigurationSession(ctx context.Context, params *appconfigdata.StartConfigurationSessionInput, optFns ...func(*appconfigdata.Options)) (*appconfigdata.StartConfigurationSessionOutput, error)
	GetLatestConfiguration(ctx context.Context, params *appconfigdata.GetLatestConfigurationInput, optFns ...func(*appconfigdata.Options)) (*appconfigdata.GetLatestConfigurationOutput, error)
}

// DataAPIFetcher fetches profiles through the AppConfig Data API, keeping
// one configuration session per profile. Each poll token is used once; a
// failed call drops the session so the next Fetch starts a new one, which
// also recovers from tokens that expired after 24 hours.
type DataAPIFetcher struct {
	client       DataAPI
	application  string
	environment  string
	pollInterval int32

	mu       sync.Mutex
	tokens   map[string]*string
	versions map[string]string
}

// NewDataAPIFetcher returns a fetcher for one AppConfig application and
// environment. pollIntervalSeconds is passed to AppConfig as the minimum
// poll interval; the caller must not poll a profile more often.
func NewDataAPIFetcher(client DataAPI, application, environment string, pollIntervalSeconds int32) *DataAPIFetcher {
	return &DataAPIFetcher{
		client:       client,
		application:  application,
		environment:  environment,
		pollInterval: pollIntervalSeconds,
		tokens:       map[string]*string{},
		versions:     map[string]string{},
	}
}

// Fetch implements Fetcher.
func (f *DataAPIFetcher) Fetch(ctx context.Context, profile string) ([]byte, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	token, ok := f.tokens[profile]
	if !ok {
		out, err := f.client.StartConfigurationSession(ctx, &appconfigdata.StartConfigurationSessionInput{
			ApplicationIdentifier:                aws.String(f.application),
			EnvironmentIdentifier:                aws.String(f.environment),
			ConfigurationProfileIdentifier:       aws.String(profile),
			RequiredMinimumPollIntervalInSeconds: aws.Int32(f.pollInterval),
		})
		if err != nil {
			return nil, "", false, fmt.Errorf(ffErrors.ErrAppConfigStartSession, profile, err)
		}
		token = out.InitialConfigurationToken
	}

	out, err := f.client.GetLatestConfiguration(ctx, &appconfigdata.GetLatestConfigurationInput{ConfigurationToken: token})
	if err != nil {
		delete(f.tokens, profile)
		return nil, "", false, fmt.Errorf(ffErrors.ErrAppConfigGetLatest, profile, err)
	}
	f.tokens[profile] = out.NextPollConfigurationToken

	// An empty body means the client already has the latest version.
	if len(out.Configuration) == 0 {
		return nil, f.versions[profile], false, nil
	}
	version := aws.ToString(out.VersionLabel)
	if version == "" {
		version = contentVersion(out.Configuration)
	}
	f.versions[profile] = version
	return out.Configuration, version, true, nil
}

// contentVersion names an unlabelled version by its content.
func contentVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:6])
}

var _ Fetcher = (*DataAPIFetcher)(nil)
