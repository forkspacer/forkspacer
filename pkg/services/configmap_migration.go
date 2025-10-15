package services

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ConfigMapMigrationService struct {
	logger *logr.Logger
}

func NewConfigMapMigrationService(logger *logr.Logger) *ConfigMapMigrationService {
	return &ConfigMapMigrationService{logger}
}

func (s *ConfigMapMigrationService) MigrateConfigMap(
	ctx context.Context,
	sourceConfig *clientcmdapi.Config,
	sourceConfigMapName, sourceConfigMapNamespace string,
	destConfig *clientcmdapi.Config,
	destConfigMapName, destConfigMapNamespace string,
) error {
	sourceRestConfig, err := clientcmd.NewDefaultClientConfig(*sourceConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to create source REST config: %w", err)
	}

	destRestConfig, err := clientcmd.NewDefaultClientConfig(*destConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to create destination REST config: %w", err)
	}

	sourceClientset, err := kubernetes.NewForConfig(sourceRestConfig)
	if err != nil {
		return fmt.Errorf("failed to create source clientset: %w", err)
	}

	destClientset, err := kubernetes.NewForConfig(destRestConfig)
	if err != nil {
		return fmt.Errorf("failed to create destination clientset: %w", err)
	}

	sourceConfigMap, err := sourceClientset.CoreV1().
		ConfigMaps(sourceConfigMapNamespace).
		Get(ctx, sourceConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get source configmap %s/%s: %w", sourceConfigMapNamespace, sourceConfigMapName, err)
	}

	destConfigMap, err := destClientset.CoreV1().
		ConfigMaps(destConfigMapNamespace).
		Get(ctx, destConfigMapName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get destination configmap %s/%s: %w", destConfigMapNamespace, destConfigMapName, err)
		}

		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        destConfigMapName,
				Namespace:   destConfigMapNamespace,
				Labels:      sourceConfigMap.Labels,
				Annotations: sourceConfigMap.Annotations,
			},
			Data:       sourceConfigMap.Data,
			BinaryData: sourceConfigMap.BinaryData,
		}

		_, err = destClientset.CoreV1().ConfigMaps(destConfigMapNamespace).Create(ctx, newConfigMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create destination configmap %s/%s: %w", destConfigMapNamespace, destConfigMapName, err)
		}

		s.logger.Info("created destination configmap", "name", destConfigMapName, "namespace", destConfigMapNamespace)
		return nil
	}

	if destConfigMap.Immutable != nil && *destConfigMap.Immutable {
		s.logger.Info("destination configmap is immutable, deleting and recreating",
			"name", destConfigMapName, "namespace", destConfigMapNamespace)

		preservedLabels := destConfigMap.Labels
		preservedAnnotations := destConfigMap.Annotations

		err = destClientset.CoreV1().ConfigMaps(destConfigMapNamespace).Delete(ctx, destConfigMapName, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf(
				"failed to delete immutable destination configmap %s/%s: %w",
				destConfigMapNamespace, destConfigMapName, err,
			)
		}

		newConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:        destConfigMapName,
				Namespace:   destConfigMapNamespace,
				Labels:      preservedLabels,
				Annotations: preservedAnnotations,
			},
			Data:       sourceConfigMap.Data,
			BinaryData: sourceConfigMap.BinaryData,
			Immutable:  destConfigMap.Immutable,
		}

		_, err = destClientset.CoreV1().ConfigMaps(destConfigMapNamespace).Create(ctx, newConfigMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf(
				"failed to recreate immutable destination configmap %s/%s: %w",
				destConfigMapNamespace, destConfigMapName, err,
			)
		}

		s.logger.Info("recreated immutable destination configmap",
			"name", destConfigMapName,
			"namespace", destConfigMapNamespace,
		)
		return nil
	}

	destConfigMap.Data = sourceConfigMap.Data
	destConfigMap.BinaryData = sourceConfigMap.BinaryData

	_, err = destClientset.CoreV1().ConfigMaps(destConfigMapNamespace).Update(ctx, destConfigMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update destination configmap %s/%s: %w", destConfigMapNamespace, destConfigMapName, err)
	}

	s.logger.Info("updated destination configmap", "name", destConfigMapName, "namespace", destConfigMapNamespace)
	return nil
}
