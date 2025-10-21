package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	batchv1 "github.com/forkspacer/forkspacer/api/v1"
	fileCons "github.com/forkspacer/forkspacer/pkg/constants/file"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/go-logr/logr"
	"github.com/go-viper/mapstructure/v2"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ base.IManager = ModuleHelmManager{}

type ModuleHelmManager struct {
	helmModule       *batchv1.ModuleSpecHelm
	helmService      *services.HelmService
	releaseName      string
	controllerClient client.Client
	log              logr.Logger
}

func NewModuleHelmManager(
	helmModule *batchv1.ModuleSpecHelm,
	helmService *services.HelmService,
	releaseName string,
	controllerClient client.Client,
	logger logr.Logger,
) (base.IManager, error) {
	return &ModuleHelmManager{
		helmModule:       helmModule,
		helmService:      helmService,
		releaseName:      releaseName,
		controllerClient: controllerClient,
		log:              logger,
	}, nil
}

// fetchRepoAuthCredentials retrieves username and password from a secret reference
func (m ModuleHelmManager) fetchRepoAuthCredentials(
	ctx context.Context,
	auth *batchv1.ModuleSpecHelmChartRepoAuth,
) (username, password string, err error) {
	secret := &corev1.Secret{}
	if err := m.controllerClient.Get(ctx, types.NamespacedName{
		Name:      auth.Name,
		Namespace: auth.Namespace,
	}, secret); err != nil {
		return "", "", fmt.Errorf("failed to fetch repository auth secret %s/%s: %w",
			auth.Namespace,
			auth.Name,
			err,
		)
	}

	// Username is optional, password/token is required
	if usernameBytes, ok := secret.Data[kubernetesCons.Helm.ChartRepoAuthSecretUsernameKey]; ok {
		username = string(usernameBytes)
	}

	passwordBytes, ok := secret.Data[kubernetesCons.Helm.ChartRepoAuthSecretPasswordKey]
	if !ok {
		return "", "", fmt.Errorf("secret %s/%s does not contain '%s' data",
			auth.Namespace,
			auth.Name,
			kubernetesCons.Helm.ChartRepoAuthSecretPasswordKey,
		)
	}
	password = string(passwordBytes)

	return username, password, nil
}

func (m ModuleHelmManager) Install(ctx context.Context, metaData base.MetaData) error {
	if m.helmModule.Chart.Repo != nil {
		// Fetch repository authentication credentials if configured
		var username, password string
		if m.helmModule.Chart.Repo.Auth != nil {
			var err error
			username, password, err = m.fetchRepoAuthCredentials(ctx, m.helmModule.Chart.Repo.Auth)
			if err != nil {
				return err
			}
		}

		if err := m.helmService.InstallFromRepository(
			ctx,
			m.helmModule.Chart.Repo.Chart,
			m.releaseName,
			m.helmModule.Namespace,
			m.helmModule.Chart.Repo.URL,
			m.helmModule.Chart.Repo.Version,
			true,
			m.helmModule.Values,
			username,
			password,
		); err != nil {
			return err
		}
	} else if m.helmModule.Chart.ConfigMap != nil {
		configMap := &corev1.ConfigMap{}
		if err := m.controllerClient.Get(ctx, types.NamespacedName{
			Name:      m.helmModule.Chart.ConfigMap.Name,
			Namespace: m.helmModule.Chart.ConfigMap.Namespace,
		}, configMap); err != nil {
			return fmt.Errorf("failed to fetch ConfigMap %s/%s: %w",
				m.helmModule.Chart.ConfigMap.Namespace,
				m.helmModule.Chart.ConfigMap.Name,
				err,
			)
		}

		chartData, ok := configMap.BinaryData[m.helmModule.Chart.ConfigMap.Key]
		if !ok {
			return fmt.Errorf("ConfigMap %s/%s does not contain '%s' data",
				m.helmModule.Chart.ConfigMap.Namespace,
				m.helmModule.Chart.ConfigMap.Name,
				m.helmModule.Chart.ConfigMap.Key,
			)
		}

		// Create a temporary file to store the chart data.
		// The pattern "helm-chart-*.tgz" ensures a unique name and .tgz extension.
		// The file is created in the system's default temporary directory.
		tmpFile, err := os.CreateTemp(fileCons.GetBaseDir(), "helm-chart-*.tgz")
		if err != nil {
			return fmt.Errorf("failed to create temporary file for Helm chart: %w", err)
		}
		defer func() {
			// Clean up the temporary file after installation, regardless of success or failure.
			m.log.V(1).Info("Deleting temporary Helm chart file", "path", tmpFile.Name())
			if err := os.Remove(tmpFile.Name()); err != nil {
				m.log.Error(err, "Failed to delete temporary Helm chart file", "path", tmpFile.Name())
			}
		}()

		if _, err := tmpFile.Write(chartData); err != nil {
			// Close the file immediately on write error to release resources.
			_ = tmpFile.Close() // Ignore error on close if write failed
			return fmt.Errorf("failed to write chart data to temporary file %s: %w", tmpFile.Name(), err)
		}

		if err := tmpFile.Close(); err != nil {
			return fmt.Errorf("failed to close temporary file %s: %w", tmpFile.Name(), err)
		}

		if err := m.helmService.InstallFromLocal(ctx,
			tmpFile.Name(), // Path to the temporary chart file
			m.releaseName,
			m.helmModule.Namespace,
			true,
			m.helmModule.Values,
		); err != nil {
			return err
		}
	} else if m.helmModule.Chart.Git != nil {
		repoURL, err := url.Parse(m.helmModule.Chart.Git.Repo)
		if err != nil {
			return err
		}

		if m.helmModule.Chart.Git.Auth != nil {
			if m.helmModule.Chart.Git.Auth.HTTPSSecretRef != nil {
				secret := &corev1.Secret{}
				if err := m.controllerClient.Get(ctx, types.NamespacedName{
					Name:      m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Name,
					Namespace: m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Namespace,
				}, secret); err != nil {
					return fmt.Errorf("failed to fetch secret %s/%s: %w",
						m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Namespace,
						m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Name,
						err,
					)
				}

				username := secret.Data[kubernetesCons.Helm.ChartGitAuthHTTPSSecretUsernameKey]
				token, ok := secret.Data[kubernetesCons.Helm.ChartGitAuthHTTPSSecretTokenKey]
				if !ok {
					return fmt.Errorf("secret %s/%s does not contain '%s' data",
						m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Namespace,
						m.helmModule.Chart.Git.Auth.HTTPSSecretRef.Name,
						kubernetesCons.Helm.ChartGitAuthHTTPSSecretTokenKey,
					)
				}

				if len(username) > 0 {
					repoURL.User = url.UserPassword(string(username), string(token))
				} else {
					repoURL.User = url.User(string(token))
				}
			} else {
				return errors.New("unsupported Git chart authentication type provided")
			}
		}

		tempDir, err := os.MkdirTemp(fileCons.GetBaseDir(), "git-helm-chart-")
		if err != nil {
			return err
		}
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				m.log.Error(err, "Failed to delete temporary Git clone directory", "path", tempDir)
			}
		}()

		cmdCtx, cancel := context.WithTimeout(ctx, time.Second*240)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx,
			fileCons.GitPath, "clone", "--quiet", "--depth=1",
			"--branch", m.helmModule.Chart.Git.Revision,
			repoURL.String(), tempDir,
		)

		var stderrBuff bytes.Buffer
		cmd.Stderr = &stderrBuff

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone failed: %w. Stderr: %s", err, stderrBuff.String())
		}

		if err := m.helmService.InstallFromLocal(ctx,
			filepath.Join(tempDir, m.helmModule.Chart.Git.Path), // Path to the temporary chart directory
			m.releaseName,
			m.helmModule.Chart.ConfigMap.Namespace,
			true,
			m.helmModule.Values,
		); err != nil {
			return err
		}
	} else {
		return errors.New("no Helm chart source (repository, configmap, or git) specified in HelmModule")
	}

	if outputs := m.extractOutputs(ctx); outputs.Len() > 0 {
		metaData[managerCons.HelmMetaDataKeys.InstallOutputs] = outputs
	}

	return nil
}

func (m ModuleHelmManager) extractOutputs(ctx context.Context) *orderedmap.OrderedMap[string, any] {
	outputs := orderedmap.New[string, any]()

	for _, output := range m.helmModule.Outputs {
		if output.Value != nil {
			var parsedValue any
			if err := json.Unmarshal(output.Value.Raw, &parsedValue); err != nil {
				m.log.Error(err, "Failed to parse output value as JSON", "outputName", output.Name)
			} else {
				outputs.Set(output.Name, parsedValue)
			}
		} else if output.ValueFrom != nil {
			if output.ValueFrom.Secret != nil && output.ValueFrom.Secret.Name != "" && output.ValueFrom.Secret.Key != "" {
				secretValue, err := m.helmService.GetSecretValue(ctx,
					output.ValueFrom.Secret.Namespace,
					output.ValueFrom.Secret.Name,
					output.ValueFrom.Secret.Key,
				)
				if err != nil {
					m.log.Error(err, "Failed to get secret value for output",
						"outputName", output.Name,
						"secretNamespace", output.ValueFrom.Secret.Namespace,
						"secretName", output.ValueFrom.Secret.Name,
						"secretKey", output.ValueFrom.Secret.Key,
					)
					continue
				}

				outputs.Set(output.Name, secretValue)
			}
		}
	}

	return outputs
}

func (m ModuleHelmManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	return m.helmService.UninstallRelease(ctx,
		m.releaseName, m.helmModule.Namespace,
		m.helmModule.Cleanup.RemoveNamespace, m.helmModule.Cleanup.RemovePVCs,
	)
}

type ReplicaHistory struct {
	Deployments  []services.ResourceReplicaCount `json:"deployments,omitempty"`
	ReplicaSets  []services.ResourceReplicaCount `json:"replicaSets,omitempty"`
	StatefulSets []services.ResourceReplicaCount `json:"statefulSets,omitempty"`
}

func (m ModuleHelmManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	var replicaHistory ReplicaHistory

	oldReplicas, err := m.helmService.ScaleDeployments(ctx, m.releaseName, m.helmModule.Namespace, 0)
	for _, oldReplica := range oldReplicas {
		replicaHistory.Deployments = append(replicaHistory.Deployments, oldReplica)
		metaData[managerCons.HelmMetaDataKeys.ReplicaHistory] = replicaHistory
	}
	if err != nil {
		return err
	}

	oldReplicas, err = m.helmService.ScaleReplicaSets(ctx, m.releaseName, m.helmModule.Namespace, 0)
	for _, oldReplica := range oldReplicas {
		replicaHistory.ReplicaSets = append(replicaHistory.ReplicaSets, oldReplica)
		metaData[managerCons.HelmMetaDataKeys.ReplicaHistory] = replicaHistory
	}
	if err != nil {
		return err
	}

	oldReplicas, err = m.helmService.ScaleStatefulSets(ctx, m.releaseName, m.helmModule.Namespace, 0)
	for _, oldReplica := range oldReplicas {
		replicaHistory.StatefulSets = append(replicaHistory.StatefulSets, oldReplica)
		metaData[managerCons.HelmMetaDataKeys.ReplicaHistory] = replicaHistory
	}
	if err != nil {
		return err
	}

	return nil
}

func (m ModuleHelmManager) Resume(ctx context.Context, metaData base.MetaData) error {
	replicaHistory, ok := metaData[managerCons.HelmMetaDataKeys.ReplicaHistory]
	if !ok {
		return errors.New("replica history not found in metadata")
	}

	var parsedreplicaHistory ReplicaHistory
	if err := mapstructure.Decode(replicaHistory, &parsedreplicaHistory); err != nil {
		return err
	}

	if err := m.helmService.ScaleDeploymentsBack(ctx, parsedreplicaHistory.Deployments...); err != nil {
		return err
	}
	if err := m.helmService.ScaleReplicaSetsBack(ctx, parsedreplicaHistory.ReplicaSets...); err != nil {
		return err
	}
	if err := m.helmService.ScaleStatefulSetsBack(ctx, parsedreplicaHistory.StatefulSets...); err != nil {
		return err
	}

	return nil
}
