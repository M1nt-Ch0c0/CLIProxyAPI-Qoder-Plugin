# 更新记录

使用方法见 [README](README.md)。发行包在 [Releases](https://github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/releases)。

## 0.8.84

拉模型名单改用桌面端现在的版本，Sonus、Cantus、Fast 会重新出现。聊天按名单填写显示名、是否推理、是否看图。签到、领权益和积分面板不再提供。

## 0.8.83

聊天改为桌面版的签名请求。回复里的模型名取目录显示名并转成小写，例如 `GLM-5.4` 写成 `glm-5.4`。发给上游的仍是目录键。插件设置说明改为中文。

## 0.8.82

模型目录加回 `?Encode=1`。桌面版 `models.list` 每次都会带这个参数，请求日志只打印路径，所以 0.8.81 把它漏掉了。聊天地址不变。

## 0.8.81

聊天和模型目录的地址去掉桌面版请求里没有的参数：`Encode=1`、`FetchKeys`、`AgentId`。模型名仍写在请求体的 `model_config.key`，不再额外加 `x-model-key`。

## 0.8.80

模型目录改回桌面版正在用的 `GET /algo/api/v2/model/list`。0.8.74 去掉 `/algo` 之后这个地址是空 404，目录会悄悄退回写死的表。聊天仍是 `POST /algo/api/v2/service/pro/sse/agent_chat_generation`。

## 0.8.79

聊天改回 `agent_chat_generation`。`/qsvr/chat/completions` 在登录之后返回空的 404，那个地址不是聊天接口。国际版仍走 `api3.qoder.sh`，国内版仍走 `gateway.qoder.com.cn`。

## 0.8.78

国际版聊天改回 `api3.qoder.sh` 上的 `/qsvr/chat/completions`。`0.8.76` 用的 `api2-v2.qoder.sh` 在公网不存在，请求会一直等到超时。国内版地址不变。

## 0.8.77

插件实现收到 `internal/qoder`，根目录只留 C 接口。登录、对话、签到的行为与 0.8.76 相同。

## 0.8.76

聊天请求改走客户端现在用的 completions 接口，模型名写在顶层 `model`。旧接口只改 `model_config.key` 时，服务会把请求按 `auto` 处理。

## 0.8.75

登录成功不再把同一份凭证登记成两行。

## 0.8.74

模型目录改到 `assistant` 场景，并改到 inference 主机上的 `/api/v2/model/list`。旧目录里的 key 会被上游改路由到 `auto`。

更早的记录在 git 历史里。
