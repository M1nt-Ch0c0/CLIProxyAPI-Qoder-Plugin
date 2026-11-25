# Qoder
把 Qoder 账号接到 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)。登录时打开 Qoder 官网做 OAuth 授权。<br/><br/>
插件同时支持国内版和国际版。

## 安装

在 CLIProxyAPI 管理面板的插件市场配置中添加该仓库的json<br/>
`https://raw.githubusercontent.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/refs/heads/main/registry.json` <br/>
刷新后找到 Qoder 并安装。

插件市场打不开的时候，可以从 [Releases](https://github.com/M1nt-Ch0c0/CLIProxyAPI-Qoder-Plugin/releases) 下载和当前系统匹配的包，把里面的插件文件放到 CPA 的 `plugins` 目录

## 配置
在插件管理页面进行插件配置，可以选择CN/INTL

## 使用
在凭证管理页面，选择Qoder的OAuth认证，完成认证即可。<br/>
注意，在进行OAuth前请确认CN/INTL选择正确。

## 许可

MIT，见 [LICENSE](LICENSE)。

