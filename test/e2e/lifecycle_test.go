//go:build e2e

// Copyright the Dploy authors.
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

// TestOnDemandLifecycle walks one claim through the whole chain and back out
// again: claim → instance → Flux source + HelmRelease → workload namespace with
// a running pod → delete → everything reclaimed. It is the test that proves the
// engine integration works at all, so the rest can assume it.
func TestOnDemandLifecycle(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	createTemplate(ctx, t, newTemplate("e2e-ondemand"))
	createClaim(ctx, t, newClaim("e2e-ondemand-claim", "e2e-ondemand", "alice"))

	claim := waitClaimBound(ctx, t, "e2e-ondemand-claim", instanceTimeout)
	inst := waitInstancePhase(ctx, t, claim.Status.InstanceRef, instanceTimeout, dployv1alpha1.PhaseReady)

	t.Run("instance status is fully populated", func(t *testing.T) {
		if inst.Status.UUID == "" {
			t.Error("status.uuid is empty")
		}
		if inst.Status.Namespace == "" {
			t.Error("status.namespace is empty")
		}
		if inst.Status.Engine != dployv1alpha1.EngineFlux {
			t.Errorf("status.engine = %q, want flux", inst.Status.Engine)
		}
		// The OperatorConfig template is "http://{{ .Host }}" and the fallback
		// host carries the template name, so both halves are asserted here.
		wantSuffix := "." + baseDomain
		if !strings.HasPrefix(inst.Status.URL, "http://") || !strings.HasSuffix(inst.Status.URL, wantSuffix) {
			t.Errorf("status.url = %q, want http://<host>%s", inst.Status.URL, wantSuffix)
		}
		if !strings.Contains(inst.Status.URL, "e2e-ondemand") {
			t.Errorf("status.url = %q, want the template name in the hostname", inst.Status.URL)
		}
	})

	t.Run("claim mirrors the instance", func(t *testing.T) {
		var got dployv1alpha1.DployInstanceClaim
		if err := k8s.Get(ctx, types.NamespacedName{Name: claim.Name, Namespace: testNS}, &got); err != nil {
			t.Fatalf("get claim: %v", err)
		}
		if got.Status.ConnectionURL != inst.Status.URL {
			t.Errorf("claim url = %q, instance url = %q", got.Status.ConnectionURL, inst.Status.URL)
		}
		if got.Status.UUID != inst.Status.UUID {
			t.Errorf("claim uuid = %q, instance uuid = %q", got.Status.UUID, inst.Status.UUID)
		}
		if got.Status.ExpiresAt == nil {
			t.Error("claim has no expiry despite a 3600s template TTL")
		}
		if cond := apimeta.FindStatusCondition(got.Status.Conditions, dployv1alpha1.ConditionBound); cond == nil || cond.Status != metav1.ConditionTrue {
			t.Errorf("Bound condition = %+v, want True", cond)
		}
	})

	t.Run("flux resources exist and are ready", func(t *testing.T) {
		var hr helmv2.HelmRelease
		if err := k8s.Get(ctx, types.NamespacedName{Name: inst.Name, Namespace: testNS}, &hr); err != nil {
			t.Fatalf("get HelmRelease %s: %v", inst.Name, err)
		}
		if hr.Spec.TargetNamespace != inst.Status.Namespace {
			t.Errorf("HelmRelease targetNamespace = %q, want %q", hr.Spec.TargetNamespace, inst.Status.Namespace)
		}
		if !apimeta.IsStatusConditionTrue(hr.Status.Conditions, "Ready") {
			t.Errorf("HelmRelease is not Ready: %+v", hr.Status.Conditions)
		}
	})

	t.Run("workload is actually running", func(t *testing.T) {
		eventually(t, instanceTimeout, "a running pod in the workload namespace", func() error {
			var pods corev1.PodList
			if err := k8s.List(ctx, &pods, client.InNamespace(inst.Status.Namespace)); err != nil {
				return err
			}
			if len(pods.Items) == 0 {
				return fmt.Errorf("no pods in %s", inst.Status.Namespace)
			}
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod %s is %s", p.Name, p.Status.Phase)
				}
			}
			return nil
		})
	})

	t.Run("labels mark ownership", func(t *testing.T) {
		if inst.Labels[dployv1alpha1.LabelTemplate] != "e2e-ondemand" {
			t.Errorf("template label = %q", inst.Labels[dployv1alpha1.LabelTemplate])
		}
		if inst.Labels[dployv1alpha1.LabelManaged] == "" {
			t.Error("managed label is missing")
		}
		if inst.Labels[dployv1alpha1.LabelClaimUID] != string(claim.UID) {
			t.Errorf("claim-uid label = %q, want %q", inst.Labels[dployv1alpha1.LabelClaimUID], claim.UID)
		}
	})

	// Teardown is the half that regresses quietly, so it is asserted rather
	// than left to t.Cleanup: a leaked namespace or HelmRelease is a real bug.
	t.Run("deleting the claim reclaims everything", func(t *testing.T) {
		workloadNS := inst.Status.Namespace
		if err := k8s.Delete(ctx, &dployv1alpha1.DployInstanceClaim{
			ObjectMeta: metav1.ObjectMeta{Name: claim.Name, Namespace: testNS},
		}); err != nil {
			t.Fatalf("delete claim: %v", err)
		}

		eventually(t, deleteTimeout, "the instance to be removed", func() error {
			var got dployv1alpha1.DployInstance
			err := k8s.Get(ctx, types.NamespacedName{Name: inst.Name, Namespace: testNS}, &got)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("still present in phase %q", got.Status.Phase)
		})

		eventually(t, deleteTimeout, "the HelmRelease to be removed", func() error {
			var hr helmv2.HelmRelease
			err := k8s.Get(ctx, types.NamespacedName{Name: inst.Name, Namespace: testNS}, &hr)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("HelmRelease %s still present", hr.Name)
		})

		eventually(t, deleteTimeout, "the workload namespace to be removed", func() error {
			gone, err := namespaceGone(ctx, workloadNS)
			if err != nil {
				return err
			}
			if !gone {
				return fmt.Errorf("namespace %s still present", workloadNS)
			}
			return nil
		})
	})
}

// TestClaimRejections covers the two refusals the operator must make up front,
// where "up front" matters: a rejected claim never provisions anything, so a
// regression here burns cluster capacity rather than returning an error.
func TestClaimRejections(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	t.Run("unknown template", func(t *testing.T) {
		createClaim(ctx, t, newClaim("e2e-reject-unknown", "no-such-template", "bob"))
		waitClaimPhase(ctx, t, "e2e-reject-unknown", dployv1alpha1.ClaimRejected, 90*time.Second)
	})

	t.Run("disabled template", func(t *testing.T) {
		tmpl := newTemplate("e2e-disabled")
		tmpl.Spec.Enabled = false
		createTemplate(ctx, t, tmpl)

		createClaim(ctx, t, newClaim("e2e-reject-disabled", "e2e-disabled", "bob"))
		waitClaimPhase(ctx, t, "e2e-reject-disabled", dployv1alpha1.ClaimRejected, 90*time.Second)

		instances, err := instancesFor(ctx, "e2e-disabled")
		if err != nil {
			t.Fatalf("list instances: %v", err)
		}
		if len(instances) != 0 {
			t.Errorf("a disabled template provisioned %d instance(s)", len(instances))
		}
	})
}

// waitClaimPhase waits for a claim to reach a specific phase.
func waitClaimPhase(ctx context.Context, t *testing.T, name string, want dployv1alpha1.ClaimPhase, timeout time.Duration) {
	t.Helper()
	eventually(t, timeout, fmt.Sprintf("claim %q to reach %q", name, want), func() error {
		var claim dployv1alpha1.DployInstanceClaim
		if err := k8s.Get(ctx, types.NamespacedName{Name: name, Namespace: testNS}, &claim); err != nil {
			return err
		}
		if claim.Status.Phase != want {
			return fmt.Errorf("phase=%q", claim.Status.Phase)
		}
		return nil
	})
}
