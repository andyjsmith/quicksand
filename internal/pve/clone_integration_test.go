package pve

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestIntegrationCloneLifecycle clones a template into a scratch VM ID,
// reconfigures it and destroys it again. It changes real Proxmox state, so it
// only runs when asked to:
//
//	QS_TEST_CONFIG=../../config.json QS_TEST_NODE=pve QS_TEST_TEMPLATE=100 QS_TEST_CLONE_VMID=9000 \
//	  QS_TEST_POOL=quicksand go test ./internal/pve -run CloneLifecycle -v
//
// Destroying a VM deletes ACL entries on its ID, so the token's rights
// should come from a pool (QS_TEST_POOL) rather than /vms/<id>.
func TestIntegrationCloneLifecycle(t *testing.T) {
	template, _ := strconv.Atoi(os.Getenv("QS_TEST_TEMPLATE"))
	target, _ := strconv.Atoi(os.Getenv("QS_TEST_CLONE_VMID"))
	if template == 0 || target == 0 {
		t.Skip("QS_TEST_TEMPLATE and QS_TEST_CLONE_VMID not set")
	}
	c, node, _ := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	upid, err := c.Clone(ctx, node, template, CloneOptions{NewID: target, Name: "quicksand-test", Pool: os.Getenv("QS_TEST_POOL")})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		upid, err := c.Destroy(ctx, node, target)
		if err == nil {
			err = c.WaitTask(ctx, node, upid, nil)
		}
		if err != nil {
			t.Errorf("destroy: %v", err)
		}
	})
	if err := c.WaitTask(ctx, node, upid, func(l string) { t.Log("clone:", l) }); err != nil {
		t.Fatalf("clone task: %v", err)
	}

	cfg, err := c.VMConfig(ctx, node, target)
	if err != nil {
		t.Fatal(err)
	}
	net0, _ := cfg["net0"].(string)
	bridge := os.Getenv("QS_TEST_BRIDGE")
	set := map[string]string{"cores": "2", "memory": "2048", "tags": "quicksand"}
	if bridge != "" {
		set["net0"] = SetNetBridge(net0, bridge, 0)
	}
	if err := c.UpdateConfig(ctx, node, target, set, nil); err != nil {
		t.Fatalf("update config: %v", err)
	}
	cfg, err = c.VMConfig(ctx, node, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cores=%v memory=%v net0=%v tags=%v", cfg["cores"], cfg["memory"], cfg["net0"], cfg["tags"])
	status, err := c.Status(ctx, node, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status %s", status.PowerState())
}
