//go:build e2e

// Stress campaign: probes the operator's limits rather than its happy path.
// Guarded by E2E_STRESS=1 so it never runs as part of the normal e2e suite.
package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dployv1alpha1 "github.com/AYDEV-FR/dploy/api/v1alpha1"
)

func requireStress(t *testing.T) {
	t.Helper()
	requireCluster(t)
	if os.Getenv("E2E_STRESS") != "1" {
		t.Skip("stress suite: set E2E_STRESS=1")
	}
}

func stressInt(key string, def int) int { return envInt(key, def) }

// bindings returns instanceRef -> []claimName for every Bound claim, which is
// where a double-binding would show up.
func bindings(ctx context.Context) (map[string][]string, map[dployv1alpha1.ClaimPhase]int, error) {
	var list dployv1alpha1.DployInstanceClaimList
	if err := k8s.List(ctx, &list, client.InNamespace(testNS)); err != nil {
		return nil, nil, err
	}
	byInstance := map[string][]string{}
	phases := map[dployv1alpha1.ClaimPhase]int{}
	for i := range list.Items {
		c := &list.Items[i]
		phases[c.Status.Phase]++
		if c.Status.Phase == dployv1alpha1.ClaimBound && c.Status.InstanceRef != "" {
			byInstance[c.Status.InstanceRef] = append(byInstance[c.Status.InstanceRef], c.Name)
		}
	}
	return byInstance, phases, nil
}

// assertNoDoubleBinding is the invariant that matters most under concurrency:
// two claimants must never be handed the same environment.
func assertNoDoubleBinding(t *testing.T, byInstance map[string][]string) {
	t.Helper()
	for inst, claims := range byInstance {
		if len(claims) > 1 {
			t.Errorf("DOUBLE-BINDING: instance %s bound to %d claims: %v", inst, len(claims), claims)
		}
	}
}

// fireClaims creates n claims concurrently and returns their names plus any
// create errors. t.Fatalf is not goroutine-safe, so errors come back instead.
func fireClaims(ctx context.Context, t *testing.T, tmplName, prefix string, n int, owner func(int) string) ([]string, []error) {
	t.Helper()
	names := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		names[i] = fmt.Sprintf("%s-%d", prefix, i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them all at once
			c := newClaim(names[i], tmplName, owner(i))
			errs[i] = k8s.Create(ctx, c)
		}(i)
	}
	close(start)
	wg.Wait()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		_ = k8s.DeleteAllOf(cctx, &dployv1alpha1.DployInstanceClaim{}, client.InNamespace(testNS))
	})
	var out []error
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	return names, out
}

// S1: the CTF-start case. A warm pool smaller than the crowd, everyone claiming
// at the same instant. Nobody may receive an environment somebody else holds.
func TestStressClaimStorm(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	pool := stressInt("E2E_STRESS_POOL", 20)
	claimants := stressInt("E2E_STRESS_CLAIMANTS", 60)

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-storm", pool, 0))
	waitPoolAvailable(ctx, t, tmpl.Name, pool, poolBudget(pool))
	t.Logf("pool of %d warm; firing %d simultaneous claims", pool, claimants)

	began := time.Now()
	_, errs := fireClaims(ctx, t, tmpl.Name, "s-storm-c", claimants, func(i int) string {
		return fmt.Sprintf("team%03d", i) // distinct owners: quota is not the subject here
	})
	for _, e := range errs {
		t.Errorf("claim create failed: %v", e)
	}

	// Let it converge, then hold and re-check: a late reconcile must not hand
	// out a second copy of an environment.
	eventually(t, poolBudget(claimants)+2*time.Minute, "every claim to settle", func() error {
		_, phases, err := bindings(ctx)
		if err != nil {
			return err
		}
		if n := phases[dployv1alpha1.ClaimPending]; n > 0 {
			return fmt.Errorf("%d claims still Pending (phases: %v)", n, phases)
		}
		return nil
	})
	t.Logf("settled in %s", time.Since(began).Round(time.Second))

	for range 6 {
		byInstance, phases, err := bindings(ctx)
		if err != nil {
			t.Fatalf("list claims: %v", err)
		}
		assertNoDoubleBinding(t, byInstance)
		t.Logf("claim phases: %v (%d distinct instances bound)", phases, len(byInstance))
		time.Sleep(5 * time.Second)
	}
}

// S2: same storm, one owner. The quota is the only thing standing between a
// single team and the whole pool, and it is enforced on a concurrent path.
func TestStressQuotaUnderConcurrency(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	quota := stressInt("E2E_STRESS_QUOTA", 3)
	claimants := stressInt("E2E_STRESS_CLAIMANTS", 40)

	tmpl := newPoolTemplate("s-quota", 15, 0)
	tmpl.Spec.MaxInstancesPerUser = &quota
	createTemplate(ctx, t, tmpl)
	waitPoolAvailable(ctx, t, tmpl.Name, 15, poolBudget(15))
	t.Logf("quota=%d; firing %d simultaneous claims from ONE owner", quota, claimants)

	_, errs := fireClaims(ctx, t, tmpl.Name, "s-quota-c", claimants, func(int) string { return "greedy" })
	for _, e := range errs {
		t.Errorf("claim create failed: %v", e)
	}

	eventually(t, poolBudget(claimants)+2*time.Minute, "the quota storm to settle", func() error {
		_, phases, err := bindings(ctx)
		if err != nil {
			return err
		}
		if n := phases[dployv1alpha1.ClaimPending]; n > 0 {
			return fmt.Errorf("%d still Pending (phases: %v)", n, phases)
		}
		return nil
	})

	for range 6 {
		byInstance, phases, err := bindings(ctx)
		if err != nil {
			t.Fatalf("list claims: %v", err)
		}
		assertNoDoubleBinding(t, byInstance)
		if got := phases[dployv1alpha1.ClaimBound]; got > quota {
			t.Errorf("QUOTA BREACH: %d bound claims for one owner, quota is %d (phases: %v)", got, quota, phases)
		}
		t.Logf("claim phases: %v", phases)
		time.Sleep(5 * time.Second)
	}
}

// S3: pool.size flapped faster than the pool can converge. The fill loop and
// the purge must not fight each other into oscillation or leak instances.
func TestStressResizeFlapping(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	hi, lo := stressInt("E2E_STRESS_HI", 12), 2
	rounds := stressInt("E2E_STRESS_ROUNDS", 6)

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-flap", lo, 0))
	waitPoolAvailable(ctx, t, tmpl.Name, lo, poolBudget(lo))

	for r := range rounds {
		for _, size := range []int{hi, lo} {
			setPoolSize(ctx, t, tmpl.Name, size)
			time.Sleep(4 * time.Second) // deliberately shorter than convergence
		}
		t.Logf("round %d/%d done", r+1, rounds)
	}

	// After the churn stops it must land exactly on the last size and stay.
	setPoolSize(ctx, t, tmpl.Name, lo)
	waitPoolAvailable(ctx, t, tmpl.Name, lo, poolBudget(hi)+3*time.Minute)
	for range 8 {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(live) != lo {
			t.Errorf("did not settle at %d after flapping: %d live (phases: %v)", lo, len(live), countPhases(live))
		}
		time.Sleep(5 * time.Second)
	}
	eventually(t, deleteTimeout, "every surplus object to finish tearing down", func() error {
		all, err := instancesFor(ctx, tmpl.Name)
		if err != nil {
			return err
		}
		if len(all) != lo {
			return fmt.Errorf("%d instance objects remain (phases: %v)", len(all), countPhases(all))
		}
		return nil
	})
}

// S4: the operator dies while it is provisioning. On a single-node control
// plane this is the realistic outage, and what matters is that it converges
// afterwards without stranding or duplicating anything.
func TestStressOperatorRestartMidFill(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	size := stressInt("E2E_STRESS_RESTART_POOL", 25)

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-restart", size, 0))

	// Kill it partway through the fill.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err == nil && len(live) >= size/3 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	killed := killOperator(ctx, t)
	t.Logf("killed operator pod(s): %v", killed)

	waitPoolAvailable(ctx, t, tmpl.Name, size, poolBudget(size)+4*time.Minute)

	for range 6 {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(live) != size {
			t.Errorf("OVER/UNDER-FILL after restart: %d live, want %d (phases: %v)", len(live), size, countPhases(live))
		}
		time.Sleep(5 * time.Second)
	}
}

// killOperator deletes the operator pods and waits for a Ready replacement.
func killOperator(ctx context.Context, t *testing.T) []string {
	t.Helper()
	var pods corev1.PodList
	if err := k8s.List(ctx, &pods, client.InNamespace("dploy-system")); err != nil {
		t.Fatalf("list operator pods: %v", err)
	}
	var killed []string
	for i := range pods.Items {
		p := &pods.Items[i]
		if !strings.Contains(p.Name, "operator") {
			continue
		}
		killed = append(killed, p.Name)
		_ = k8s.Delete(ctx, p)
	}
	if len(killed) == 0 {
		t.Fatal("no operator pod found in dploy-system")
	}
	eventually(t, 3*time.Minute, "a fresh operator pod to become Ready", func() error {
		var fresh corev1.PodList
		if err := k8s.List(ctx, &fresh, client.InNamespace("dploy-system")); err != nil {
			return err
		}
		for i := range fresh.Items {
			p := &fresh.Items[i]
			if !strings.Contains(p.Name, "operator") || !p.DeletionTimestamp.IsZero() {
				continue
			}
			for _, c := range p.Status.ContainerStatuses {
				if c.Ready {
					return nil
				}
			}
		}
		return fmt.Errorf("no Ready operator pod yet")
	})
	return killed
}

// S5: a wave of TTLs expiring at once — the end of a CTF, essentially.
func TestStressTTLWave(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	n := stressInt("E2E_STRESS_TTL_N", 20)

	tmpl := newPoolTemplate("s-ttl", n, 0)
	tmpl.Spec.TTL = &dployv1alpha1.TTLSpec{Seconds: 90}
	createTemplate(ctx, t, tmpl)
	waitPoolAvailable(ctx, t, tmpl.Name, n, poolBudget(n))

	_, errs := fireClaims(ctx, t, tmpl.Name, "s-ttl-c", n, func(i int) string { return fmt.Sprintf("ttl%03d", i) })
	for _, e := range errs {
		t.Errorf("claim create failed: %v", e)
	}
	eventually(t, poolBudget(n)+2*time.Minute, "all claims bound", func() error {
		_, phases, err := bindings(ctx)
		if err != nil {
			return err
		}
		if got := phases[dployv1alpha1.ClaimBound]; got != n {
			return fmt.Errorf("bound=%d want %d (phases: %v)", got, n, phases)
		}
		return nil
	})
	t.Logf("%d claims bound with a 90s TTL; waiting for the wave", n)

	eventually(t, 10*time.Minute, "every expired claim to be reaped", func() error {
		_, phases, err := bindings(ctx)
		if err != nil {
			return err
		}
		if got := phases[dployv1alpha1.ClaimBound]; got != 0 {
			return fmt.Errorf("%d claims still Bound past TTL (phases: %v)", got, phases)
		}
		return nil
	})
}

// S6: does anything survive that should not? Namespaces are the expensive leak.
func TestStressOrphanAudit(t *testing.T) {
	requireStress(t)
	ctx := context.Background()

	var nss corev1.NamespaceList
	if err := k8s.List(ctx, &nss); err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	var managed []string
	for i := range nss.Items {
		ns := &nss.Items[i]
		if ns.Labels[dployv1alpha1.LabelManaged] != "" && ns.DeletionTimestamp.IsZero() {
			managed = append(managed, fmt.Sprintf("%s(template=%s)", ns.Name, ns.Labels[dployv1alpha1.LabelTemplate]))
		}
	}
	t.Logf("dploy-managed namespaces still present: %d %v", len(managed), managed)

	var insts dployv1alpha1.DployInstanceList
	if err := k8s.List(ctx, &insts, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list instances: %v", err)
	}
	t.Logf("instances left in %s: %d (phases: %v)", testNS, len(insts.Items), countPhases(insts.Items))
	for i := range insts.Items {
		in := &insts.Items[i]
		if in.Status.Phase == dployv1alpha1.PhaseFailed {
			t.Errorf("instance %s left in Failed", in.Name)
		}
	}
	_ = metav1.Now
}

// ---------------------------------------------------------------------------
// Batch 2: adversarial cases rather than volume.
// ---------------------------------------------------------------------------

// S7: maxSize is a hard cap on idle+claimed. Under a storm the fill loop and the
// on-demand fallback both create instances; nothing may push past the cap.
func TestStressMaxSizeUnderStorm(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	size, maxSize := 10, 15
	claimants := stressInt("E2E_STRESS_CLAIMANTS", 60)

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-cap", size, maxSize))
	waitPoolAvailable(ctx, t, tmpl.Name, size, poolBudget(size))
	t.Logf("size=%d maxSize=%d; firing %d claims", size, maxSize, claimants)

	_, errs := fireClaims(ctx, t, tmpl.Name, "s-cap-c", claimants, func(i int) string {
		return fmt.Sprintf("capteam%03d", i)
	})
	for _, e := range errs {
		t.Errorf("claim create failed: %v", e)
	}

	worst := 0
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(live) > worst {
			worst = len(live)
		}
		if len(live) > maxSize {
			t.Errorf("MAXSIZE BREACH: %d live instances, cap is %d (phases: %v)",
				len(live), maxSize, countPhases(live))
		}
		time.Sleep(3 * time.Second)
	}
	_, phases, err := bindings(ctx)
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}
	t.Logf("peak live instances: %d (cap %d); claim phases: %v", worst, maxSize, phases)
}

// S8: the template is deleted while claims are still landing on it. Nothing may
// be stranded: no instance, no namespace.
func TestStressTemplateDeletedMidStorm(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	claimants := stressInt("E2E_STRESS_CLAIMANTS", 40)

	tmpl := newPoolTemplate("s-yank", 12, 0)
	if err := k8s.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	waitPoolAvailable(ctx, t, tmpl.Name, 12, poolBudget(12))

	_, errs := fireClaims(ctx, t, tmpl.Name, "s-yank-c", claimants, func(i int) string {
		return fmt.Sprintf("yank%03d", i)
	})
	for _, e := range errs {
		t.Errorf("claim create failed: %v", e)
	}
	// Yank it out from under them, mid-flight.
	time.Sleep(3 * time.Second)
	if err := k8s.Delete(ctx, &dployv1alpha1.DployTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: tmpl.Name, Namespace: testNS},
	}); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	t.Log("template deleted mid-storm")

	eventually(t, 10*time.Minute, "every instance of the yanked template to go", func() error {
		all, err := instancesFor(ctx, tmpl.Name)
		if err != nil {
			return err
		}
		if len(all) != 0 {
			return fmt.Errorf("%d instances remain (phases: %v)", len(all), countPhases(all))
		}
		return nil
	})
	eventually(t, 5*time.Minute, "every workload namespace to go", func() error {
		var nss corev1.NamespaceList
		if err := k8s.List(ctx, &nss); err != nil {
			return err
		}
		var left []string
		for i := range nss.Items {
			ns := &nss.Items[i]
			if ns.Labels[dployv1alpha1.LabelTemplate] == tmpl.Name && ns.DeletionTimestamp.IsZero() {
				left = append(left, ns.Name)
			}
		}
		if len(left) > 0 {
			return fmt.Errorf("%d namespaces stranded: %v", len(left), left)
		}
		return nil
	})
}

// S9: a template whose chart cannot render. Instances must fail visibly and the
// operator must not spin forever trying to reach the target size.
func TestStressBrokenChartDoesNotWedge(t *testing.T) {
	requireStress(t)
	ctx := context.Background()

	tmpl := newPoolTemplate("s-broken", 3, 0)
	tmpl.Spec.Chart.Path = "charts/this-path-does-not-exist"
	createTemplate(ctx, t, tmpl)

	// Whatever happens, it must reach a steady state rather than creating
	// instances without bound.
	time.Sleep(3 * time.Minute)
	counts := map[int]int{}
	for range 10 {
		all, err := instancesFor(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		counts[len(all)]++
		t.Logf("instances=%d phases=%v", len(all), countPhases(all))
		time.Sleep(6 * time.Second)
	}
	most := 0
	for n := range counts {
		if n > most {
			most = n
		}
	}
	if most > 12 {
		t.Errorf("RUNAWAY: broken template produced %d instances for a pool of 3", most)
	}
}

// S10: claims created and destroyed as fast as the API allows. Recycling has to
// keep up without leaking instances or namespaces.
func TestStressClaimChurn(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	rounds := stressInt("E2E_STRESS_CHURN_ROUNDS", 8)
	batch := stressInt("E2E_STRESS_CHURN_BATCH", 12)

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-churn", 10, 0))
	waitPoolAvailable(ctx, t, tmpl.Name, 10, poolBudget(10))

	for r := range rounds {
		var wg sync.WaitGroup
		for i := range batch {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				name := fmt.Sprintf("s-churn-%d-%d", r, i)
				c := newClaim(name, tmpl.Name, fmt.Sprintf("churn%03d", i))
				if err := k8s.Create(ctx, c); err != nil {
					return
				}
				time.Sleep(6 * time.Second)
				_ = k8s.Delete(ctx, &dployv1alpha1.DployInstanceClaim{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
				})
			}(i)
		}
		wg.Wait()
		t.Logf("churn round %d/%d", r+1, rounds)
	}

	// It has to come back to exactly the warm pool, and stay there.
	waitPoolAvailable(ctx, t, tmpl.Name, 10, poolBudget(20)+4*time.Minute)
	for range 8 {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(live) != 10 {
			t.Errorf("pool did not return to 10 after churn: %d live (phases: %v)", len(live), countPhases(live))
		}
		time.Sleep(5 * time.Second)
	}
}

// S11: the operator dies mid-purge, the other half of the restart story.
func TestStressOperatorRestartMidPurge(t *testing.T) {
	requireStress(t)
	ctx := context.Background()
	hi, lo := 20, 3

	tmpl := createTemplate(ctx, t, newPoolTemplate("s-purgekill", hi, 0))
	waitPoolAvailable(ctx, t, tmpl.Name, hi, poolBudget(hi))

	setPoolSize(ctx, t, tmpl.Name, lo)
	time.Sleep(2 * time.Second) // let the purge start
	t.Logf("killed mid-purge: %v", killOperator(ctx, t))

	waitPoolAvailable(ctx, t, tmpl.Name, lo, poolBudget(hi)+4*time.Minute)
	for range 6 {
		live, err := livePoolInstances(ctx, tmpl.Name)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(live) != lo {
			t.Errorf("did not settle at %d after mid-purge restart: %d live (phases: %v)",
				lo, len(live), countPhases(live))
		}
		time.Sleep(5 * time.Second)
	}
}
