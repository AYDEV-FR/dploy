package kube

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AYDEV-FR/dploy/internal/config"
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
		// Fallback to kubeconfig
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		restConfig, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
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
		if env.Enabled {
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
	ttl := c.config.DefaultTTL
	if env.TTL != nil {
		ttl = *env.TTL
	}

	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second)
	expiresAtISO := expiresAt.UTC().Format(time.RFC3339) // ISO 8601 format

	appName := fmt.Sprintf("%s-%s-%s", username, envName, shortUUID)
	namespace := fmt.Sprintf("%s-%s-%s", username, envName, shortUUID)
	ingressHost := fmt.Sprintf("%s-%s.%s", username, shortUUID, c.config.BaseDomain)

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

	// Build Git source
	source := map[string]interface{}{
		"repoURL":        repoURL,
		"targetRevision": chartRevision,
		"path":           chartPath,
		"helm": map[string]interface{}{
			"values": helmValues,
		},
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
				"annotations": map[string]interface{}{
					"dploy.dev/uuid":       shortUUID,
					"dploy.dev/expires-at": expiresAtISO,
				},
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

	var expiresTime time.Time
	if currentExpires != "" {
		// Parse ISO 8601 timestamp
		parsedTime, parseErr := time.Parse(time.RFC3339, currentExpires)
		if parseErr == nil {
			expiresTime = parsedTime
		} else {
			expiresTime = time.Now()
		}
	} else {
		expiresTime = time.Now()
	}

	newExpires := expiresTime.Add(time.Duration(c.config.ExtendTTL) * time.Second)
	newExpiresISO := newExpires.UTC().Format(time.RFC3339) // ISO 8601 format

	// Update expires-at annotation
	annotations["dploy.dev/expires-at"] = newExpiresISO
	app.SetAnnotations(annotations)

	_, err = c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Update(ctx, app, metav1.UpdateOptions{})
	return newExpires, err
}

func (c *Client) DeleteApplication(ctx context.Context, appName string) error {
	// Get the application first to extract the namespace
	app, err := c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Extract namespace from spec.destination.namespace
	namespace, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")

	// Remove finalizers to prevent the application from being stuck in "Deleting" state
	// This allows immediate deletion without waiting for ArgoCD to clean up resources
	app.SetFinalizers(nil)
	_, err = c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Update(ctx, app, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove finalizers: %w", err)
	}

	// Delete the Application
	if err := c.dynamic.Resource(applicationGVR).Namespace(c.config.ArgoCDNamespace).Delete(ctx, appName, metav1.DeleteOptions{}); err != nil {
		return err
	}

	// Delete the namespace separately since we removed the ArgoCD finalizer
	if namespace != "" {
		namespaceGVR := schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "namespaces",
		}

		// Ignore errors if namespace doesn't exist or is already being deleted
		//nolint:errcheck // Intentionally ignoring error - namespace may not exist or already be deleted
		_ = c.dynamic.Resource(namespaceGVR).Delete(ctx, namespace, metav1.DeleteOptions{}) // #nosec G104
	}

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
