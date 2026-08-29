# 部署指南

前提:VPS上已经有一个在跑的 **Xray-core**(如果你不确定,执行 `xray version`;如果是 sing-box/v2ray,这套工具目前不适用,见 README 的"它是什么、不是什么")。

这些命令都要在你的VPS上执行——Claude 这边没有你VPS的登录权限,没法替你跑。

## 0. 拿到代码、编译、装成系统服务

```bash
git clone https://github.com/GITGIT816/qianshan-memos.git
cd qianshan-memos/vps-billing
sudo ./scripts/install.sh
```

这一步脚本会自动:编译 `billingctl`、装到 `/usr/local/bin`、建专用系统用户 `vps-billing`、建 `/var/lib/vps-billing`(数据库目录)和 `/etc/vps-billing/billing.env`(配置)、装好 systemd 服务文件(先不启动)。跑完会打印接下来要做的事,和下面第1~4步是对应的。

## 1. 给 Xray 打开管理 API

不用手改 JSON,用工具自动合并:

```bash
billingctl xray-merge-config -in /usr/local/etc/xray/config.json
```

会在旁边生成一份 `config.json.merged.json`,自动加好 `api`/`stats`/`policy`/`routing` 这几块,**不会动你现有的 inbound/outbound**。跑完看它打印出来的:

- "inbounds found" 列表——确认你现有代理那个 inbound 有 tag(没有的话工具会在 "⚠ needs your attention" 里提醒你,需要你自己去 merged 文件里给它加一个,比如 `"vless-in"`,这个 tag 名字之后要传给 `sub create -tag`)。
- 如果提示 "⚠ heads up: inbound xxx already has N client(s)"——说明你现在这个 inbound 底下已经手工加了用户,这些人以后不会被 billingctl 追踪流量/到期,建议清空 `settings.clients` 后用 `sub create` 帮他们重新开一份。

确认没问题后:

```bash
xray -test -config /usr/local/etc/xray/config.json.merged.json   # 先校验
sudo cp /usr/local/etc/xray/config.json.merged.json /usr/local/etc/xray/config.json
sudo systemctl reload xray   # 或 restart,取决于你的 Xray 是否支持 reload
```

验证 API 已经通了:

```bash
xray api statsquery --server=127.0.0.1:10085 -pattern ""
# 能返回 JSON(哪怕是空的 {"stat":[]})就说明API通了;报错说明上面哪步没配对
```

## 2. 编辑配置

```bash
sudo nano /etc/vps-billing/billing.env
```

至少确认/填好:`BILLING_DB`(默认已经指向 `/var/lib/vps-billing/billing.db`,一般不用改)、`XRAY_BIN`、`BILLING_HOST`(你的域名或IP)、以及用 Reality 的话 `BILLING_PUBLIC_KEY`/`BILLING_SNI`/`BILLING_SHORT_ID`(公钥是 `xray x25519` 生成密钥对时留存的那个,私钥填在 Xray config.json 里,不要填进这个 env 文件)。

之后为了方便,下面命令里为了不依赖这个 env 文件(`sudo -u` 不会自动加载它),统一显式传 `-db /var/lib/vps-billing/billing.db`;实际后台常驻的 systemd 服务会通过 `EnvironmentFile` 自动读到这些值。

## 3. 建套餐、建客户、开订阅

```bash
sudo -u vps-billing billingctl seed-plans -db /var/lib/vps-billing/billing.db   # 按截图里的三档建 轻量15元/标准25元/重度50元
sudo -u vps-billing billingctl plan list -db /var/lib/vps-billing/billing.db

sudo -u vps-billing billingctl customer add -db /var/lib/vps-billing/billing.db -name "老王" -contact "微信:xxx"
sudo -u vps-billing billingctl sub create -db /var/lib/vps-billing/billing.db \
  -customer 1 -plan 2 -email laowang@yournode -tag vless-in \
  -host 你的域名或IP -pbk <第1步生成的公钥> -sni www.example.com -sid ""
```

带上 `-host`(和视情况需要的 `-pbk`/`-sni`/`-sid`/`-fp`)之后,`sub create` 会直接打印出完整的 `vless://` 分享链接,并在终端画出对应的二维码——截图发给对方,或者对方直接拿手机相机/客户端扫终端里那个二维码即可导入,不用手工拼链接。

如果第2步已经把 `BILLING_HOST`/`BILLING_PUBLIC_KEY` 等写进了 `billing.env`,可以省掉这些参数,但只有通过 systemd 或手动 `source /etc/vps-billing/billing.env` 之后才会生效——直接 `sudo -u vps-billing billingctl ...` 不会读这个文件。

如果客户把链接弄丢了,不用重新开通,直接:

```bash
sudo -u vps-billing billingctl sub link -db /var/lib/vps-billing/billing.db -id 3 \
  -host 你的域名或IP -pbk <公钥> -sni www.example.com
```

重新打印一遍链接和二维码。

任意支持 Reality 的客户端都能识别这个 `vless://` 链接,包括 iOS 上的 **小火箭(Shadowrocket)**——较新版本可以直接扫二维码或粘贴链接导入,老版本可能不支持 Reality,建议先在 App Store 更新到最新版。

## 4. 让 sync 常驻

```bash
sudo systemctl enable --now vps-billing-sync.service
sudo systemctl status vps-billing-sync.service
journalctl -u vps-billing-sync.service -f
```

(`vps-billing-sync.service` 是第0步 `install.sh` 已经装好的,这里只是启用。)这个服务每 5 分钟做一次:拉每个在用订阅的流量增量累加进数据库、检查是否过期或超流量并停用、把 Xray 当前用户列表和数据库对齐(处理 Xray 重启后 API 加的用户丢失的问题)。

## 5. 日常运维

```bash
sudo -u vps-billing billingctl sub list -db /var/lib/vps-billing/billing.db      # 看所有人的用量/到期/在线设备数
sudo -u vps-billing billingctl sub renew -db /var/lib/vps-billing/billing.db -id 3     # 收到续费款后手动续期、清零用量
sudo -u vps-billing billingctl sub suspend -db /var/lib/vps-billing/billing.db -id 3 -reason "手动停用"  # 立即停用
sudo -u vps-billing billingctl sub resume -db /var/lib/vps-billing/billing.db -id 3    # 恢复(不改到期时间/用量)
```

## 设备数限制的真实能力

Xray 没有"超过N台设备自动踢掉多余那台"这种精确能力,`billingctl` 用 `xray api statsonlineiplist` 统计一个用户当前有几个不同来源IP连着,作为设备数的近似值,默认只在 `sub list` 里展示、不做任何动作。如果确认这个数字在你的 Xray 版本上靠谱,可以在 `/etc/vps-billing/billing.env` 里把 `BILLING_ENFORCE_DEVICE_LIMIT` 设成 `true`,超限会直接把整个订阅停用(不是只挑一台设备踢),重新激活要手动 `sub resume`。建议先观察几天数据再决定开不开。
