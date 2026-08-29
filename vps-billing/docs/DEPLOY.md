# 部署指南

前提:VPS上已经有一个在跑的 **Xray-core**(如果你不确定,执行 `xray version`;如果是 sing-box/v2ray,这套工具目前不适用,见 README 的"它是什么、不是什么")。

## 1. 给 Xray 打开管理 API

编辑你现有的 `config.json`(通常在 `/usr/local/etc/xray/config.json`),把 [`configs/xray.config.example.json`](../configs/xray.config.example.json) 里的 `api`、`stats`、`policy`、`routing.rules` 这几块**合并**进去——不要整个文件替换,你现有的 inbound/outbound 保留不动,只需要:

1. 给你现有那个代理用的 inbound 加一个 `tag`(比如 `"vless-in"`),并把它的 `settings.clients` 清空成 `[]`——用户以后交给本工具管理,不要再手工往里加。
2. 新增一个只监听 `127.0.0.1:10085` 的 `dokodemo-door` inbound,`tag` 设为 `api-in`。
3. 加 `api` / `stats` / `policy` 块。
4. 在 `routing.rules` 里加一条把 `api-in` 的流量交给 `api` 处理的规则。

改完之后校验配置再重载,不要盲目重启:

```bash
xray -test -config /usr/local/etc/xray/config.json
systemctl reload xray   # 或 restart,取决于你的 Xray 是否支持 reload
```

验证 API 已经通了:

```bash
xray api statsquery --server=127.0.0.1:10085 -pattern ""
# 能返回 JSON(哪怕是空的 {"stat":[]})就说明API通了;报错说明上面哪步没配对
```

## 2. 编译并安装 billingctl

```bash
cd vps-billing
go build -o billingctl ./cmd/billingctl
sudo install -o root -g root -m 755 billingctl /usr/local/bin/billingctl
```

建运行目录和专用用户(不需要 root 权限跑):

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin vps-billing
sudo mkdir -p /var/lib/vps-billing /etc/vps-billing
sudo chown vps-billing:vps-billing /var/lib/vps-billing
sudo cp configs/billing.env.example /etc/vps-billing/billing.env
sudo chmod 600 /etc/vps-billing/billing.env
```

按需编辑 `/etc/vps-billing/billing.env`(数据库路径、xray 二进制路径、协议等)。

## 3. 建套餐、建客户、开订阅

```bash
sudo -u vps-billing billingctl seed-plans   # 按截图里的三档建 轻量15元/标准25元/重度50元
sudo -u vps-billing billingctl plan list

sudo -u vps-billing billingctl customer add -name "老王" -contact "微信:xxx"
sudo -u vps-billing billingctl sub create -customer 1 -plan 2 -email laowang@yournode -tag vless-in
```

`sub create` 会打印出这个订阅的 `uuid` 和到期时间。把它拼成客户端能识别的订阅链接,例如 VLESS + Reality:

```
vless://<uuid>@<你的域名或IP>:443?security=reality&sni=<serverNames>&fp=chrome&pbk=<公钥>&sid=<shortId>&type=tcp#<备注名>
```

`<公钥>` 是你在第1步用 `xray x25519` 生成密钥对时留存的公钥(私钥填在 config.json 里,公钥给客户端)。这部分和套餐管理无关,是标准的 VLESS Reality 客户端配置,任意支持 Reality 的客户端(v2rayN、NekoBox、Shadowrocket 等)都能识别这个链接格式。

## 4. 让 sync 常驻

```bash
sudo cp configs/systemd/vps-billing-sync.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now vps-billing-sync.service
sudo systemctl status vps-billing-sync.service
journalctl -u vps-billing-sync.service -f
```

这个服务每 5 分钟做一次:拉每个在用订阅的流量增量累加进数据库、检查是否过期或超流量并停用、把 Xray 当前用户列表和数据库对齐(处理 Xray 重启后 API 加的用户丢失的问题)。

## 5. 日常运维

```bash
billingctl sub list -db /var/lib/vps-billing/billing.db      # 看所有人的用量/到期/在线设备数
billingctl sub renew -id 3                                    # 收到续费款后手动续期、清零用量
billingctl sub suspend -id 3 -reason "手动停用"                # 立即停用
billingctl sub resume -id 3                                    # 恢复(不改到期时间/用量)
```

## 设备数限制的真实能力

Xray 没有"超过N台设备自动踢掉多余那台"这种精确能力,`billingctl` 用 `xray api statsonlineiplist` 统计一个用户当前有几个不同来源IP连着,作为设备数的近似值,默认只在 `sub list` 里展示、不做任何动作。如果确认这个数字在你的 Xray 版本上靠谱,可以在 `/etc/vps-billing/billing.env` 里把 `BILLING_ENFORCE_DEVICE_LIMIT` 设成 `true`,超限会直接把整个订阅停用(不是只挑一台设备踢),重新激活要手动 `sub resume`。建议先观察几天数据再决定开不开。
