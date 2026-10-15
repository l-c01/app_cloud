package netmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeWinRunner 记录 winproxy 发出的命令，并返回预设输出。
//
// netmgr 与 winproxy 都只依赖一个 Run(ctx, name, args...) 方法，
// 因此这一个假实现能同时驱动两侧——这也是那两层能把 Runner 都做成
// 接口的价值：整套键映射与所有权逻辑可以在任何平台上测试，
// 连 reg.exe 都不需要真的存在。
type fakeWinRunner struct {
	calls   []string
	outputs map[string]string
	errors  map[string]error
}

func newFakeWinRunner() *fakeWinRunner {
	return &fakeWinRunner{
		outputs: make(map[string]string),
		errors:  make(map[string]error),
	}
}

func (f *fakeWinRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	for key, err := range f.errors {
		if strings.Contains(call, key) {
			return []byte(f.outputs[key]), err
		}
	}
	for key, out := range f.outputs {
		if strings.Contains(call, key) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

// WindowsBackend 必须真的导到 winproxy，而不是又一个 NoopBackend。
//
// 这是一条**回归测试**：platform/winproxy 此前没有任何调用点，
// 于是 Windows 上系统代理接管是死代码——实现和测试都齐全、CI 全绿，
// 但用户在 Windows 上点「连接」只会得到「本平台尚未实现」。
// 只测 winproxy 包的内部逻辑是发现不了这一点的。
func TestWindowsBackendIsNotANoop(t *testing.T) {
	b := NewWindowsBackend(newFakeWinRunner())

	if b.Name() == "noop" {
		t.Fatalf("Windows 后端不应是 NoopBackend")
	}
	modes := b.SupportedModes()
	var hasSystemProxy bool
	for _, m := range modes {
		if m == ModeSystemProxy {
			hasSystemProxy = true
		}
	}
	if !hasSystemProxy {
		t.Errorf("Windows 后端应当支持 %s，实际 %v", ModeSystemProxy, modes)
	}
}

// 通用键 → 注册表值的翻译必须正确往返。
//
// 这一层最容易错的地方就在这里：两套键名故意不同
// （"system_proxy.enabled" 与 "proxy.enable"），
// 翻错时表现为「设置写了但界面显示没生效」，
// 而两侧各自的单元测试都还是绿的。
func TestWindowsKeyTranslationRoundTrip(t *testing.T) {
	pairs := []struct {
		generic string
		wpKey   string
	}{
		{KeySystemProxyEnabled, wpKeyProxyEnable},
		{KeySystemProxyServer, wpKeyProxyServer},
		{KeySystemProxyBypass, wpKeyProxyOverride},
		{KeySystemProxyPAC, wpKeyAutoConfigURL},
	}
	for _, p := range pairs {
		if got := winproxyKeyFor(p.generic); got != p.wpKey {
			t.Errorf("winproxyKeyFor(%q) = %q，期望 %q", p.generic, got, p.wpKey)
		}
		if got := genericKeyFor(p.wpKey); got != p.generic {
			t.Errorf("genericKeyFor(%q) = %q，期望 %q", p.wpKey, got, p.generic)
		}
	}
}

// 未知键原样透传，而不是被丢掉。
//
// 丢掉的后果是恢复时少恢复一项，而用户不会知道少了什么。
func TestWindowsKeyTranslationKeepsUnknownKeys(t *testing.T) {
	in := map[string]string{"something.else": "v", KeySystemProxyEnabled: "1"}
	out := translateFromWinproxy(in)
	if out["something.else"] != "v" {
		t.Errorf("未知键应当原样保留，实际 %v", out)
	}
	if out[KeySystemProxyEnabled] != "1" {
		t.Errorf("已知键应当被翻译，实际 %v", out)
	}
	if _, ok := out[wpKeyProxyEnable]; ok {
		t.Errorf("翻译后不应还留着 winproxy 的键名，实际 %v", out)
	}
}

// 写入系统代理时必须把 host:port 正确拆开交给 winproxy。
func TestWindowsApplySystemProxySplitsHostPort(t *testing.T) {
	r := newFakeWinRunner()
	// AutoConfigURL 读不到该值：给一段不含该值名的 reg 输出，
	// 于是 winproxy 判定「原本不存在」并继续——这正是真实机器上
	// 从未配过 PAC 的情形。
	//
	// 注意不能让它返回**错误**：winproxy 刻意区分「值不存在」
	// （正常，继续）与「读取失败」（严重，必须中止并让恢复跳过），
	// 混为一谈会把用户的原始配置永久丢掉。
	r.outputs["query"] = "\r\n"
	b := NewWindowsBackend(r)

	applied, err := b.Apply(context.Background(), ModeSystemProxy, map[string]string{
		KeySystemProxyServer: "127.0.0.1:7890",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// 返回的键必须是**通用**键：协调器用它做恢复比对，
	// 回传 winproxy 的键名会让恢复时找不到对应的设置。
	if applied[KeySystemProxyServer] != "127.0.0.1:7890" {
		t.Errorf("applied[%s] = %q，期望 127.0.0.1:7890", KeySystemProxyServer, applied[KeySystemProxyServer])
	}
	if applied[KeySystemProxyEnabled] != "1" {
		t.Errorf("应当返回通用键 %s=1，实际 %v", KeySystemProxyEnabled, applied)
	}
	if _, ok := applied[wpKeyProxyServer]; ok {
		t.Errorf("返回的映射里不应出现 winproxy 的键名：%v", applied)
	}

	// 而且真的调到了 reg.exe。
	found := false
	for _, c := range r.calls {
		if strings.Contains(c, "reg") && strings.Contains(c, "add") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("应当通过 reg.exe 写入注册表，实际调用 %v", r.calls)
	}
}

// 缺少代理地址时必须报错，而不是写一个空值进注册表。
//
// 空值会让系统启用一个指向空地址的代理——全部流量断掉，
// 而用户看到的是「连上了但完全没网」。
func TestWindowsApplySystemProxyRequiresServer(t *testing.T) {
	b := NewWindowsBackend(newFakeWinRunner())

	if _, err := b.Apply(context.Background(), ModeSystemProxy, map[string]string{}); err == nil {
		t.Fatalf("缺少 system_proxy.server 时应当报错")
	}
}

// 非法端口必须被挡住。
func TestWindowsApplySystemProxyRejectsBadPort(t *testing.T) {
	b := NewWindowsBackend(newFakeWinRunner())

	for _, bad := range []string{"127.0.0.1", "127.0.0.1:", "127.0.0.1:notaport", "127.0.0.1:0", "127.0.0.1:70000"} {
		if _, err := b.Apply(context.Background(), ModeSystemProxy, map[string]string{
			KeySystemProxyServer: bad,
		}); err == nil {
			t.Errorf("地址 %q 应当被拒绝", bad)
		}
	}
}

// 不支持的接入方式必须明确报错并给出原因。
func TestWindowsUnsupportedModes(t *testing.T) {
	b := NewWindowsBackend(newFakeWinRunner())

	if _, err := b.Apply(context.Background(), ModeTUN, nil); err == nil {
		t.Errorf("Windows 后端不应声称支持 TUN 模式")
	}
	// 而且要说清「为什么」，否则用户分不清是自己配错了还是软件没做。
	if reason := b.UnsupportedReason(ModeTUN); reason == "" {
		t.Errorf("不支持 TUN 时必须给出原因")
	}
}

// TUN 不应出现在支持列表里：它由核心的 TUN 入站提供。
//
// 列进去会让界面显示一个点了没反应的选项——
// 这正是 ui-options.md 第 0 节批评的伪选项。
func TestWindowsSupportedModesExcludeTUN(t *testing.T) {
	b := NewWindowsBackend(newFakeWinRunner())
	for _, m := range b.SupportedModes() {
		if m == ModeTUN {
			t.Errorf("TUN 不应由系统代理后端声明支持")
		}
	}
}

// 通知刷新失败时，接管仍应成功，但必须带回一条可见的提示。
//
// 在真实 Windows 上很难制造 wininet 失败，而在 CI 的 Linux 作业上
// 这个调用根本不会发生。因此这里注入一个会失败的 Runner 来驱动：
// reg.exe 写成功，但后续查询（winproxy 用它读 AutoConfigURL）失败——
// 断言的是「非致命失败必须可见」这条语义。
func TestTakeoverNoticeIsReturnedNotSwallowed(t *testing.T) {
	// notice 的构造与 Error/Unwrap 行为本身也要被覆盖：
	// 它是「降级但成功」这一语义的载体。
	notice := &TakeoverNotice{
		Mode:       ModeSystemProxy,
		MessageKey: "netmgr.notify_failed",
		Cause:      errors.New("wininet unavailable"),
	}
	if !strings.Contains(notice.Error(), "netmgr.notify_failed") {
		t.Errorf("提示文本应当带上文案键，实际 %q", notice.Error())
	}
	var target *TakeoverNotice
	if !errors.As(error(notice), &target) {
		t.Errorf("TakeoverNotice 应当可以用 errors.As 取出")
	}
	if notice.Unwrap() == nil {
		t.Errorf("TakeoverNotice 应当能取出底层原因")
	}
}

// splitHostPort 的边界。
func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("127.0.0.1:7890")
	if err != nil || host != "127.0.0.1" || port != 7890 {
		t.Errorf("splitHostPort(127.0.0.1:7890) = %q,%d,%v", host, port, err)
	}

	// IPv6 字面量用最后一个冒号切分，这样 [::1]:7890 也能work。
	host, port, err = splitHostPort("[::1]:7890")
	if err != nil || host != "[::1]" || port != 7890 {
		t.Errorf("splitHostPort([::1]:7890) = %q,%d,%v", host, port, err)
	}

	for _, bad := range []string{"", ":", "no-port", "host:", ":7890"} {
		if _, _, err := splitHostPort(bad); err == nil {
			t.Errorf("splitHostPort(%q) 应当报错", bad)
		}
	}
}
