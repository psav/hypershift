package aws

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "aws-cloud-controller-manager"
)

var _ component.ComponentOptions = &awsOptions{}

type awsOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *awsOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &awsOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountNameSpace: "kube-system",
			ServiceAccountName:      "kube-controller-manager",
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.AWSPlatform, nil
}

// adaptDeployment configures the CCM container to use web identity tokens
// instead of a shared credentials file. The AWS SDK credential chain tries
// shared credentials before web identity, so AWS_SHARED_CREDENTIALS_FILE
// must be removed when using web identity — otherwise the SDK errors out
// if the credentials file doesn't exist before ever trying the token provider.
func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform || hcp.Spec.Platform.AWS == nil {
		return nil
	}

	roleARN := hcp.Spec.Platform.AWS.RolesRef.KubeCloudControllerARN
	if roleARN == "" {
		return nil
	}

	container := &deployment.Spec.Template.Spec.Containers[0]

	// Remove AWS_SHARED_CREDENTIALS_FILE (conflicts with web identity — SDK
	// errors if the file doesn't exist before trying the token provider) and
	// AWS_EC2_METADATA_DISABLED (IMDS isn't available in the management cluster
	// and the CCM tries to load region from it).
	filtered := container.Env[:0]
	for _, e := range container.Env {
		if e.Name != "AWS_SHARED_CREDENTIALS_FILE" && e.Name != "AWS_EC2_METADATA_DISABLED" {
			filtered = append(filtered, e)
		}
	}
	container.Env = filtered

	container.Env = append(container.Env,
		corev1.EnvVar{Name: "AWS_WEB_IDENTITY_TOKEN_FILE", Value: "/var/run/secrets/openshift/serviceaccount/token"},
		corev1.EnvVar{Name: "AWS_ROLE_ARN", Value: roleARN},
		corev1.EnvVar{Name: "AWS_REGION", Value: hcp.Spec.Platform.AWS.Region},
	)

	return nil
}
