package secretsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

const (
	envLambdaExtensionPort  = "PARAMETERS_SECRETS_EXTENSION_HTTP_PORT"
	envAWSSessionToken      = "AWS_SESSION_TOKEN"
	lambdaExtensionTokenHdr = "X-Aws-Parameters-Secrets-Token"
	lambdaExtensionTimeout  = 10 * time.Second
	lambdaExtensionHost     = "localhost"
	// Probing a local port is either accepted or refused almost immediately, so this only bounds pathological cases
	lambdaExtensionProbeTimeout = 10 * time.Millisecond
)

// Port the Lambda extension listens on when PARAMETERS_SECRETS_EXTENSION_HTTP_PORT is not set. A var for tests.
var defaultLambdaExtensionPort = "2773"

// newDefaultFetcher returns a fetcher for the Lambda extension if it is detected, and otherwise an SDK fetcher with a
// client created from the default AWS config. The extension doesn't set PARAMETERS_SECRETS_EXTENSION_HTTP_PORT in the
// function's environment unless the user configured it, so if it is unset, the extension's default port is probed.
// Setting it to empty disables detection, for when another service uses the default port.
func newDefaultFetcher() fetcher {
	port, ok := os.LookupEnv(envLambdaExtensionPort)
	if !ok {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(lambdaExtensionHost, defaultLambdaExtensionPort),
			lambdaExtensionProbeTimeout)
		if err != nil {
			return &sdkFetcher{}
		}
		_ = conn.Close()
		port = defaultLambdaExtensionPort
	}
	if port == "" {
		return &sdkFetcher{}
	}
	return newLambdaExtensionFetcher(port)
}

type secretValue struct {
	value     []byte
	versionID string
}

type fetcher interface {
	fetch(ctx context.Context, secretID string) (secretValue, error)
	path() fetchPath
}

// sdkFetcher fetches secrets directly from Secrets Manager. If client is nil, one is created from the default AWS
// config on first use.
type sdkFetcher struct {
	mu     sync.Mutex
	client SecretsClient
}

func (f *sdkFetcher) fetch(ctx context.Context, secretID string) (secretValue, error) {
	c, err := f.getClient(ctx)
	if err != nil {
		return secretValue{}, err
	}
	out, err := c.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: &secretID})
	if err != nil {
		return secretValue{}, err
	}
	return newSecretValue(out.SecretString, out.SecretBinary, out.VersionId)
}

func (*sdkFetcher) path() fetchPath { return fetchPathSDK }

func (f *sdkFetcher) getClient(ctx context.Context) (SecretsClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed loading default AWS config: %w", err)
		}
		f.client = awssm.NewFromConfig(cfg)
	}
	return f.client, nil
}

// lambdaExtensionFetcher fetches secrets through the AWS Parameters and Secrets Lambda Extension. See
// https://docs.aws.amazon.com/secretsmanager/latest/userguide/retrieving-secrets_lambda.html.
type lambdaExtensionFetcher struct {
	endpoint   string
	httpClient *http.Client
}

func newLambdaExtensionFetcher(port string) *lambdaExtensionFetcher {
	return &lambdaExtensionFetcher{
		endpoint:   "http://" + net.JoinHostPort(lambdaExtensionHost, port) + "/secretsmanager/get",
		httpClient: &http.Client{Timeout: lambdaExtensionTimeout},
	}
}

func (f *lambdaExtensionFetcher) fetch(ctx context.Context, secretID string) (secretValue, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		f.endpoint+"?secretId="+url.QueryEscape(secretID), nil)
	if err != nil {
		return secretValue{}, err
	}
	req.Header.Set(lambdaExtensionTokenHdr, os.Getenv(envAWSSessionToken))
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return secretValue{}, fmt.Errorf("failed calling AWS Parameters and Secrets Lambda Extension: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return secretValue{}, fmt.Errorf("failed reading AWS Parameters and Secrets Lambda Extension response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return secretValue{}, &lambdaExtensionStatusError{statusCode: resp.StatusCode, body: string(body)}
	}
	// Same shape as the GetSecretValue response. SecretBinary is base64, which json decodes into []byte.
	var out struct {
		SecretString *string
		SecretBinary []byte
		VersionId    *string
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return secretValue{}, &invalidResponseError{
			fmt.Errorf("failed decoding AWS Parameters and Secrets Lambda Extension response: %w", err)}
	}
	return newSecretValue(out.SecretString, out.SecretBinary, out.VersionId)
}

func (*lambdaExtensionFetcher) path() fetchPath { return fetchPathExtension }

func newSecretValue(secretString *string, secretBinary []byte, versionID *string) (secretValue, error) {
	var ret secretValue
	if versionID != nil {
		ret.versionID = *versionID
	}
	if secretString != nil {
		ret.value = []byte(*secretString)
	} else if len(secretBinary) > 0 {
		ret.value = secretBinary
	} else {
		return secretValue{}, &invalidResponseError{errors.New("secret has no value")}
	}
	return ret, nil
}

type lambdaExtensionStatusError struct {
	statusCode int
	body       string
}

func (e *lambdaExtensionStatusError) Error() string {
	return fmt.Sprintf("AWS Parameters and Secrets Lambda Extension returned status %v: %s", e.statusCode, e.body)
}

// invalidResponseError is a response that was received but has no usable secret value.
type invalidResponseError struct{ err error }

func (e *invalidResponseError) Error() string { return e.err.Error() }
func (e *invalidResponseError) Unwrap() error { return e.err }
