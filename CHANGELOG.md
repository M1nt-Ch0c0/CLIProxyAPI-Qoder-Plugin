# 更新记录

使用方法见 [README](README.md)。发行包在 [Releases](https://github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/releases)。

## 0.8.76

聊天请求改走客户端现在用的 completions 接口，模型名写在顶层 `model`。旧接口只改 `model_config.key` 时，服务会把请求按 `auto` 处理。

## 0.8.75

登录成功不再把同一份凭证登记成两行。

## 0.8.74

模型目录改到 `assistant` 场景，并改到 inference 主机上的 `/api/v2/model/list`。旧目录里的 key 会被上游改路由到 `auto`。

更早的记录在 git 历史里。
