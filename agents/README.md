# Agents 部署模块

- `dify/`：`git@github.com:ymouzhang/dify.git` 子模块，跟踪 `feature/1.16.1`；
- `dify-deploy/`：面向 DP 的固定组合、amd64 离线打包和运行包装。

Dify 分支相对官方版本的差异、DP 离线部署约束和操作说明见 `dify-deploy/README.md`。不要把 DP 运行
脚本直接写入 `dify/`，否则子模块会变成 dirty 状态并增加后续跟踪远端分支的冲突。

