# Qoder

这个插件把 Qoder 账号接到 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)。登录时打开 Qoder 官网做 OAuth 授权，国内账号用 [qoder.cn](https://qoder.cn)，国际账号用 [qoder.com](https://qoder.com)。浏览器里授权完成之后，CPA 就用这份登录态去调用该账号下的模型。插件 ID 是 `qoder`。每份凭证自己保存地区，配置里的 `login_region` 只影响之后新建的登录。

## 安装

在 CLIProxyAPI 管理面板的插件市场里找到 Qoder 并安装。装好以后打开这个插件的配置，保存下面这份 JSON，然后启用。`login_region` 为 `cn` 时新登录走国内站，为 `intl` 时走国际站。

```json
{
  "enabled": true,
  "login_region": "cn"
}
```

主机配置里还要打开插件总开关，市场装上的插件才会被加载。写在 `config.yaml` 里时，上面这些字段放在 `plugins.configs.qoder` 下面，`plugins.enabled` 设为 `true`。

到凭证页添加 Qoder 之后，面板会打开浏览器，按页面上的 OAuth 流程完成授权。授权结束，这张凭证会出现在列表里。之后用 CPA 的地址和密钥发请求，模型名用插件列出的 ID。

## 自己放文件

插件市场打不开的时候，可以从 [Releases](https://github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/releases) 下载和当前系统匹配的包，把里面的插件文件放到 CPA 的 `plugins` 目录，重启后再按上面的 JSON 填写配置。

## 许可

MIT，见 [LICENSE](LICENSE)。
