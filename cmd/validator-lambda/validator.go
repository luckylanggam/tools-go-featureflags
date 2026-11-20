package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	appconfigdatatypes "github.com/aws/aws-sdk-go-v2/service/appconfigdata/types"
	"github.com/luckylanggam/tools-go-featureflags/document"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
	"github.com/luckylanggam/tools-go-featureflags/validate"
	"github.com/luckylanggam/tools-logger"
)

// Profile names created by the AppConfig feature-flags IaC module.
const (
	flagsProfile   = "feature-flags"
	catalogProfile = "catalog"
)

// Event is what AppConfig sends a LAMBDA validator for each configuration
// version it is about to deploy. Content is base64 encoded.
type Event struct {
	ApplicationID          string `json:"applicationId"`
	ConfigurationProfileID string `json:"configurationProfileId"`
	ConfigurationVersion   string `json:"configurationVersion"`
	Content                string `json:"content"`
	URI                    string `json:"uri"`
}

// ProfileAPI is the part of the AppConfig control-plane client the validator
// uses to learn which profile a version belongs to.
type ProfileAPI interface {
	GetConfigurationProfile(ctx context.Context, params *appconfig.GetConfigurationProfileInput, optFns ...func(*appconfig.Options)) (*appconfig.GetConfigurationProfileOutput, error)
}

// DataAPI is the part of the AppConfig Data client the validator uses to
// read what is deployed.
type DataAPI interface {
	StartConfigurationSession(ctx context.Context, params *appconfigdata.StartConfigurationSessionInput, optFns ...func(*appconfigdata.Options)) (*appconfigdata.StartConfigurationSessionOutput, error)
	GetLatestConfiguration(ctx context.Context, params *appconfigdata.GetLatestConfigurationInput, optFns ...func(*appconfigdata.Options)) (*appconfigdata.GetLatestConfigurationOutput, error)
}

// Validator checks a new feature-flags or catalog version against the other
// document as deployed in one AppConfig environment. Returning an error fails
// the AppConfig deployment, and AppConfig shows the error text.
type Validator struct {
	profiles    ProfileAPI
	data        DataAPI
	environment string
	log         logger.Logger
}

// NewValidator returns a validator for the given AppConfig environment name.
func NewValidator(profiles ProfileAPI, data DataAPI, environment string, log logger.Logger) *Validator {
	return &Validator{profiles: profiles, data: data, environment: environment, log: log}
}

// Handle validates the version in event.
func (v *Validator) Handle(ctx context.Context, event Event) error {
	if err := checkEvent(event); err != nil {
		return err
	}
	content, err := base64.StdEncoding.DecodeString(event.Content)
	if err != nil {
		return fmt.Errorf(ffErrors.ErrValidatorDecodeContent, event.ConfigurationVersion, err)
	}

	profile, err := v.profileName(ctx, event)
	if err != nil {
		return err
	}
	log := v.log.
		With("application", event.ApplicationID).
		With("profile", profile).
		With("version", event.ConfigurationVersion)

	sizeWarning, err := validate.CheckSize(content)
	if err != nil {
		return err
	}
	if sizeWarning != "" {
		log.Warn(sizeWarning)
	}

	var warnings []validate.Problem
	switch profile {
	case flagsProfile:
		warnings, err = v.validateFlags(ctx, event.ApplicationID, content)
	case catalogProfile:
		warnings, err = v.validateCatalog(ctx, event.ApplicationID, content)
	default:
		err = fmt.Errorf(ffErrors.ErrValidatorUnknownProfile, profile)
	}
	for _, w := range warnings {
		log.Warn(w.String())
	}
	if err != nil {
		log.WithError(err).Error("version rejected")
		return err
	}
	log.Info("version accepted")
	return nil
}

func checkEvent(event Event) error {
	switch {
	case event.ApplicationID == "":
		return fmt.Errorf(ffErrors.ErrValidatorBadEvent, "applicationId")
	case event.ConfigurationProfileID == "":
		return fmt.Errorf(ffErrors.ErrValidatorBadEvent, "configurationProfileId")
	case event.ConfigurationVersion == "":
		return fmt.Errorf(ffErrors.ErrValidatorBadEvent, "configurationVersion")
	}
	return nil
}

func (v *Validator) profileName(ctx context.Context, event Event) (string, error) {
	out, err := v.profiles.GetConfigurationProfile(ctx, &appconfig.GetConfigurationProfileInput{
		ApplicationId:          aws.String(event.ApplicationID),
		ConfigurationProfileId: aws.String(event.ConfigurationProfileID),
	})
	if err != nil {
		return "", fmt.Errorf(ffErrors.ErrValidatorGetProfile, event.ConfigurationProfileID, err)
	}
	return aws.ToString(out.Name), nil
}

// validateFlags checks new flags against the deployed catalog, which must
// exist: without it no attribute can be checked.
func (v *Validator) validateFlags(ctx context.Context, application string, content []byte) ([]validate.Problem, error) {
	flags, err := document.ParseFlags(content)
	if err != nil {
		return nil, err
	}
	catalog, err := v.deployedCatalog(ctx, application)
	if err != nil {
		return nil, err
	}
	if catalog == nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorNoCatalog, v.environment)
	}
	return validate.Validate(flags, catalog)
}

// validateCatalog checks a new catalog on its own and, when flags are
// deployed, that they still validate against it and that it does not retype
// an attribute they use, compared with the deployed catalog.
func (v *Validator) validateCatalog(ctx context.Context, application string, content []byte) ([]validate.Problem, error) {
	catalog, err := document.ParseCatalog(content)
	if err != nil {
		return nil, err
	}
	if err := validate.ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	raw, err := v.deployed(ctx, application, flagsProfile)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	flags, err := document.ParseFlags(raw)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorDeployedInvalid, flagsProfile, v.environment, err)
	}
	prev, err := v.deployedCatalog(ctx, application)
	if err != nil {
		return nil, err
	}
	// Warnings here belong to the deployed flags, not to this catalog.
	if _, err := validate.ValidateCatalogChange(prev, catalog, flags); err != nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorBreaksFlags, v.environment, err)
	}
	return nil, nil
}

// deployedCatalog returns the catalog deployed in the environment, or nil
// when none is.
func (v *Validator) deployedCatalog(ctx context.Context, application string) (*document.Catalog, error) {
	raw, err := v.deployed(ctx, application, catalogProfile)
	if err != nil || raw == nil {
		return nil, err
	}
	catalog, err := document.ParseCatalog(raw)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorDeployedInvalid, catalogProfile, v.environment, err)
	}
	return catalog, nil
}

// deployed returns the content of profile as deployed in the environment,
// or nil when nothing has been deployed yet. Any other error, including a
// missing environment, application or profile, fails closed.
func (v *Validator) deployed(ctx context.Context, application, profile string) ([]byte, error) {
	session, err := v.data.StartConfigurationSession(ctx, &appconfigdata.StartConfigurationSessionInput{
		ApplicationIdentifier:          aws.String(application),
		EnvironmentIdentifier:          aws.String(v.environment),
		ConfigurationProfileIdentifier: aws.String(profile),
	})
	if notDeployed(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorReadDeployed, profile, v.environment, err)
	}
	out, err := v.data.GetLatestConfiguration(ctx, &appconfigdata.GetLatestConfigurationInput{
		ConfigurationToken: session.InitialConfigurationToken,
	})
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrValidatorReadDeployed, profile, v.environment, err)
	}
	if len(out.Configuration) == 0 {
		return nil, nil
	}
	return out.Configuration, nil
}

// notDeployed reports whether err is AppConfig saying the profile has never
// been deployed to the environment.
func notDeployed(err error) bool {
	var notFound *appconfigdatatypes.ResourceNotFoundException
	return errors.As(err, &notFound) && notFound.ResourceType == appconfigdatatypes.ResourceTypeDeployment
}
