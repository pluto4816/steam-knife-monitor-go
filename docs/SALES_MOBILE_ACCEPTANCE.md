# Android 夸克与局域网 HTTPS 验收

本阶段使用同一 Wi-Fi 下的独立测试账本。移动尺寸自动化检查不等于夸克真机通过；请记录 Android 版本、夸克版本和最后的实际结果。无需打开购买或 QQ。

## 启动 HTTPS 测试

在仓库运行（把 IP 换成电脑的实际局域网 IPv4）：

```bash
bash scripts/sales-lan-test.sh 192.168.1.112
```

脚本生成测试专用 CA 和带 IP SAN 的服务证书，构建同一个 Go 程序，提示输入至少 16 字符的测试密码，然后以前台方式监听 HTTPS 8443。Ctrl+C 正常关闭服务。也可加 `--prepare-only` 只准备证书。

数据、可执行文件和私钥保存在已忽略的 `.sales-lan-test/`，数据库是该目录下的 `sales-test.db`。脚本不安装系统信任、不改防火墙、不访问正式账本。不把整个目录发送给手机：只传 `ca.crt` 公共证书。CA 有效 30 天，服务证书有效 7 天；运行脚本会重签服务证书。IP 改变后需停服并以新 IP 重新运行。CA 到期时先从手机删除旧测试 CA，再另行建立新的测试 CA，不把旧 CA 当成有效证书继续使用。

## Android 安装测试 CA

1. 用 USB 等方式把 `.sales-lan-test/ca.crt` 复制到手机。
2. 在系统设置中搜索“安装证书”，选择 **CA 证书**，安装该文件。不同 Android 厂商的菜单可能不同，不是选择“Wi-Fi 证书”。
3. 将电脑输出的 SHA-256 指纹与传输的证书核对。不要传 `ca.key` 或 `server.key`。
4. 在夸克地址栏输入完整地址，如 `https://192.168.1.112:8443`，确认页面没有证书警告再登录。

用户 CA 是系统信任操作；测试完成后在系统的用户凭据中移除“Steam Sales LAN Test CA”。Android 安装入口参考 [Google 官方说明](https://support.google.com/pixelphone/answer/2844832?hl=en)。这里不预先保证每个夸克版本都采用系统用户 CA：若仍报错，应记录错误并检查时间、IP、证书信任；不要把“忽略警告继续访问”当成 HTTPS 验收通过。

## 当前电脑运行在 WSL 2 时

手机连接 Windows 的局域网 IP，而不是 `localhost` 或 WSL 虚拟网卡 IP。默认 WSL NAT 通常还需要端口转发；参考 [Microsoft 官方说明](https://learn.microsoft.com/zh-cn/windows/wsl/networking)。以下修改需在 **Windows 管理员 PowerShell** 执行，示例 IP 必须以当前电脑和 WSL 实际值为准。

先检查现有配置，避免覆盖同一端口的其他服务：

```powershell
netsh interface portproxy show v4tov4
Get-NetTCPConnection -State Listen -LocalPort 8443 -ErrorAction SilentlyContinue
```

确认该局域网 IP 的 8443 未被使用后，为本测试添加专用转发和仅限专用网络、本地子网的防火墙规则：

```powershell
netsh interface portproxy add v4tov4 listenaddress=192.168.1.112 listenport=8443 connectaddress=172.31.79.86 connectport=8443
New-NetFirewallRule -Name SteamSalesLanTest8443 -DisplayName 'Steam Sales LAN test HTTPS' -Direction Inbound -Action Allow -Protocol TCP -LocalAddress 192.168.1.112 -LocalPort 8443 -RemoteAddress LocalSubnet -Profile Private
```

这些 IP 是本次准备时观测值，不是固定配置。WSL 重启后地址可能改变，需用 `hostname -I` 重新核对；不要转发到 VPN 虚拟网卡。电脑网络应当是可信的家庭专用网络，不扩大到所有公网来源。测试结束撤销本次规则：

```powershell
netsh interface portproxy delete v4tov4 listenaddress=192.168.1.112 listenport=8443
Remove-NetFirewallRule -Name SteamSalesLanTest8443
```

若超时，检查同一局域网、访客 Wi-Fi/AP 隔离、代理是否绕过局域网、服务是否仍运行、转发目标是否正确。证书错误和连接超时是不同问题。不要通过关闭整个防火墙或放开明文登录排查。

## 夸克真机清单

- [ ] 记录手机型号、Android 和夸克版本；完整 HTTPS 地址无证书警告。
- [ ] 登录后能切换四页，刷新仍登录；退出后受保护页面要求登录。
- [ ] 竖屏、横屏下导航、长名称、金额和日期控件无裁切；页面本身不横向溢出。
- [ ] 数量和金额输入可用，软键盘不妨碍滚动到保存按钮；日期选择可用。
- [ ] 输入两字搜索并选中物品，名称省略久经沙场；新建 10 件 × 0.65 元，默认目标 0.6500，保存前预览 6.50 元和目标售价 1.15 元。新增页不显示参考价输入框。
- [ ] 持仓显示 10 件、成本 6.50 元；搜索和编辑可用。在待售清单单独保存 1.15 元参考价，随后编辑买入信息不应清空此报价。
- [ ] 登记售出 3 件、实际总到账 3.00 元，剩余 7 件、成本 4.55 元，实际比例 0.6500。
- [ ] 历史表格可横向滚动至“纠错”，作废登记后恢复 10 件，并保留“已作废”。
- [ ] 浏览器返回、刷新或重复提交不导致重复记账；旧表单提示刷新。
- [ ] 重启服务后账本仍保留；明确重新核价能刷新报价记录时间。

若某项失败，记录页面、操作和错误文字即可，勿提供密码、会话 Cookie 或私钥。

## 本次开发验证

新增 HTTPS 回归验证可信 TLS 连接、Secure/HttpOnly/SameSite Cookie、HTTPS 表单、静态资源、退出以及配置边界。真实桌面 Chromium 已在移动模拟 360/390/430px 检查四页无页面级横向溢出，并走通买入、售出和纠错。移动浏览器自动化仅固定接受本次服务证书公钥；Go HTTPS 测试和实际服务的 CA 校验另行验证信任链。夸克的用户 CA 接受行为、Android 键盘和实际触控仍须完成以上真机清单。
