// Command validator-lambda is the AWS AppConfig LAMBDA validator for the
// feature-flags and catalog profiles. AppConfig calls it before each
// deployment; it runs the library's validate package against the other
// document as deployed in the same environment, so an invalid version is
// rejected whatever path it came from (Flag Console, AWS console, CLI, CI).
//
// Environment:
//
//	FEATURE_FLAGS_ENVIRONMENT  AppConfig environment name to read deployed documents from (e.g. uat)
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	"github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
	"github.com/luckylanggam/tools-logger"
)

const environmentVar = "FEATURE_FLAGS_ENVIRONMENT"

func main() {
	log := logger.New("feature-flags-validator", false)

	environment := os.Getenv(environmentVar)
	if environment == "" {
		log.Error(fmt.Sprintf(ffErrors.ErrValidatorMissingEnv, environmentVar))
		os.Exit(1)
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.WithError(err).Error("load AWS config")
		os.Exit(1)
	}

	v := NewValidator(appconfig.NewFromConfig(cfg), appconfigdata.NewFromConfig(cfg), environment, log)
	lambda.Start(v.Handle)
}
