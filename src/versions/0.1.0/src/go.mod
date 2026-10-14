module proxyclient

go 1.24

// 本文件是共享业务模块的 Go module 定义。
//
// module 路径说明：
//   正式仓库地址尚未确定，这里使用不带域名的占位名 `proxyclient`。
//   待用户创建 GitHub 仓库后，应改为真实路径（例如 github.com/<owner>/<repo>/src），
//   并同步更新所有 import。改名前不应发布正式构建。
//
// Go 版本说明：
//   核心（Xray / sing-box）按 ADR-0003 以官方预构建二进制分发，不参与本 module 编译，
//   因此这里不受核心自身 Go 版本要求约束。
//   实际使用的 Go 版本以 deploy/targets/versions.lock.json 冻结值为准，并由 CI 校验。
