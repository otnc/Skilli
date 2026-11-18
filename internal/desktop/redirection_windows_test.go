package desktop

import (
	"os"
	"os/exec"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The guard cannot be turned off again, so the guarded check runs in a
// copy of the test binary.
const guardHelperEnv = "SKILLI_TEST_GUARD_HELPER"

func TestRedirectionGuardDetection(t *testing.T) {
	if os.Getenv(guardHelperEnv) == "1" {
		enforce := uint32(1)
		proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessMitigationPolicy")
		if r, _, err := proc.Call(processRedirectionTrustPolicy, uintptr(unsafe.Pointer(&enforce)), unsafe.Sizeof(enforce)); r == 0 {
			t.Skipf("this Windows cannot enforce RedirectionGuard: %v", err)
		}
		if !redirectionGuardEnforced() {
			t.Fatal("an enforced guard must be detected")
		}
		return
	}

	if redirectionGuardEnforced() {
		t.Fatal("the test process itself must not be guarded")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRedirectionGuardDetection$", "-test.v")
	cmd.Env = append(os.Environ(), guardHelperEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
}
