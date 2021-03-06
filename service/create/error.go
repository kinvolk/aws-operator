package create

import (
	"github.com/juju/errgo"
)

var invalidConfigError = errgo.New("invalid config")

// IsInvalidConfig asserts invalidConfigError.
func IsInvalidConfig(err error) bool {
	return errgo.Cause(err) == invalidConfigError
}

var invalidCloudconfigExtensionNameError = errgo.New("invalid cloudconfig extension name")

//  asserts invalidCloudconfigExtensionNameError.
func IsInvalidCloudconfigExtensionName(err error) bool {
	return errgo.Cause(err) == invalidCloudconfigExtensionNameError
}

var malformedCloudConfigKeyError = errgo.New("malformed key in the cloudconfig")

// IsMalformedCloudConfigKey asserts malformedCloudConfigKeyError.
func IsMalformedCloudConfigKey(err error) bool {
	return errgo.Cause(err) == malformedCloudConfigKeyError
}

var missingCloudConfigKeyError = errgo.New("missing cloud config key")

// IsMissingCloudConfigKey asserts missingCloudConfigKeyError.
func IsMissingCloudConfigKey(err error) bool {
	return errgo.Cause(err) == missingCloudConfigKeyError
}

var secretsRetrievalFailedError = errgo.New("secrets retrieval failed")

// IsSecretsRetrievalFailed asserts secretsRetrievalFailedError.
func IsSecretsRetrievalFailed(err error) bool {
	return errgo.Cause(err) == secretsRetrievalFailedError
}
