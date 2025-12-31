package kube

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
	"github.com/AYDEV-FR/dploy/internal/logger"
	"github.com/AYDEV-FR/dploy/internal/models"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var applicationGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "applications",
}

type Client struct {
	dynamic dynamic.Interface
	config  *config.Config
}

// GetConfig returns the client's configuration.
func (c *Client) GetConfig() *config.Config {
	return c.config
}

func NewClient(cfg *config.Config) (*Client, error) {
	var restConfig *rest.Config
	var err error

	// Try in-cluster config first
	restConfig, err = rest.InClusterConfig()
	if err != nil {
		logger.Debug("In-cluster config not available, falling back to kubeconfig")
		// Fallback to kubeconfig
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		restConfig, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
		logger.Debug("Using kubeconfig for Kubernetes connection")
	} else {
		logger.Debug("Using in-cluster config for Kubernetes connection")
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &Client{
		dynamic: dynamicClient,
		config:  cfg,
	}, nil
}

func (c *Client) GetEnvironment(name string) (*models.Environment, error) {
	for _, env := range c.config.Environments {
		if env.Name == name && env.Enabled {
			return &env, nil
		}
	}
	return nil, fmt.Errorf("environment not found or disabled: %s", name)
}

func (c *Client) ListAvailableEnvironments() []models.Environment {
	var available []models.Environment
	for _, env := range c.config.Environments {
		if env.Enabled && env.IsVisible() {
			available = append(available, env)
		}
	}
	return available
}

func (c *Client) GetEnvironmentByName(name string) *models.Environment {
	for _, env := range c.config.Environments {
		if env.Name == name {
			return &env
		}
	}
	return nil
}

func (c *Client) ListUserApplications(ctx context.Context, username string) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("dploy.dev/owner=%s", username),
	})
}

// ListAllDployApplications lists all ArgoCD Applications managed by dploy.
func (c *Client) ListAllDployApplications(ctx context.Context) (*unstructured.UnstructuredList, error) {
	return c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "dploy.dev/owner",
	})
}

func (c *Client) GetUserApplication(ctx context.Context, username, envName string) (*unstructured.Unstructured, error) {
	apps, err := c.ListUserApplications(ctx, username)
	if err != nil {
		return nil, err
	}

	for _, app := range apps.Items {
		labels := app.GetLabels()
		if labels["dploy.dev/env"] == envName {
			return &app, nil
		}
	}

	return nil, nil
}

func (c *Client) CreateApplication(ctx context.Context, username, envName string, env *models.Environment) (*unstructured.Unstructured, error) {
	shortUUID := generateShortUUID()

	// Parse TTL configuration
	ttlConfig := env.ParseTTL()
	ttl := c.config.DefaultTTL
	if ttlConfig != nil {
		ttl = ttlConfig.TTL
	}

	appName := fmt.Sprintf("%s-%s-%s", username, envName, shortUUID)
	namespace := fmt.Sprintf("%s-%s-%s", username, envName, shortUUID)
	ingressHost := fmt.Sprintf("%s-%s.%s", username, shortUUID, c.config.BaseDomain)

	// Build annotations
	annotations := map[string]interface{}{
		"dploy.dev/uuid":          shortUUID,
		"dploy.dev/extend-count":  "0",
	}

	// Handle TTL: -1 means unlimited (no expiration)
	if ttl == -1 {
		logger.Debug("Creating ArgoCD application with unlimited TTL",
			"appName", appName,
			"namespace", namespace,
			"ingressHost", ingressHost,
		)
		// No expires-at annotation for unlimited TTL
	} else {
		expiresAt := time.Now().Add(time.Duration(ttl) * time.Second)
		expiresAtISO := expiresAt.UTC().Format(time.RFC3339)
		annotations["dploy.dev/expires-at"] = expiresAtISO

		logger.Debug("Creating ArgoCD application",
			"appName", appName,
			"namespace", namespace,
			"ingressHost", ingressHost,
			"ttl", ttl,
			"expiresAt", expiresAtISO,
		)
	}

	// Store extend configuration if specified
	if ttlConfig != nil {
		if ttlConfig.HasExtend {
			annotations["dploy.dev/extend-ttl"] = fmt.Sprintf("%d", ttlConfig.ExtendTTL)
		}
		if ttlConfig.HasMax {
			annotations["dploy.dev/max-extends"] = fmt.Sprintf("%d", ttlConfig.MaxExtends)
		}
	}

	// Build base Helm values
	helmValues := fmt.Sprintf("username: %s\nuuid: %s\ningressHost: %s", username, shortUUID, ingressHost)

	// Merge with extraValues if provided
	if env.ExtraValues != "" {
		// Replace variables in extraValues
		extraValues := replaceVariables(env.ExtraValues, username, shortUUID, ingressHost)
		helmValues = helmValues + "\n" + extraValues
	}

	// Parse chart string to get repo URL, path, and revision
	repoURL, chartPath, chartRevision := env.ParseChart()
	logger.Debug("Chart configuration",
		"repoURL", repoURL,
		"chartPath", chartPath,
		"chartRevision", chartRevision,
	)
	logger.Debug("Helm values", "values", helmValues)

	// Build Helm configuration
	helmConfig := map[string]interface{}{
		"values": helmValues,
	}

	// Add valueFiles if specified
	if len(env.ValueFiles) > 0 {
		valueFiles := make([]interface{}, len(env.ValueFiles))
		for i, f := range env.ValueFiles {
			valueFiles[i] = f
		}
		helmConfig["valueFiles"] = valueFiles
		logger.Debug("Using custom value files", "valueFiles", env.ValueFiles)
	}

	// Build Git source
	source := map[string]interface{}{
		"repoURL":        repoURL,
		"targetRevision": chartRevision,
		"path":           chartPath,
		"helm":           helmConfig,
	}

	app := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]interface{}{
				"name":      appName,
				"namespace": c.config.ArgoCDNamespace,
				"finalizers": []interface{}{
					"argoproj.io/resources-finalizer",
				},
				"labels": map[string]interface{}{
					"dploy.dev/owner": username,
					"dploy.dev/env":   envName,
				},
				"annotations": annotations,
			},
			"spec": map[string]interface{}{
				"project": c.config.ArgoCDProject,
				"source":  source,
				"destination": map[string]interface{}{
					"server":    "https://kubernetes.default.svc",
					"namespace": namespace,
				},
				"syncPolicy": map[string]interface{}{
					"automated": map[string]interface{}{
						"prune":    true,
						"selfHeal": true,
					},
					"syncOptions": []interface{}{
						"CreateNamespace=true",
					},
					"managedNamespaceMetadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"dploy.dev/managed": "true",
							"dploy.dev/owner":   username,
						},
					},
				},
			},
		},
	}

	return c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Create(ctx, app, metav1.CreateOptions{})
}

func (c *Client) ExtendApplication(ctx context.Context, appName string) (time.Time, error) {
	app, err := c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, err
	}

	annotations := app.GetAnnotations()
	currentExpires := annotations["dploy.dev/expires-at"]

	// Check if this is an unlimited TTL environment
	if currentExpires == "" {
		return time.Time{}, fmt.Errorf("environment has unlimited TTL, no extension needed")
	}

	// Check max extends limit
	extendCountStr := annotations["dploy.dev/extend-count"]
	extendCount := 0
	if extendCountStr != "" {
		if count, err := strconv.Atoi(extendCountStr); err == nil {
			extendCount = count
		}
	}

	maxExtendsStr := annotations["dploy.dev/max-extends"]
	if maxExtendsStr != "" {
		if maxExtends, err := strconv.Atoi(maxExtendsStr); err == nil && maxExtends >= 0 {
			if extendCount >= maxExtends {
				return time.Time{}, fmt.Errorf("maximum extensions (%d) reached", maxExtends)
			}
		}
	}

	// Determine extend TTL (use per-environment value if set, otherwise use default)
	extendTTL := c.config.ExtendTTL
	extendTTLStr := annotations["dploy.dev/extend-ttl"]
	if extendTTLStr != "" {
		if customExtendTTL, err := strconv.Atoi(extendTTLStr); err == nil {
			extendTTL = customExtendTTL
		}
	}

	// Parse current expiration time
	var expiresTime time.Time
	parsedTime, parseErr := time.Parse(time.RFC3339, currentExpires)
	if parseErr == nil {
		expiresTime = parsedTime
	} else {
		expiresTime = time.Now()
	}

	// Calculate new expiration
	newExpires := expiresTime.Add(time.Duration(extendTTL) * time.Second)
	newExpiresISO := newExpires.UTC().Format(time.RFC3339)

	// Update annotations
	annotations["dploy.dev/expires-at"] = newExpiresISO
	annotations["dploy.dev/extend-count"] = fmt.Sprintf("%d", extendCount+1)
	app.SetAnnotations(annotations)

	_, err = c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Update(ctx, app, metav1.UpdateOptions{})
	return newExpires, err
}

func (c *Client) DeleteApplication(ctx context.Context, appName string) error {
	logger.Debug("Deleting ArgoCD application", "appName", appName)

	// Get the application first to extract the namespace
	app, err := c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		logger.Error("Failed to get application", "appName", appName, "error", err)
		return err
	}

	// Extract namespace from spec.destination.namespace
	namespace, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
	logger.Debug("Application namespace", "appName", appName, "namespace", namespace)

	// Remove finalizers to prevent the application from being stuck in "Deleting" state
	// This allows immediate deletion without waiting for ArgoCD to clean up resources
	logger.Debug("Removing finalizers", "appName", appName)
	app.SetFinalizers(nil)
	_, err = c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Update(ctx, app, metav1.UpdateOptions{})
	if err != nil {
		logger.Error("Failed to remove finalizers", "appName", appName, "error", err)
		return fmt.Errorf("failed to remove finalizers: %w", err)
	}

	// Delete the Application
	logger.Debug("Deleting ArgoCD application resource", "appName", appName)
	if err := c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Delete(ctx, appName, metav1.DeleteOptions{}); err != nil {
		logger.Error("Failed to delete application", "appName", appName, "error", err)
		return err
	}

	// Delete the namespace separately since we removed the ArgoCD finalizer
	if namespace != "" {
		namespaceGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "namespaces",
		}

		logger.Debug("Deleting namespace", "namespace", namespace)
		// Ignore errors if namespace doesn't exist or is already being deleted
		//nolint:errcheck // Intentionally ignoring error - namespace may not exist or already be deleted
		_ = c.dynamic.Resource(namespaceGVR).Delete(ctx, namespace, metav1.DeleteOptions{}) // #nosec G104
	}

	logger.Debug("Successfully deleted application", "appName", appName)
	return nil
}

func (c *Client) GenerateURL(username, uuid string) string {
	return fmt.Sprintf("https://%s-%s.%s", username, uuid, c.config.BaseDomain)
}

// replaceVariables replaces dynamic variables in extraValues.
// Supported variables: ${username}, ${user}, ${uuid}, ${ingressHost}, ${ingress}.
func replaceVariables(values, username, uuid, ingressHost string) string {
	replacer := strings.NewReplacer(
		"${username}", username,
		"${user}", username,
		"${uuid}", uuid,
		"${ingressHost}", ingressHost,
		"${ingress}", ingressHost,
	)
	return replacer.Replace(values)
}

func generateShortUUID() string {
	fullUUID := uuid.New().String()
	return strings.ReplaceAll(fullUUID, "-", "")[:8]
}
