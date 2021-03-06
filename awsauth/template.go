package awsauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	texttemplate "text/template"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/vault/logical"
)

type Values struct {
	InstanceHash string
	FQDN         string
	InternalIPv4 string
	BasePath     string
	OutputPath   string
	TemplateName string
}

func secretName(values Values) string {
	return filepath.Join(values.BasePath, values.OutputPath, fmt.Sprintf("%s-%s", values.TemplateName, values.InstanceHash))
}

func renderTemplates(ctx context.Context, b *backend, req *logical.Request, instance *ec2.Instance, roleName string, role *awsRoleEntry) ([]string, error) {
	values := Values{
		BasePath: role.BasePath,
	}

	if instance == nil {
		return nil, fmt.Errorf("instance cannot be nil")
	}

	if instance.InstanceId != nil {
		values.InstanceHash = *instance.InstanceId
	}

	if instance.PrivateDnsName != nil {
		values.FQDN = *instance.PrivateDnsName
	}

	if instance.PrivateIpAddress != nil {
		values.InternalIPv4 = *instance.PrivateIpAddress
	}

	policies := []string{}

	templates, err := req.Storage.List(ctx, fmt.Sprintf("template/%s/", roleName))
	if err != nil {
		return nil, err
	}

	vaultClient, err := b.GetVaultClient(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	for _, templateName := range templates {
		template, err := b.lockedTemplateEntry(ctx, req.Storage, roleName, templateName)
		if err != nil {
			return nil, err
		}

		values.OutputPath = template.Path
		values.TemplateName = templateName

		tmpl, err := texttemplate.New("tmpl").Parse(template.Template)
		if err != nil {
			return nil, err
		}

		var buf bytes.Buffer
		err = tmpl.Execute(&buf, values)
		if err != nil {
			return nil, err
		}

		switch template.Type {
		case "policy":
			fullPolicyName := secretName(values)

			b.Logger().Info(fmt.Sprintf("creating policy: '%s' %s", fullPolicyName, buf.String()))
			policies = append(policies, fullPolicyName)
			_, err = vaultClient.Logical().Write(fullPolicyName,
				map[string]interface{}{
					"policy": buf.String(),
				},
			)
			if err != nil {
				return nil, err
			}
		case "generic":
			m := map[string]interface{}{}
			err := json.Unmarshal(buf.Bytes(), &m)
			if err != nil {
				return nil, err
			}

			fullSecretName := secretName(values)
			b.Logger().Info(fmt.Sprintf("creating secret: '%s' %v", fullSecretName, m))
			_, err = vaultClient.Logical().Write(fullSecretName, m)

			if err != nil {
				return nil, err
			}

		default:
			return nil, fmt.Errorf("not a supported template type: %s", template.Type)
		}

	}
	return policies, nil
}
