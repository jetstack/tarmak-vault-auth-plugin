package awsauth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/helper/awsutil"
)

type CLIHandler struct{}

// Generates the necessary data to send to the Vault server for generating a token
// This is useful for other API clients to use
func GenerateLoginData(accessKey, secretKey, sessionToken, headerValue string) (map[string]interface{}, error) {
	loginData := make(map[string]interface{})

	credConfig := &awsutil.CredentialsConfig{
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		SessionToken: sessionToken,
	}
	creds, err := credConfig.GenerateCredentialChain()
	if err != nil {
		return nil, err
	}
	if creds == nil {
		return nil, fmt.Errorf("could not compile valid credential providers from static config, environment, shared, or instance metadata")
	}

	// Use the credentials we've found to construct an STS session
	stsSession, err := session.NewSessionWithOptions(session.Options{
		Config: aws.Config{Credentials: creds},
	})
	if err != nil {
		return nil, err
	}

	var params *sts.GetCallerIdentityInput
	svc := sts.New(stsSession)
	stsRequest, _ := svc.GetCallerIdentityRequest(params)

	// Inject the required auth header value, if supplied, and then sign the request including that header
	if headerValue != "" {
		stsRequest.HTTPRequest.Header.Add(iamServerIdHeader, headerValue)
	}
	stsRequest.Sign()

	// Now extract out the relevant parts of the request
	headersJson, err := json.Marshal(stsRequest.HTTPRequest.Header)
	if err != nil {
		return nil, err
	}
	requestBody, err := ioutil.ReadAll(stsRequest.HTTPRequest.Body)
	if err != nil {
		return nil, err
	}
	loginData["iam_http_request_method"] = stsRequest.HTTPRequest.Method
	loginData["iam_request_url"] = base64.StdEncoding.EncodeToString([]byte(stsRequest.HTTPRequest.URL.String()))
	loginData["iam_request_headers"] = base64.StdEncoding.EncodeToString(headersJson)
	loginData["iam_request_body"] = base64.StdEncoding.EncodeToString(requestBody)

	return loginData, nil
}

func (h *CLIHandler) Auth(c *api.Client, m map[string]string) (*api.Secret, error) {
	mount, ok := m["mount"]
	if !ok {
		mount = "aws"
	}

	role, ok := m["role"]
	if !ok {
		role = ""
	}

	headerValue, ok := m["header_value"]
	if !ok {
		headerValue = ""
	}

	loginData, err := GenerateLoginData(m["aws_access_key_id"], m["aws_secret_access_key"], m["aws_security_token"], headerValue)
	if err != nil {
		return nil, err
	}
	if loginData == nil {
		return nil, fmt.Errorf("got nil response from GenerateLoginData")
	}
	loginData["role"] = role
	path := fmt.Sprintf("auth/%s/login", mount)
	secret, err := c.Logical().Write(path, loginData)

	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, fmt.Errorf("empty response from credential provider")
	}

	return secret, nil
}

func (h *CLIHandler) Help() string {
	help := `
Usage: vault login -method=aws [CONFIG K=V...]

  The AWS auth method allows users to authenticate with AWS IAM
  credentials. The AWS IAM credentials may be specified in a number of ways,
  listed in order of precedence below:

    1. Explicitly via the command line (not recommended)

    2. Via the standard AWS environment variables (AWS_ACCESS_KEY, etc.)

    3. Via the ~/.aws/credentials file

    4. Via EC2 instance profile

  Authenticate using locally stored credentials:

      $ vault login -method=aws

  Authenticate by passing keys:

      $ vault login -method=aws aws_access_key_id=... aws_secret_access_key=...

Configuration:

  aws_access_key_id=<string>
      Explicit AWS access key ID

  aws_secret_access_key=<string>
      Explicit AWS secret access key

  aws_security_token=<string>
      Explicit AWS security token for temporary credentials

  header_value=<string>
      Value for the x-vault-aws-iam-server-id header in requests

  mount=<string>
      Path where the AWS credential method is mounted. This is usually provided
      via the -path flag in the "vault login" command, but it can be specified
      here as well. If specified here, it takes precedence over the value for
      -path. The default value is "aws".

  role=<string>
      Name of the role to request a token against
`

	return strings.TrimSpace(help)
}
