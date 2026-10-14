// 已弃用，请勿使用。
//
// 本文件原本用于为 xboard-api-map.json 批量注入 scope 字段。
// 弃用原因：
//   1. 本机没有 Node 运行时（tools 目录下的检查脚本统一改用 PowerShell + .NET）；
//   2. API 映射的范围口径已改为集中声明在 map 的 scopePolicy 字段中，
//      不再需要逐条注入 scope，避免维护两份真相。
//
// 若将来确实需要批量改写契约文件，请在本机安装 Node 后另行编写带测试的脚本，
// 或在 GitHub Actions 中执行，不要复活本占位文件。

throw new Error('此脚本已弃用：范围口径见 design/contracts/xboard-api-map.json 的 scopePolicy 字段。');
