// Copyright (c) 2018 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package garden

import (
	"context"
	"fmt"
	"strings"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/utils"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
	"github.com/gardener/gardener/pkg/utils/version"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewBuilder returns a new Builder.
func NewBuilder() *Builder {
	return &Builder{
		projectFunc: func(context.Context) (*gardencorev1beta1.Project, error) {
			return nil, fmt.Errorf("project is required but not set")
		},
		internalDomainFunc: func() (*Domain, error) { return nil, fmt.Errorf("internal domain is required but not set") },
	}
}

// WithProject sets the projectFunc attribute at the Builder.
func (b *Builder) WithProject(project *gardencorev1beta1.Project) *Builder {
	b.projectFunc = func(context.Context) (*gardencorev1beta1.Project, error) { return project, nil }
	return b
}

// WithProjectFrom sets the projectFunc attribute after fetching it from the given reader.
func (b *Builder) WithProjectFrom(reader client.Reader, namespace string) *Builder {
	b.projectFunc = func(ctx context.Context) (*gardencorev1beta1.Project, error) {
		project, _, err := gutil.ProjectAndNamespaceFromReader(ctx, reader, namespace)
		return project, err
	}
	return b
}

// WithInternalDomain sets the internalDomainFunc attribute at the Builder.
func (b *Builder) WithInternalDomain(internalDomain *Domain) *Builder {
	b.internalDomainFunc = func() (*Domain, error) { return internalDomain, nil }
	return b
}

// WithInternalDomainFromSecrets sets the internalDomainFunc attribute at the Builder based on the given secrets map.
func (b *Builder) WithInternalDomainFromSecrets(secrets map[string]*corev1.Secret) *Builder {
	b.internalDomainFunc = func() (*Domain, error) { return GetInternalDomain(secrets) }
	return b
}

// WithDefaultDomains sets the defaultDomainsFunc attribute at the Builder.
func (b *Builder) WithDefaultDomains(defaultDomains []*Domain) *Builder {
	b.defaultDomainsFunc = func() ([]*Domain, error) { return defaultDomains, nil }
	return b
}

// WithDefaultDomainsFromSecrets sets the defaultDomainsFunc attribute at the Builder based on the given secrets map.
func (b *Builder) WithDefaultDomainsFromSecrets(secrets map[string]*corev1.Secret) *Builder {
	b.defaultDomainsFunc = func() ([]*Domain, error) { return GetDefaultDomains(secrets) }
	return b
}

// Build initializes a new Garden object.
func (b *Builder) Build(ctx context.Context) (*Garden, error) {
	garden := &Garden{}

	project, err := b.projectFunc(ctx)
	if err != nil {
		return nil, err
	}
	garden.Project = project

	internalDomain, err := b.internalDomainFunc()
	if err != nil {
		return nil, err
	}
	garden.InternalDomain = internalDomain

	defaultDomains, err := b.defaultDomainsFunc()
	if err != nil {
		return nil, err
	}
	garden.DefaultDomains = defaultDomains

	return garden, nil
}

// GetDefaultDomains finds all the default domain secrets within the given map and returns a list of
// objects that contains all relevant information about the default domains.
func GetDefaultDomains(secrets map[string]*corev1.Secret) ([]*Domain, error) {
	var defaultDomains []*Domain

	for key, secret := range secrets {
		if strings.HasPrefix(key, v1beta1constants.GardenRoleDefaultDomain) {
			domain, err := constructDomainFromSecret(secret)
			if err != nil {
				return nil, fmt.Errorf("error getting information out of default domain secret: %+v", err)
			}
			defaultDomains = append(defaultDomains, domain)
		}
	}

	return defaultDomains, nil
}

// GetInternalDomain finds the internal domain secret within the given map and returns the object
// that contains all relevant information about the internal domain.
func GetInternalDomain(secrets map[string]*corev1.Secret) (*Domain, error) {
	internalDomainSecret, ok := secrets[v1beta1constants.GardenRoleInternalDomain]
	if !ok {
		return nil, nil
	}

	return constructDomainFromSecret(internalDomainSecret)
}

func constructDomainFromSecret(secret *corev1.Secret) (*Domain, error) {
	provider, domain, zone, includeZones, excludeZones, err := gutil.GetDomainInfoFromAnnotations(secret.Annotations)
	if err != nil {
		return nil, err
	}

	return &Domain{
		Domain:       domain,
		Provider:     provider,
		Zone:         zone,
		SecretData:   secret.Data,
		IncludeZones: includeZones,
		ExcludeZones: excludeZones,
	}, nil
}

// DomainIsDefaultDomain identifies whether the given domain is a default domain.
func DomainIsDefaultDomain(domain string, defaultDomains []*Domain) *Domain {
	for _, defaultDomain := range defaultDomains {
		if strings.HasSuffix(domain, "."+defaultDomain.Domain) {
			return defaultDomain
		}
	}
	return nil
}

var gardenRoleReq = utils.MustNewRequirement(v1beta1constants.GardenRole, selection.Exists)

// ReadGardenSecrets reads the Kubernetes Secrets from the Garden cluster which are independent of Shoot clusters.
// The Secret objects are stored on the Controller in order to pass them to created Garden objects later.
func ReadGardenSecrets(ctx context.Context, c client.Reader, namespace string, log logrus.FieldLogger, enforceInternalDomainSecret bool) (map[string]*corev1.Secret, error) {
	var (
		logInfo                             []string
		secretsMap                          = make(map[string]*corev1.Secret)
		numberOfInternalDomainSecrets       = 0
		numberOfOpenVPNDiffieHellmanSecrets = 0
		numberOfAlertingSecrets             = 0
		numberOfGlobalMonitoringSecrets     = 0
	)

	secretList := &corev1.SecretList{}
	if err := c.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: labels.NewSelector().Add(gardenRoleReq)}); err != nil {
		return nil, err
	}

	for _, secret := range secretList.Items {
		// Retrieving default domain secrets based on all secrets in the Garden namespace which have
		// a label indicating the Garden role default-domain.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleDefaultDomain {
			_, domain, _, _, _, err := gutil.GetDomainInfoFromAnnotations(secret.Annotations)
			if err != nil {
				log.Warnf("error getting information out of default domain secret %s: %+v", secret.Name, err)
				continue
			}
			defaultDomainSecret := secret
			secretsMap[fmt.Sprintf("%s-%s", v1beta1constants.GardenRoleDefaultDomain, domain)] = &defaultDomainSecret
			logInfo = append(logInfo, fmt.Sprintf("default domain secret %q for domain %q", secret.Name, domain))
		}

		// Retrieving internal domain secrets based on all secrets in the Garden namespace which have
		// a label indicating the Garden role internal-domain.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleInternalDomain {
			_, domain, _, _, _, err := gutil.GetDomainInfoFromAnnotations(secret.Annotations)
			if err != nil {
				log.Warnf("error getting information out of internal domain secret %s: %+v", secret.Name, err)
				continue
			}
			internalDomainSecret := secret
			secretsMap[v1beta1constants.GardenRoleInternalDomain] = &internalDomainSecret
			logInfo = append(logInfo, fmt.Sprintf("internal domain secret %q for domain %q", secret.Name, domain))
			numberOfInternalDomainSecrets++
		}

		// Retrieving Diffie-Hellman secret for OpenVPN based on all secrets in the Garden namespace which have
		// a label indicating the Garden role openvpn-diffie-hellman.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleOpenVPNDiffieHellman {
			openvpnDiffieHellman := secret
			key := "dh2048.pem"
			if _, ok := secret.Data[key]; !ok {
				return nil, fmt.Errorf("cannot use OpenVPN Diffie Hellman secret '%s' as it does not contain key '%s' (whose value should be the actual Diffie Hellman key)", secret.Name, key)
			}
			secretsMap[v1beta1constants.GardenRoleOpenVPNDiffieHellman] = &openvpnDiffieHellman
			logInfo = append(logInfo, fmt.Sprintf("OpenVPN Diffie Hellman secret %q", secret.Name))
			numberOfOpenVPNDiffieHellmanSecrets++
		}

		// Retrieve the alerting secret to configure alerting. Either in cluster email alerting or
		// external alertmanager configuration.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleAlerting {
			authType := string(secret.Data["auth_type"])
			if authType != "smtp" && authType != "none" && authType != "basic" && authType != "certificate" {
				return nil, fmt.Errorf("invalid or missing field 'auth_type' in secret %s", secret.Name)
			}
			alertingSecret := secret
			secretsMap[v1beta1constants.GardenRoleAlerting] = &alertingSecret
			logInfo = append(logInfo, fmt.Sprintf("alerting secret %q", secret.Name))
			numberOfAlertingSecrets++
		}

		// Retrieving basic auth secret for aggregate monitoring with a label
		// indicating the Garden role global-monitoring.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleGlobalMonitoring {
			monitoringSecret := secret
			secretsMap[v1beta1constants.GardenRoleGlobalMonitoring] = &monitoringSecret
			logInfo = append(logInfo, fmt.Sprintf("monitoring basic auth secret %q", secret.Name))
			numberOfGlobalMonitoringSecrets++
		}

		// Retrieving basic auth secret for remote write monitoring with a label
		// indicating the Garden role global-shoot-remote-write-monitoring.
		if secret.Labels[v1beta1constants.GardenRole] == v1beta1constants.GardenRoleGlobalShootRemoteWriteMonitoring {
			monitoringSecret := secret
			secretsMap[v1beta1constants.GardenRoleGlobalShootRemoteWriteMonitoring] = &monitoringSecret
			logInfo = append(logInfo, fmt.Sprintf("monitoring basic auth secret %q", secret.Name))
		}
	}

	// Check if an internal domain secret is required
	seedList := &gardencorev1beta1.SeedList{}
	if err := c.List(ctx, seedList); err != nil {
		return nil, err
	}
	for _, seed := range seedList.Items {
		if !seed.Spec.Settings.ShootDNS.Enabled {
			continue
		}

		// For each Shoot we create a LoadBalancer(LB) pointing to the API server of the Shoot. Because the technical address
		// of the LB (ip or hostname) can change we cannot directly write it into the kubeconfig of the components
		// which talk from outside (kube-proxy, kubelet etc.) (otherwise those kubeconfigs would be broken once ip/hostname
		// of LB changed; and we don't have means to exchange kubeconfigs currently).
		// Therefore, to have a stable endpoint, we create a DNS record pointing to the ip/hostname of the LB. This DNS record
		// is used in all kubeconfigs. With that we have a robust endpoint stable against underlying ip/hostname changes.
		// And there can only be one of this internal domain secret because otherwise the gardener would not know which
		// domain it should use.
		if enforceInternalDomainSecret && numberOfInternalDomainSecrets == 0 {
			return nil, fmt.Errorf("need an internal domain secret but found none")
		}
	}

	// The VPN bridge from a Shoot's control plane running in the Seed cluster to the worker nodes of the Shoots is based
	// on OpenVPN. It requires a Diffie Hellman key. If no such key is explicitly provided as secret in the garden namespace
	// then the Gardener will use a default one (not recommended, but useful for local development). If a secret is specified
	// its key will be used for all Shoots. However, at most only one of such a secret is allowed to be specified (otherwise,
	// the Gardener cannot determine which to choose).
	if numberOfOpenVPNDiffieHellmanSecrets > 1 {
		return nil, fmt.Errorf("can only accept at most one OpenVPN Diffie Hellman secret, but found %d", numberOfOpenVPNDiffieHellmanSecrets)
	}

	// Operators can configure gardener to send email alerts or send the alerts to an external alertmanager. If no configuration
	// is provided then no alerts will be sent.
	if numberOfAlertingSecrets > 1 {
		return nil, fmt.Errorf("can only accept at most one alerting secret, but found %d", numberOfAlertingSecrets)
	}

	if numberOfGlobalMonitoringSecrets > 1 {
		return nil, fmt.Errorf("can only accept at most one global monitoring secret, but found %d", numberOfGlobalMonitoringSecrets)
	}

	log.Infof("Found secrets in namespace %q: %s", namespace, strings.Join(logInfo, ", "))

	return secretsMap, nil
}

// BootstrapCluster bootstraps the Garden cluster and deploys various required manifests.
func BootstrapCluster(ctx context.Context, k8sGardenClient kubernetes.Interface, secretsManager secretsmanager.Interface) error {
	// Check whether the Kubernetes version of the Garden cluster is at least 1.17 (least supported K8s version of Gardener).
	minGardenVersion := "1.17"
	gardenVersionOK, err := version.CompareVersions(k8sGardenClient.Version(), ">=", minGardenVersion)
	if err != nil {
		return err
	}
	if !gardenVersionOK {
		return fmt.Errorf("the Kubernetes version of the Garden cluster must be at least %s", minGardenVersion)
	}

	secretList := &corev1.SecretList{}
	if err := k8sGardenClient.Client().List(
		ctx,
		secretList,
		client.InNamespace(v1beta1constants.GardenNamespace),
		client.MatchingLabels{v1beta1constants.GardenRole: v1beta1constants.GardenRoleGlobalMonitoring},
	); err != nil {
		return err
	}

	mustGenerateMonitoringSecret := true
	for _, s := range secretList.Items {
		managedBySecretsManager := s.Labels[secretsmanager.LabelKeyManagedBy] == secretsmanager.LabelValueSecretsManager &&
			s.Labels[secretsmanager.LabelKeyManagerIdentity] == v1beta1constants.SecretManagerIdentityControllerManager

		if !managedBySecretsManager {
			// found a custom monitoring secret managed by a human operator
			// keep it and don't take over responsibility for the monitoring secret
			mustGenerateMonitoringSecret = false
			break
		}
	}

	// we don't want to override custom monitoring secret managed by a human operator
	// only take over responsibility over monitoring secret if we find the legacy secret created by GCM or a new one managed by SecretsManager
	if mustGenerateMonitoringSecret {
		if _, err = generateGlobalMonitoringSecret(ctx, k8sGardenClient.Client(), secretsManager); err != nil {
			return err
		}
	}

	return nil
}

func generateGlobalMonitoringSecret(ctx context.Context, k8sGardenClient client.Client, secretsManager secretsmanager.Interface) (*corev1.Secret, error) {
	credentialsSecret, err := secretsManager.Generate(ctx, &secretutils.BasicAuthSecretConfig{
		Name:           v1beta1constants.SecretNameObservabilityIngress,
		Format:         secretutils.BasicAuthFormatNormal,
		Username:       "admin",
		PasswordLength: 32,
	})
	if err != nil {
		return nil, err
	}

	// TODO(rfranzke): Remove in a future release.
	if err := kutil.DeleteObject(ctx, k8sGardenClient, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "monitoring-ingress-credentials", Namespace: v1beta1constants.GardenNamespace}}); err != nil {
		return nil, err
	}

	patch := client.MergeFrom(credentialsSecret.DeepCopy())
	metav1.SetMetaDataLabel(&credentialsSecret.ObjectMeta, v1beta1constants.GardenRole, v1beta1constants.GardenRoleGlobalMonitoring)
	return credentialsSecret, k8sGardenClient.Patch(ctx, credentialsSecret, patch)
}
