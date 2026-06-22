// Package constants 存放跨包共享的命名常量，消除魔法值并避免前后端/多包漂移。
package constants

// LocalhostAddr 是 worker 与 manager 监听/回连的回环地址。
const LocalhostAddr = "127.0.0.1"

// LockFileName 是 root 进程独占锁文件的文件名，存放在 XDG_RUNTIME_DIR 或回退到 os.TempDir。
const LockFileName = "cap.lock"

// 以下 Proxy* 路由常量是 worker 管理 API 的路径前缀与端点，
// worker 包作为服务端匹配，manager 包作为客户端拼接，集中定义以避免漂移。
const (
	ProxyPathPrefix    = "/_proxy/"
	ProxyHealthPath    = "/_proxy/health"
	ProxyRuntimePath   = "/_proxy/runtime"
	ProxyStatusPath    = "/_proxy/status"
	ProxySwitchPath    = "/_proxy/switch"
	ProxyModulesPrefix = "/_proxy/modules/"
)
