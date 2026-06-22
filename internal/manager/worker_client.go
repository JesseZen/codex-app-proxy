package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/jesse/codex-app-proxy/internal/constants"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

type HTTPWorkerClient struct {
	Client *http.Client
}

func (c HTTPWorkerClient) ToggleModule(port int, moduleName string) error {
	url := fmt.Sprintf("http://%s:%d%s%s/toggle", constants.LocalhostAddr, port, constants.ProxyModulesPrefix, moduleName)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	return c.do(req)
}

func (c HTTPWorkerClient) PatchModule(port int, moduleName string, cfg config.ModuleConfig) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(cfg); err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:%d%s%s", constants.LocalhostAddr, port, constants.ProxyModulesPrefix, moduleName)
	req, err := http.NewRequest(http.MethodPatch, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c HTTPWorkerClient) SwitchUpstream(port int, runtime upstream.RuntimeUpstream) error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(struct {
		Upstream upstream.RuntimeUpstream `json:"upstream"`
	}{Upstream: runtime}); err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:%d%s", constants.LocalhostAddr, port, constants.ProxySwitchPath)
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c HTTPWorkerClient) ApplyRuntime(port int, workerRuntime appruntime.WorkerRuntime) (ApplyRuntimeStatus, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(workerRuntime); err != nil {
		return ApplyRuntimeStatus{}, err
	}
	url := fmt.Sprintf("http://%s:%d%s", constants.LocalhostAddr, port, constants.ProxyRuntimePath)
	req, err := http.NewRequest(http.MethodPut, url, &body)
	if err != nil {
		return ApplyRuntimeStatus{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ApplyRuntimeStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ApplyRuntimeStatus{}, fmt.Errorf("worker returned %s", resp.Status)
	}
	var status ApplyRuntimeStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ApplyRuntimeStatus{}, err
	}
	return status, nil
}

func (c HTTPWorkerClient) GetStatus(port int) (WorkerStatus, error) {
	url := fmt.Sprintf("http://%s:%d%s", constants.LocalhostAddr, port, constants.ProxyStatusPath)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return WorkerStatus{}, err
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return WorkerStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return WorkerStatus{}, fmt.Errorf("worker returned %s", resp.Status)
	}
	var status WorkerStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return WorkerStatus{}, err
	}
	return status, nil
}

func (c HTTPWorkerClient) do(req *http.Request) error {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("worker returned %s", resp.Status)
	}
	return nil
}
