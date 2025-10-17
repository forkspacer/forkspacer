package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	fileCons "github.com/forkspacer/forkspacer/pkg/constants/file"
	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
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
	helmModule       *resources.HelmModule
	helmService      *services.HelmService
	releaseName      string
	controllerClient client.Client
	log              logr.Logger
}

func NewModuleHelmManager(
	helmModule *resources.HelmModule,
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

func (m ModuleHelmManager) Install(ctx context.Context, metaData base.MetaData) error {
	namespace := m.helmModule.Spec.Namespace
	if namespace == "" {
		namespace = kubernetesCons.Helm.DefaultNamespace
	}

	if m.helmModule.Spec.Chart.Repo != nil {
		if err := m.helmService.InstallFromRepository(
			ctx,
			m.helmModule.Spec.Chart.Repo.ChartName,
			m.releaseName,
			namespace,
			m.helmModule.Spec.Chart.Repo.URL,
			m.helmModule.Spec.Chart.Repo.Version,
			true,
			m.helmModule.Spec.Values,
		); err != nil {
			return err
		}
	} else if m.helmModule.Spec.Chart.ConfigMap != nil {
		namespace := m.helmModule.Spec.Chart.ConfigMap.Namespace
		if namespace == "" {
			namespace = "default"
		}

		configMap := &corev1.ConfigMap{}
		if err := m.controllerClient.Get(ctx, types.NamespacedName{
			Name:      m.helmModule.Spec.Chart.ConfigMap.Name,
			Namespace: namespace,
		}, configMap); err != nil {
			return fmt.Errorf("failed to fetch ConfigMap %s/%s: %w",
				namespace,
				m.helmModule.Spec.Chart.ConfigMap.Name,
				err,
			)
		}

		key := m.helmModule.Spec.Chart.ConfigMap.Key
		if key == "" {
			key = kubernetesCons.Helm.ChartConfigMapKey
		}

		chartData, ok := configMap.BinaryData[key]
		if !ok {
			return fmt.Errorf("ConfigMap %s/%s does not contain '%s' data",
				namespace,
				m.helmModule.Spec.Chart.ConfigMap.Name,
				kubernetesCons.Helm.ChartConfigMapKey,
			)
		}

		// Create a temporary file to store the chart data.
		// The pattern "helm-chart-*.tgz" ensures a unique name and .tgz extension.
		// The file is created in the system's default temporary directory.
		tmpFile, err := os.CreateTemp(fileCons.BaseDir, "helm-chart-*.tgz")
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
			namespace,
			true,
			m.helmModule.Spec.Values,
		); err != nil {
			return err
		}
	} else if m.helmModule.Spec.Chart.Git != nil {
		repoURL, err := url.Parse(m.helmModule.Spec.Chart.Git.Repo)
		if err != nil {
			return err
		}

		revision := "main"
		if m.helmModule.Spec.Chart.Git.Revision != nil {
			revision = *m.helmModule.Spec.Chart.Git.Revision
		}

		if m.helmModule.Spec.Chart.Git.Auth != nil {
			if m.helmModule.Spec.Chart.Git.Auth.HTTPSSecretRef != nil {
				namespace := m.helmModule.Spec.Chart.Git.Auth.HTTPSSecretRef.Namespace
				if namespace == "" {
					namespace = "default"
				}

				secret := &corev1.Secret{}
				if err := m.controllerClient.Get(ctx, types.NamespacedName{
					Name:      m.helmModule.Spec.Chart.Git.Auth.HTTPSSecretRef.Name,
					Namespace: namespace,
				}, secret); err != nil {
					return fmt.Errorf("failed to fetch secret %s/%s: %w",
						namespace,
						m.helmModule.Spec.Chart.Git.Auth.HTTPSSecretRef.Name,
						err,
					)
				}

				username := secret.Data[kubernetesCons.Helm.ChartGitAuthHTTPSSecretUsernameKey]
				token, ok := secret.Data[kubernetesCons.Helm.ChartGitAuthHTTPSSecretTokenKey]
				if !ok {
					return fmt.Errorf("secret %s/%s does not contain '%s' data",
						namespace,
						m.helmModule.Spec.Chart.Git.Auth.HTTPSSecretRef.Name,
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

		tempDir, err := os.MkdirTemp(fileCons.BaseDir, "git-helm-chart-")
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
			"--branch", revision,
			repoURL.String(), tempDir,
		)

		var stderrBuff bytes.Buffer
		cmd.Stderr = &stderrBuff

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone failed: %w. Stderr: %s", err, stderrBuff.String())
		}

		if err := m.helmService.InstallFromLocal(ctx,
			filepath.Join(tempDir, m.helmModule.Spec.Chart.Git.Path), // Path to the temporary chart directory
			m.releaseName,
			namespace,
			true,
			m.helmModule.Spec.Values,
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

	for _, output := range m.helmModule.Spec.Outputs {
		if output.Value != nil {
			outputs.Set(output.Name, *output.Value)
		} else if output.ValueFrom != nil {
			if output.ValueFrom.Secret != nil && output.ValueFrom.Secret.Name != "" && output.ValueFrom.Secret.Key != "" {
				namespace := output.ValueFrom.Secret.Namespace
				if namespace != "" {
					namespace = kubernetesCons.Helm.DefaultNamespace
				}

				secretValue, err := m.helmService.GetSecretValue(ctx,
					namespace,
					output.ValueFrom.Secret.Name,
					output.ValueFrom.Secret.Key,
				)
				if err != nil {
					m.log.Error(err, "")
					continue
				}

				outputs.Set(output.Name, secretValue)
			}
		}
	}

	return outputs
}

func (m ModuleHelmManager) Uninstall(ctx context.Context, metaData base.MetaData) error {
	removeNamespace := false
	removePVCs := false

	if m.helmModule.Spec.Cleanup != nil {
		removeNamespace = m.helmModule.Spec.Cleanup.RemoveNamespace
		removePVCs = m.helmModule.Spec.Cleanup.RemovePVCs
	}

	err := m.helmService.UninstallRelease(ctx,
		m.releaseName, m.helmModule.Spec.Namespace,
		removeNamespace, removePVCs,
	)
	if err != nil {
		return err
	}

	return nil
}

type ReplicaHistory struct {
	Deployments  []services.ResourceReplicaCount `json:"deployments,omitempty"`
	ReplicaSets  []services.ResourceReplicaCount `json:"replicaSets,omitempty"`
	StatefulSets []services.ResourceReplicaCount `json:"statefulSets,omitempty"`
}

func (m ModuleHelmManager) Sleep(ctx context.Context, metaData base.MetaData) error {
	namespace := m.helmModule.Spec.Namespace
	if namespace == "" {
		namespace = kubernetesCons.Helm.DefaultNamespace
	}

	var replicaHistory ReplicaHistory

	oldReplicas, err := m.helmService.ScaleDeployments(ctx, m.releaseName, namespace, 0)
	for _, oldReplica := range oldReplicas {
		replicaHistory.Deployments = append(replicaHistory.Deployments, oldReplica)
		metaData[managerCons.HelmMetaDataKeys.ReplicaHistory] = replicaHistory
	}
	if err != nil {
		return err
	}

	oldReplicas, err = m.helmService.ScaleReplicaSets(ctx, m.releaseName, namespace, 0)
	for _, oldReplica := range oldReplicas {
		replicaHistory.ReplicaSets = append(replicaHistory.ReplicaSets, oldReplica)
		metaData[managerCons.HelmMetaDataKeys.ReplicaHistory] = replicaHistory
	}
	if err != nil {
		return err
	}

	oldReplicas, err = m.helmService.ScaleStatefulSets(ctx, m.releaseName, namespace, 0)
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
		return fmt.Errorf("replica history not found in metadata for module %s", m.helmModule.ObjectMeta.Name)
	}

	var parsedreplicaHistory ReplicaHistory
	if err := mapstructure.Decode(replicaHistory, &parsedreplicaHistory); err != nil {
		return err
	}

	if err := m.helmService.ScaleDeploymentsBack(ctx, parsedreplicaHistory.Deployments...); err != nil {
		return fmt.Errorf("failed to scale deployments back for module %s: %w", m.helmModule.ObjectMeta.Name, err)
	}
	if err := m.helmService.ScaleReplicaSetsBack(ctx, parsedreplicaHistory.ReplicaSets...); err != nil {
		return fmt.Errorf("failed to scale replica sets back for module %s: %w", m.helmModule.ObjectMeta.Name, err)
	}
	if err := m.helmService.ScaleStatefulSetsBack(ctx, parsedreplicaHistory.StatefulSets...); err != nil {
		return fmt.Errorf("failed to scale stateful sets back for module %s: %w", m.helmModule.ObjectMeta.Name, err)
	}

	return nil
}
