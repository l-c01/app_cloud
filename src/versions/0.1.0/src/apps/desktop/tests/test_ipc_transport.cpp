// ipc_transport 与 ipc_client 中可离线测试的部分。
//
// 真正的连接行为需要活着的服务进程，无法在这里测试。
// 这里覆盖的是纯逻辑与安全判定——尤其是环回地址检查，
// 它是本模块唯一与安全直接相关的判定。
#include "core/ipc_client.hpp"
#include "core/ipc_transport.hpp"
#include "test_framework.hpp"

#include <set>
#include <string>
#include <chrono>
#include <cstdint>

using pcx::ipc::DescribeAddress;
using pcx::ipc::IpcClient;
using pcx::ipc::IsLoopbackAddress;

// ---------------------------------------------------------------------------
// 环回地址判定
// ---------------------------------------------------------------------------

PCX_TEST(LoopbackAddressesAreAccepted) {
    PCX_CHECK(IsLoopbackAddress("127.0.0.1:8080"));
    PCX_CHECK(IsLoopbackAddress("localhost:1"));
    PCX_CHECK(IsLoopbackAddress("127.0.0.1"));
    // 127.0.0.0/8 整个网段都是环回。
    PCX_CHECK(IsLoopbackAddress("127.5.6.7:9000"));
    // IPv6 环回，带方括号与不带都要认。
    PCX_CHECK(IsLoopbackAddress("[::1]:8080"));
    PCX_CHECK(IsLoopbackAddress("::1"));
    PCX_CHECK(IsLoopbackAddress("::ffff:127.0.0.1"));
}

PCX_TEST(NonLoopbackAddressesAreRejected) {
    // 这些一旦被当成管理接口地址，同网络的任何人都能控制本进程，
    // 而「把 127.0.0.1 写成 0.0.0.0」在实践中非常常见。
    PCX_CHECK_FALSE(IsLoopbackAddress("0.0.0.0:8080"));
    PCX_CHECK_FALSE(IsLoopbackAddress("192.168.1.10:8080"));
    PCX_CHECK_FALSE(IsLoopbackAddress("10.0.0.1:8080"));
    PCX_CHECK_FALSE(IsLoopbackAddress("example.com:8080"));
    PCX_CHECK_FALSE(IsLoopbackAddress("[::]:8080"));
    PCX_CHECK_FALSE(IsLoopbackAddress(""));
    // 前缀相似但不是环回的值：1270.x 不是 127.x。
    PCX_CHECK_FALSE(IsLoopbackAddress("1270.0.0.1:80"));
}

PCX_TEST(EmptyHostIsTreatedAsUnsafe) {
    // 空主机名在某些平台上会被解释为「任意地址」（等价于 0.0.0.0）。
    // 不确定的输入一律按不安全处理。
    PCX_CHECK_FALSE(IsLoopbackAddress(":8080"));
}

// ---------------------------------------------------------------------------
// 地址描述
// ---------------------------------------------------------------------------

PCX_TEST(DescribeAddressOmitsLocalPaths) {
    // 套接字路径与管道名可能含用户名等本地信息，日志里不该写出来。
    const std::string unix_desc = DescribeAddress("unix", "/run/user/1000/proxyclient.sock");
    PCX_CHECK_EQ(unix_desc.find("1000"), std::string::npos);
    PCX_CHECK_EQ(unix_desc.find("proxyclient.sock"), std::string::npos);
    PCX_CHECK(unix_desc.find("unix") != std::string::npos);

    const std::string pipe_desc = DescribeAddress("npipe", "\\\\.\\pipe\\proxyclient-abc");
    PCX_CHECK_EQ(pipe_desc.find("proxyclient-abc"), std::string::npos);

    // tcp 的地址本来就会出现在配置里，保留它便于排障。
    PCX_CHECK_EQ(DescribeAddress("tcp", "127.0.0.1:7899"), std::string("tcp 127.0.0.1:7899"));
}

// ---------------------------------------------------------------------------
// 状态文件位置
// ---------------------------------------------------------------------------

PCX_TEST(DefaultStatePathIsUnderTheAppDirectory) {
    const std::string path = IpcClient::DefaultStatePath();
    // 环境变量缺失时可能为空（例如容器里没有 HOME）。
    // 这里只要求：非空时必须指向本应用的目录。
    if (!path.empty()) {
        PCX_CHECK(path.find("proxyclient") != std::string::npos);
        PCX_CHECK(path.find("service.json") != std::string::npos);
    }
}

// ---------------------------------------------------------------------------
// 未连接时的行为
// ---------------------------------------------------------------------------

PCX_TEST(CallWithoutAConnectionFailsImmediately) {
    // 未连接时不能阻塞等待，必须立刻给出可读的原因——
    // 否则界面会卡住，而用户以为程序死了。
    IpcClient client;
    PCX_CHECK_FALSE(client.IsConnected());

    std::string error;
    const auto response = client.Call("GetCapabilities", std::nullopt,
                                      std::chrono::milliseconds(50), &error);
    PCX_CHECK_FALSE(response.has_value());
    PCX_CHECK_FALSE(error.empty());
}

PCX_TEST(PollEventWithoutAConnectionReturnsNothing) {
    IpcClient client;
    pcx::ipc::Event event;
    PCX_CHECK_FALSE(client.PollEvent(&event));
    PCX_CHECK_EQ(client.DroppedLocally(), std::uint64_t(0));
    PCX_CHECK_EQ(client.DroppedByServer(), std::uint64_t(0));
    PCX_CHECK_EQ(client.LastEventSeq(), std::uint64_t(0));
}

PCX_TEST(DisconnectIsIdempotent) {
    // 析构、退出、错误路径都会调用它，必须能重复调用。
    IpcClient client;
    client.Disconnect();
    client.Disconnect();
    PCX_CHECK_FALSE(client.IsConnected());
}

PCX_TEST(ConnectRejectsAnIncompatibleHandle) {
    // 版本不兼容时**不连接**：连上去也只会收到语焉不详的握手失败。
    IpcClient client;
    pcx::ipc::Handle handle;
    handle.network = "unix";
    handle.address = "/tmp/does-not-matter.sock";
    handle.token = "token";
    handle.protocol_version = pcx::ipc::kProtocolVersion + 1;
    handle.schema_version = pcx::ipc::kSchemaVersion;

    std::string error;
    PCX_CHECK_FALSE(client.Connect(handle, &error));
    PCX_CHECK(error.find("protocol") != std::string::npos);
    PCX_CHECK_FALSE(client.IsConnected());
}

PCX_TEST(ConnectRejectsAnIncompleteHandle) {
    IpcClient client;
    pcx::ipc::Handle handle;   // 全部字段为空
    std::string error;
    PCX_CHECK_FALSE(client.Connect(handle, &error));
    PCX_CHECK_FALSE(error.empty());
}

PCX_TEST(ConnectToAnUnsupportedNetworkFailsClearly) {
    IpcClient client;
    pcx::ipc::Handle handle;
    handle.network = "carrier-pigeon";
    handle.address = "somewhere";
    handle.token = "token";
    handle.protocol_version = pcx::ipc::kProtocolVersion;
    handle.schema_version = pcx::ipc::kSchemaVersion;

    std::string error;
    PCX_CHECK_FALSE(client.Connect(handle, &error));
    PCX_CHECK(error.find("carrier-pigeon") != std::string::npos);
}

// ---------------------------------------------------------------------------
// Transport 工厂的错误路径
// ---------------------------------------------------------------------------

PCX_TEST(ConnectRejectsANonLoopbackTcpAddress) {
    // 这是安全判定：管理接口只允许环回。
    std::string error;
    const auto transport = pcx::ipc::Connect("tcp", "0.0.0.0:7899", &error);
    PCX_CHECK_FALSE(transport != nullptr);
#if defined(_WIN32)
    // Windows 上 tcp 传输整体未实现，拒绝原因不同（且更早）。
    PCX_CHECK_FALSE(error.empty());
#else
    PCX_CHECK(error.find("loopback") != std::string::npos);
#endif
}

PCX_TEST(ConnectRejectsAnUnknownNetwork) {
    std::string error;
    const auto transport = pcx::ipc::Connect("weird", "x", &error);
    PCX_CHECK_FALSE(transport != nullptr);
    PCX_CHECK_FALSE(error.empty());
}

PCX_TEST(ConnectToAMissingSocketFailsWithAReason) {
    // 服务没启动时这是最常见的情况，错误信息必须能让人看懂。
    std::string error;
    const auto transport = pcx::ipc::Connect("unix", "/tmp/pcx-does-not-exist-12345.sock", &error);
    PCX_CHECK_FALSE(transport != nullptr);
    PCX_CHECK_FALSE(error.empty());
}
