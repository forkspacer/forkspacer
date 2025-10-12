package manager

import (
	"context"
	"fmt"

	kubernetesCons "github.com/forkspacer/forkspacer/pkg/constants/kubernetes"
	managerCons "github.com/forkspacer/forkspacer/pkg/constants/manager"
	"github.com/forkspacer/forkspacer/pkg/manager/base"
	"github.com/forkspacer/forkspacer/pkg/resources"
	"github.com/forkspacer/forkspacer/pkg/services"
	"github.com/go-logr/logr"
	"github.com/go-viper/mapstructure/v2"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

var _ base.IManager = ModuleHelmManager{}

type ModuleHelmManager struct {
	helmModule  *resources.HelmModule
	helmService *services.HelmService
	releaseName string
	log         logr.Logger
}

func NewModuleHelmManager(
	helmModule *resources.HelmModule,
	helmService *services.HelmService,
	releaseName string,
	logger logr.Logger,
) (base.IManager, error) {
	if err := helmModule.Validate(); err != nil {
		return nil, err
	}

	return &ModuleHelmManager{
		helmModule:  helmModule,
		helmService: helmService,
		releaseName: releaseName,
		log:         logger,
	}, nil
}

func (m ModuleHelmManager) Install(ctx context.Context, metaData base.MetaData) error {
	namespace := m.helmModule.Spec.Namespace
	if namespace == "" {
		namespace = kubernetesCons.Helm.DefaultNamespace
	}

	err := m.helmService.InstallFromRepository(
		ctx,
		m.helmModule.Spec.ChartName,
		m.releaseName,
		namespace,
		m.helmModule.Spec.Repo,
		m.helmModule.Spec.Version,
		true,
		m.helmModule.Spec.Values,
	)
	if err != nil {
		return err
	}

	if outputs := m.extractOutputs(ctx); outputs.Len() > 0 {
		metaData[managerCons.HelmMetaDataKeys.InstallOutputs] = outputs
	}

	return err
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
	err := m.helmService.UninstallRelease(ctx,
		m.releaseName, m.helmModule.Spec.Namespace,
		m.helmModule.Spec.Cleanup.RemoveNamespace, m.helmModule.Spec.Cleanup.RemovePVCs,
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
