# Qoder

把 Qoder 账号接到 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)。登录用的是 Qoder 自己的 OAuth，不是 API Key。登完之后，用 CPA 的接口调用这个账号能用的模型。

国内账号走 [qoder.cn](https://qoder.cn)，国际账号走 [qoder.com](https://qoder.com)。一个插件 ID：`qoder`。每个凭证自己记地区，`login_region` 只决定接下来新登录去哪个站。

## 安装

用管理面板里的插件市场。

1. 打开 CLIProxyAPI 管理面板，进入插件市场，安装 **Qoder** 并启用。
2. 插件配置里把 `login_region` 设成 `cn` 或 `intl`。不写的话新登录按国内站。
3. 在凭证页添加 Qoder。页面会打开浏览器，按 Qoder 的 OAuth 授权。授权完成，凭证出现在列表里。

然后用 CPA 的地址和密钥请求。模型名用插件列出来的 ID。

```yaml
plugins:
  enabled: true
  configs:
    qoder:
      enabled: true
      login_region: cn   # 国际账号写 intl
```

`plugins.enabled` 要开着，市场装上去的插件才会加载。

## 自己放文件

市场不可用时，从 [Releases](https://github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/releases) 下载和机器系统匹配的包，把里面的插件文件放到 CPA 的 `plugins` 目录，重启。配置同上。

## 许可

MIT，见 [LICENSE](LICENSE)。
