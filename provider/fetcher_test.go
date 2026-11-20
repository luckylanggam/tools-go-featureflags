package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDataAPI hands out numbered tokens and serves scripted
// GetLatestConfiguration answers, checking each token is used once.
type fakeDataAPI struct {
	sessions   []*appconfigdata.StartConfigurationSessionInput
	startErr   error
	answers    []*appconfigdata.GetLatestConfigurationOutput
	getErrs    []error
	usedTokens map[string]bool
	next       int
}

func (f *fakeDataAPI) StartConfigurationSession(_ context.Context, in *appconfigdata.StartConfigurationSessionInput, _ ...func(*appconfigdata.Options)) (*appconfigdata.StartConfigurationSessionOutput, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.sessions = append(f.sessions, in)
	return &appconfigdata.StartConfigurationSessionOutput{InitialConfigurationToken: f.token()}, nil
}

func (f *fakeDataAPI) GetLatestConfiguration(_ context.Context, in *appconfigdata.GetLatestConfigurationInput, _ ...func(*appconfigdata.Options)) (*appconfigdata.GetLatestConfigurationOutput, error) {
	tok := aws.ToString(in.ConfigurationToken)
	if f.usedTokens[tok] {
		return nil, &types.BadRequestException{Message: aws.String("token reused")}
	}
	f.usedTokens[tok] = true

	if len(f.getErrs) > 0 {
		err := f.getErrs[0]
		f.getErrs = f.getErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	out := &appconfigdata.GetLatestConfigurationOutput{}
	if len(f.answers) > 0 {
		out = f.answers[0]
		f.answers = f.answers[1:]
	}
	out.NextPollConfigurationToken = f.token()
	return out, nil
}

func (f *fakeDataAPI) token() *string {
	f.next++
	return aws.String(fmt.Sprintf("token-%d", f.next))
}

func newFakeDataAPI(answers ...*appconfigdata.GetLatestConfigurationOutput) *fakeDataAPI {
	return &fakeDataAPI{answers: answers, usedTokens: map[string]bool{}}
}

func answer(data, label string) *appconfigdata.GetLatestConfigurationOutput {
	out := &appconfigdata.GetLatestConfigurationOutput{Configuration: []byte(data)}
	if label != "" {
		out.VersionLabel = aws.String(label)
	}
	return out
}

func TestDataAPIFetcher_SessionAndTokenChain(t *testing.T) {
	api := newFakeDataAPI(answer(flagsV1, "v1"), answer("", ""), answer(flagsV2, "v2"))
	f := NewDataAPIFetcher(api, "atom", "uat", 30)
	ctx := context.Background()

	data, version, changed, err := f.Fetch(ctx, "feature-flags")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, flagsV1, string(data))
	assert.Equal(t, "v1", version)

	require.Len(t, api.sessions, 1)
	s := api.sessions[0]
	assert.Equal(t, "atom", aws.ToString(s.ApplicationIdentifier))
	assert.Equal(t, "uat", aws.ToString(s.EnvironmentIdentifier))
	assert.Equal(t, "feature-flags", aws.ToString(s.ConfigurationProfileIdentifier))
	assert.Equal(t, int32(30), aws.ToInt32(s.RequiredMinimumPollIntervalInSeconds))

	data, version, changed, err = f.Fetch(ctx, "feature-flags")
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, data)
	assert.Equal(t, "v1", version, "unchanged keeps the last version")

	_, version, changed, err = f.Fetch(ctx, "feature-flags")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "v2", version)
	assert.Len(t, api.sessions, 1, "the session is reused")
}

func TestDataAPIFetcher_OneSessionPerProfile(t *testing.T) {
	api := newFakeDataAPI(answer(flagsV1, "v1"), answer(testCatalog, "c1"))
	f := NewDataAPIFetcher(api, "atom", "uat", 30)

	_, _, _, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)
	_, _, _, err = f.Fetch(context.Background(), "catalog")
	require.NoError(t, err)

	require.Len(t, api.sessions, 2)
	assert.Equal(t, "catalog", aws.ToString(api.sessions[1].ConfigurationProfileIdentifier))
}

func TestDataAPIFetcher_UnlabelledVersionIsAContentHash(t *testing.T) {
	api := newFakeDataAPI(answer(flagsV1, ""), answer(flagsV1, ""), answer(flagsV2, ""))
	f := NewDataAPIFetcher(api, "atom", "uat", 30)

	_, v1, _, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)
	_, again, _, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)
	_, v2, _, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)

	assert.Regexp(t, `^sha256:[0-9a-f]{12}$`, v1)
	assert.Equal(t, v1, again)
	assert.NotEqual(t, v1, v2)
}

func TestDataAPIFetcher_FailedCallStartsANewSession(t *testing.T) {
	api := newFakeDataAPI(answer(flagsV1, "v1"), answer(flagsV1, "v1"))
	expired := &types.BadRequestException{Message: aws.String("token expired")}
	api.getErrs = []error{nil, expired}
	f := NewDataAPIFetcher(api, "atom", "uat", 30)

	_, _, _, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)

	_, _, _, err = f.Fetch(context.Background(), "feature-flags")
	require.Error(t, err)
	var bad *types.BadRequestException
	assert.True(t, errors.As(err, &bad))

	_, version, changed, err := f.Fetch(context.Background(), "feature-flags")
	require.NoError(t, err)
	assert.True(t, changed, "a new session returns the full content")
	assert.Equal(t, "v1", version)
	assert.Len(t, api.sessions, 2)
}

func TestDataAPIFetcher_StartSessionError(t *testing.T) {
	api := newFakeDataAPI()
	api.startErr = errUnreachable
	f := NewDataAPIFetcher(api, "atom", "uat", 30)

	_, _, _, err := f.Fetch(context.Background(), "feature-flags")
	assert.ErrorIs(t, err, errUnreachable)
}
