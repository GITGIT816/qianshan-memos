# vps-billing

一个跑在你 VPS 上、给家人朋友分摊节点费用用的小工具:按你定的套餐(价格 / 流量 / 有效期 / 设备数)开通、续费、到期自动停用 Xray 用户,并周期性把 Xray 的实时用户列表和你数据库里的订阅状态对齐。

这是一个**独立的小工程**,和上层 `qianshan-memos`(笔记应用)没有任何代码或运行时关联,可以整个目录复制到别处单独维护。

## 它是什么、不是什么

- ✅ 管理"谁在用、用了多少、什么时候到期"这件事的一个小型计费/配额引擎。
- ✅ 通过 `xray api` 命令行(Xray-core 自带)在 Xray 运行时增删用户、拉取每用户流量计数,不需要重启 Xray。
- ❌ 不做支付对接、不生成账单、不发短信/邮件通知——这些留给你手动操作(`sub renew`)或自己按需接入。
- ❌ 只支持 **Xray-core**。如果你VPS上跑的是 sing-box / v2ray 等其他内核,这套 `xray api` 命令不适用,需要换成对应内核的 API(sing-box 是 Clash API,协议和字段都不同)。

## 快速上手

```bash
cd vps-billing
go build -o billingctl ./cmd/billingctl

# 建库 + 按截图里的三档套餐建 轻量/标准/重度
./billingctl seed-plans -db ./billing.db

# 建一个客户,开一个订阅(需要 Xray 已按 configs/xray.config.example.json 打开API)
./billingctl customer add -db ./billing.db -name "Alice"
./billingctl sub create -db ./billing.db -customer 1 -plan 2 -email alice@yournode -tag vless-in

# 收到续费款之后
./billingctl sub renew -db ./billing.db -id 1

# 后台常驻:每5分钟拉一次流量、清理过期/超额账号、把Xray用户列表和数据库对齐
./billingctl sync -db ./billing.db -interval 5m
```

完整部署步骤(改 Xray 配置、装 systemd 服务、怎么把 uuid 拼成客户端订阅链接)见 [`docs/DEPLOY.md`](docs/DEPLOY.md)。

## 目录结构

```
cmd/billingctl/        CLI 入口
internal/model/         Plan / Customer / Subscription 领域模型
internal/store/         SQLite 持久化
internal/xrayctl/       封装 `xray api ...` 命令行调用
internal/billing/       套餐开通/续费/停用 + 定时对账的业务逻辑
configs/                Xray 配置片段示例、systemd 单元、环境变量样例
docs/DEPLOY.md           从零部署到日常运维的完整步骤
```

## 已知局限

- **设备数限制是尽力而为**:通过 `xray api statsonlineiplist` 统计某用户当前有几个不同来源IP在连,不是精确的"设备数",且这个API在不同Xray版本里返回结构不算稳定(其他几个API——加/删用户、查流量——是长期稳定的)。默认只记录、不强制停用,想开启强制停用见 `BILLING_ENFORCE_DEVICE_LIMIT`,但建议先跑几天观察 `sub list` 里的 DEVICES 列是否符合预期,再决定要不要打开强制模式,以免误封自己人。
- **API 增删的用户不持久化**:通过 API 加进 Xray 的用户不会写回 config.json,Xray 重启后就没了。`sync` 每次都会做一次对账(reconcile),用数据库里的"应该有谁"去修正 Xray 里"实际有谁",所以只要 sync 常驻运行,重启后几分钟内会自动补回来;但如果你手动改了 Xray 又忘了让 sync 跑一轮,两边可能短暂不一致。
- **不做支付**:套餐从"付了多少钱"到"该给哪个套餐"这一步,是你自己在 `sub create -plan <id>` 时手动选的,工具不判断也不校验有没有真的收到钱。

## 合规提醒

这套工具设计给自己和家人朋友几个人分摊 VPS 代理费用用。如果打算对外向陌生人收费提供代理/VPN服务,在中国大陆经营此类业务需要相应的电信业务经营许可,私自对外经营存在合规风险,请自行了解清楚后再决定要不要扩大到这个场景。
