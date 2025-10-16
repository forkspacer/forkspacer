package services

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/remotecommand"
)

type PVCMigrationService struct {
	logger *logr.Logger
}

func NewPVCMigrationService(logger *logr.Logger) *PVCMigrationService {
	return &PVCMigrationService{logger}
}

type migrationPodConfig struct {
	name       string
	namespace  string
	pvcName    string
	image      string
	command    []string
	args       []string
	restConfig *rest.Config
	clientset  *kubernetes.Clientset
}

func (s *PVCMigrationService) MigratePVC(
	ctx context.Context,
	sourceConfig *clientcmdapi.Config,
	sourcePVCName, sourcePVCNamespace string,
	destConfig *clientcmdapi.Config,
	destPVCName, destPVCNamespace string,
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

	timestamp := time.Now().Unix()
	sourcePodName := fmt.Sprintf("pvc-migration-source-%d", timestamp)
	destPodName := fmt.Sprintf("pvc-migration-dest-%d", timestamp)

	sourcePodConfig := &migrationPodConfig{
		name:       sourcePodName,
		namespace:  sourcePVCNamespace,
		pvcName:    sourcePVCName,
		image:      "alpine:latest",
		command:    []string{"tail", "-f", "/dev/null"},
		restConfig: sourceRestConfig,
		clientset:  sourceClientset,
	}

	destPodConfig := &migrationPodConfig{
		name:       destPodName,
		namespace:  destPVCNamespace,
		pvcName:    destPVCName,
		image:      "alpine:latest",
		command:    []string{"tail", "-f", "/dev/null"},
		restConfig: destRestConfig,
		clientset:  destClientset,
	}

	_, err = s.createMigrationPod(ctx, sourcePodConfig, "/data")
	if err != nil {
		return fmt.Errorf("failed to create source migration pod: %w", err)
	}
	defer s.cleanupPod(context.Background(), sourceClientset, sourcePodName, sourcePVCNamespace)

	_, err = s.createMigrationPod(ctx, destPodConfig, "/data")
	if err != nil {
		return fmt.Errorf("failed to create destination migration pod: %w", err)
	}
	defer s.cleanupPod(context.Background(), destClientset, destPodName, destPVCNamespace)

	if err := s.waitForPodReady(ctx, sourceClientset, sourcePodName, sourcePVCNamespace); err != nil {
		return fmt.Errorf("source pod did not become ready: %w", err)
	}

	if err := s.waitForPodReady(ctx, destClientset, destPodName, destPVCNamespace); err != nil {
		return fmt.Errorf("destination pod did not become ready: %w", err)
	}

	if err := s.migrateDataViaTar(ctx,
		sourceRestConfig, sourceClientset, sourcePodName, sourcePVCNamespace,
		destRestConfig, destClientset, destPodName, destPVCNamespace,
	); err != nil {
		return fmt.Errorf("failed to migrate data: %w", err)
	}

	return nil
}

func (s *PVCMigrationService) createMigrationPod(
	ctx context.Context,
	config *migrationPodConfig,
	mountPath string,
) (*corev1.Pod, error) { //nolint:unparam
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.name,
			Namespace: config.namespace,
			Labels: map[string]string{
				"app": "pvc-migration",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "migration",
					Image:   config.image,
					Command: config.command,
					Args:    config.args,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data",
							MountPath: mountPath,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: config.pvcName,
						},
					},
				},
			},
		},
	}

	createdPod, err := config.clientset.CoreV1().Pods(config.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod %s/%s: %w", config.namespace, config.name, err)
	}

	return createdPod, nil
}

func (s *PVCMigrationService) waitForPodReady(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	podName, namespace string,
) error {
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for pod %s/%s to become ready", namespace, podName)
		case <-ticker.C:
			pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					continue
				}
				return fmt.Errorf("failed to get pod %s/%s: %w", namespace, podName, err)
			}

			if pod.Status.Phase == corev1.PodRunning {
				allReady := true
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						return nil
					}
					if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
						allReady = false
					}
				}
				if allReady && len(pod.Status.Conditions) > 0 {
					return nil
				}
			}

			if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
				return fmt.Errorf("pod %s/%s entered terminal phase: %s", namespace, podName, pod.Status.Phase)
			}
		}
	}
}

func (s *PVCMigrationService) migrateDataViaTar(
	ctx context.Context,
	sourceRestConfig *rest.Config,
	sourceClientset *kubernetes.Clientset,
	sourcePodName, sourcePodNamespace string,
	destRestConfig *rest.Config,
	destClientset *kubernetes.Clientset,
	destPodName, destPodNamespace string,
) error {
	pipeReader, pipeWriter := io.Pipe()

	sourceErrChan := make(chan error, 1)
	destErrChan := make(chan error, 1)

	go func() {
		defer func() {
			if err := pipeWriter.Close(); err != nil {
				s.logger.Error(err, "failed to close tar command pipe writer")
			}
		}()

		tarCmd := []string{"tar", "-czf", "-", "-C", "/data", "."}
		if err := s.execInPodWithStreams(ctx,
			sourceRestConfig, sourceClientset, sourcePodName, sourcePodNamespace,
			tarCmd, nil, pipeWriter, io.Discard,
		); err != nil {
			sourceErrChan <- fmt.Errorf("failed to tar source data: %w", err)
			return
		}
		sourceErrChan <- nil
	}()

	go func() {
		defer func() {
			if err := pipeReader.Close(); err != nil {
				s.logger.Error(err, "failed to close tar command pipe writer")
			}
		}()

		untarCmd := []string{"tar", "-xzf", "-", "-C", "/data"}
		if err := s.execInPodWithStreams(ctx,
			destRestConfig, destClientset, destPodName, destPodNamespace,
			untarCmd, pipeReader, io.Discard, io.Discard,
		); err != nil {
			destErrChan <- fmt.Errorf("failed to untar to destination: %w", err)
			return
		}
		destErrChan <- nil
	}()

	sourceErr := <-sourceErrChan
	destErr := <-destErrChan

	if sourceErr != nil {
		return sourceErr
	}
	if destErr != nil {
		return destErr
	}

	return nil
}

func (s *PVCMigrationService) execInPodWithStreams(
	ctx context.Context,
	restConfig *rest.Config,
	clientset *kubernetes.Clientset,
	podName, namespace string,
	command []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdin:   stdin != nil,
			Stdout:  stdout != nil,
			Stderr:  stderr != nil,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})

	if err != nil {
		return fmt.Errorf("command execution failed: %w", err)
	}

	return nil
}

func (s *PVCMigrationService) cleanupPod(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	podName, namespace string,
) {
	deletePolicy := metav1.DeletePropagationForeground
	err := clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
	if err != nil && !errors.IsNotFound(err) {
		s.logger.Error(err, "failed to delete pod", "podName", podName, "namespace", namespace)
	}
}
