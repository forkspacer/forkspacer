package utils

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func WriteHTTPDataToFile(
	ctx context.Context,
	fileURL, writePath string,
	override, fileHash bool,
) (*os.File, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform HTTP request to '%s': %w", fileURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if fileHash {
		hasher := sha256.New()
		if _, err := hasher.Write(body); err != nil {
			return nil, fmt.Errorf("failed to write response body data to hasher: %w", err)
		}
		writePath = fmt.Sprintf(writePath, hasher.Sum(nil))
	}

	if !override {
		if _, err := os.Stat(writePath); err == nil {
			file, err := os.Open(writePath)
			if err != nil {
				return nil, fmt.Errorf("failed to open existing file '%s': %w", writePath, err)
			}

			return file, nil
		}
	}

	dir := filepath.Dir(writePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory '%s': %w", dir, err)
	}

	file, err := os.Create(writePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	if _, err = file.Write(body); err != nil {
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("failed to write remote file content to the file: %w", err)
	}

	return file, nil
}

func WriteConfigMapToFile(
	ctx context.Context,
	controllerClient client.Client,
	namespace, name, key, writePath string,
	override, fileHash bool,
) (*os.File, error) {
	if namespace == "" {
		namespace = "default"
	}

	configMap := &corev1.ConfigMap{}
	err := controllerClient.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, configMap)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get ConfigMap '%s' in namespace '%s': %w",
			name, namespace, err,
		)
	}

	configMapData := configMap.Data[key]
	if configMapData == "" {
		return nil, fmt.Errorf(
			"ConfigMap '%s' in namespace '%s' does not contain data under key '%s'",
			name, namespace, key,
		)
	}

	if fileHash {
		hasher := sha256.New()
		if _, err := hasher.Write([]byte(configMapData)); err != nil {
			return nil, fmt.Errorf("failed to write config map data to hasher: %w", err)
		}
		writePath = fmt.Sprintf(writePath, hasher.Sum(nil))
	}

	if !override {
		if _, err := os.Stat(writePath); err == nil {
			file, err := os.Open(writePath)
			if err != nil {
				return nil, fmt.Errorf("failed to open existing file '%s': %w", writePath, err)
			}

			return file, nil
		}
	}

	dir := filepath.Dir(writePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory '%s': %w", dir, err)
	}

	file, err := os.Create(writePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	if _, err := file.WriteString(configMapData); err != nil {
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("failed to write config map data to file: %w", err)
	}

	return file, nil
}

func WriteSecretToFile(
	ctx context.Context,
	controllerClient client.Client,
	namespace, name, key, writePath string,
	override, fileHash bool,
) (*os.File, error) {
	if namespace == "" {
		namespace = "default"
	}

	secret := &corev1.Secret{}
	err := controllerClient.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get Secret '%s' in namespace '%s': %w",
			name, namespace, err,
		)
	}

	secretData := secret.Data[key]
	if len(secretData) == 0 {
		return nil, fmt.Errorf(
			"secret '%s' in namespace '%s' does not contain data under key '%s'",
			name, namespace, key,
		)
	}

	if fileHash {
		hasher := sha256.New()
		if _, err := hasher.Write(secretData); err != nil {
			return nil, fmt.Errorf("failed to write secret data to hasher: %w", err)
		}
		writePath = fmt.Sprintf(writePath, hasher.Sum(nil))
	}

	if !override {
		if _, err := os.Stat(writePath); err == nil {
			file, err := os.Open(writePath)
			if err != nil {
				return nil, fmt.Errorf("failed to open existing file '%s': %w", writePath, err)
			}

			return file, nil
		}
	}

	dir := filepath.Dir(writePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory '%s': %w", dir, err)
	}

	file, err := os.Create(writePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	if _, err := file.Write(secretData); err != nil {
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("failed to write secret data to file: %w", err)
	}

	return file, nil
}
