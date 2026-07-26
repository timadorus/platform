package e2eutil

import (
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

// PortForward represents one running `kubectl port-forward` process.
type PortForward struct {
	cmd *exec.Cmd
}

// StartPortForward starts `kubectl port-forward` from localPort to service:servicePort in
// Namespace, and blocks until healthPath on that local port responds, or timeout elapses.
func StartPortForward(service string, localPort, servicePort int, healthPath string, timeout time.Duration) (*PortForward, error) {
	cmd := exec.Command("kubectl", "port-forward",
		"--namespace", Namespace,
		fmt.Sprintf("svc/%s", service),
		fmt.Sprintf("%d:%d", localPort, servicePort),
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("e2eutil: start port-forward for %s: %w", service, err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, healthPath)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return &PortForward{cmd: cmd}, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("e2eutil: port-forward for %s never became reachable at %s within %s", service, url, timeout)
}

// Stop terminates the port-forward process. Safe to call on a nil *PortForward.
func (pf *PortForward) Stop() {
	if pf == nil || pf.cmd.Process == nil {
		return
	}
	_ = pf.cmd.Process.Kill()
	_ = pf.cmd.Wait()
}
