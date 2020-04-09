package internal

// Below are the metadata which will be embedded as part of headers in every rpc call made by this client to Temporal server.
// Update to the metadata below is typically done by the Temporal team as part of a major feature or behavior change.

// SDKVersion is a semver that represents the version of this Temporal SDK.
// This represents API changes visible to Temporal SDK consumers, i.e. developers
// that are writing workflows. So every time we change API that can affect them we have to change this number.
// Format: MAJOR.MINOR.PATCH
const SDKVersion = "0.20.0"

// SDKFeatureVersion is a semver that represents the feature set version of this Temporal SDK support.
// This can be used for client capability check, on Temporal server, for backward compatibility.
// Format: MAJOR.MINOR.PATCH
const SDKFeatureVersion = "1.0.0"
