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

type SecretMigrationService struct {
	logger *logr.Logger
}

func NewSecretMigrationService(logger *logr.Logger) *SecretMigrationService {
	return &SecretMigrationService{logger}
}

func (s *SecretMigrationService) MigrateSecret(
	ctx context.Context,
	sourceConfig *clientcmdapi.Config,
	sourceSecretName, sourceSecretNamespace string,
	destConfig *clientcmdapi.Config,
	destSecretName, destSecretNamespace string,
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

	sourceSecret, err := sourceClientset.CoreV1().
		Secrets(sourceSecretNamespace).
		Get(ctx, sourceSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get source secret %s/%s: %w", sourceSecretNamespace, sourceSecretName, err)
	}

	destSecret, err := destClientset.CoreV1().Secrets(destSecretNamespace).Get(ctx, destSecretName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get destination secret %s/%s: %w", destSecretNamespace, destSecretName, err)
		}

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        destSecretName,
				Namespace:   destSecretNamespace,
				Labels:      sourceSecret.Labels,
				Annotations: sourceSecret.Annotations,
			},
			Type:       sourceSecret.Type,
			Data:       sourceSecret.Data,
			StringData: sourceSecret.StringData,
		}

		_, err = destClientset.CoreV1().Secrets(destSecretNamespace).Create(ctx, newSecret, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create destination secret %s/%s: %w", destSecretNamespace, destSecretName, err)
		}

		s.logger.Info("created destination secret", "name", destSecretName, "namespace", destSecretNamespace)
		return nil
	}

	if destSecret.Immutable != nil && *destSecret.Immutable {
		s.logger.Info("destination secret is immutable, deleting and recreating",
			"name", destSecretName, "namespace", destSecretNamespace)

		preservedLabels := destSecret.Labels
		preservedAnnotations := destSecret.Annotations

		err = destClientset.CoreV1().Secrets(destSecretNamespace).Delete(ctx, destSecretName, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf(
				"failed to delete immutable destination secret %s/%s: %w",
				destSecretNamespace, destSecretName, err,
			)
		}

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        destSecretName,
				Namespace:   destSecretNamespace,
				Labels:      preservedLabels,
				Annotations: preservedAnnotations,
			},
			Type:       sourceSecret.Type,
			Data:       sourceSecret.Data,
			StringData: sourceSecret.StringData,
			Immutable:  destSecret.Immutable,
		}

		_, err = destClientset.CoreV1().Secrets(destSecretNamespace).Create(ctx, newSecret, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf(
				"failed to recreate immutable destination secret %s/%s: %w",
				destSecretNamespace, destSecretName, err,
			)
		}

		s.logger.Info("recreated immutable destination secret", "name", destSecretName, "namespace", destSecretNamespace)
		return nil
	}

	destSecret.Data = sourceSecret.Data
	destSecret.StringData = sourceSecret.StringData
	destSecret.Type = sourceSecret.Type

	_, err = destClientset.CoreV1().Secrets(destSecretNamespace).Update(ctx, destSecret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update destination secret %s/%s: %w", destSecretNamespace, destSecretName, err)
	}

	s.logger.Info("updated destination secret", "name", destSecretName, "namespace", destSecretNamespace)
	return nil
}
