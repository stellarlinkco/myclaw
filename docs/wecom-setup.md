# 企业微信（WeCom）智能机器人接入教程

## 前置条件

- 企业微信管理员账号
- `myclaw` 已编译（`make build`）
- 一个可公网访问的回调 URL（生产建议 HTTPS）

> 说明：myclaw 只实现渠道协议与业务逻辑；公网入口、证书、域名和反向代理由部署方自行配置。

## 协议说明（当前仅支持这一种）

当前 `wecom` 通道只实现 **企业微信智能机器人 API 模式**（不是自建应用回调模式）。

回调特点：

- URL 校验：`GET` + `msg_signature/timestamp/nonce/echostr`
- 消息推送：`POST`，Body 为 JSON 加密包（`{"encrypt":"..."}`）
- 入站解密后为 JSON（含 `msgid/from.userid/response_url/msgtype` 等）
- 出站通过 `response_url` 回发 `markdown` 消息

## 能力边界

当前支持：

- 入站消息解析：`text`、`voice`、`mixed`（仅提取其中 `text` 项）
- 出站回包：`markdown`（通过 `response_url`）
- `allowFrom` 白名单控制（未配置或空数组时默认放行）
- `msgid` 去重
- 回调签名校验 + 加解密

当前不支持：

- 模板卡片
- 流式回复
- 复杂事件处理

## 第一步：创建企业微信智能机器人

1. 登录企业微信管理后台
2. 进入「安全与管理」→「管理工具」→「创建机器人」
3. 选择 **API 模式创建**

   ![74444fff04262489ee33c735877cc976.png](https://i.mji.rip/2026/02/09/74444fff04262489ee33c735877cc976.png)

4. 记录以下字段：
   - `Token`
   - `EncodingAESKey`

> 可选字段：`ReceiveID`（若你明确知道加解密校验用的接收方 ID，可在 myclaw 中配置；不配则不做严格 ReceiveID 校验）

## 第二步：配置回调 URL

在企业微信机器人配置页填写：

- URL：`https://your-domain.com/wecom/bot`
- Token：与你配置文件一致
- EncodingAESKey：与你配置文件一致

保存时平台会发起 URL 验证请求，myclaw 会自动处理。

## 第三步：配置 myclaw

编辑 `~/.myclaw/config.json`：

```json
{
  "channels": {
    "wecom": {
      "enabled": true,
      "token": "your-token",
      "encodingAESKey": "your-43-char-encoding-aes-key",
      "receiveId": "",
      "port": 9886,
      "allowFrom": ["zhangsan"]
    }
  }
}
```

### 配置项说明

| 参数 | 类型 | 说明 |
|------|------|------|
| `enabled` | bool | 是否启用 WeCom 通道 |
| `token` | string | 回调签名 Token |
| `encodingAESKey` | string | 回调加解密密钥（43 位） |
| `receiveId` | string | 可选，启用严格接收方 ID 校验 |
| `port` | int | 回调服务端口（默认 9886） |
| `allowFrom` | []string | 可选白名单；未配置或空数组时默认接收所有用户 |

### 环境变量（可选覆盖）

```bash
export MYCLAW_WECOM_TOKEN="your-token"
export MYCLAW_WECOM_ENCODING_AES_KEY="your-43-char-encoding-aes-key"
export MYCLAW_WECOM_RECEIVE_ID="optional-receive-id"
```

## 第四步：启动并验证

```bash
make gateway
```

日志出现如下信息表示通道已启动：

```text
[wecom] callback server listening on :9886
[gateway] channels started: [wecom]
```

然后在企业微信里给机器人发一条文本消息，观察网关是否回包。

## 关键限制与风险

- `allowFrom` 行为是“默认放行”：
  - 未配置或 `[]`：接收所有入站消息
  - 配置非空列表：仅接收列表中的用户
  - 注意：若错误配置成 `allowFrom: [""]`，会被视为“启用白名单”，可能导致全部被拒绝
- 出站依赖临时 `response_url`：
  - 只有在该会话最近有入站消息且缓存了 `response_url`，myclaw 才能回消息
  - `response_url` 基本是单次/短时有效，不要依赖延迟回包或多次发送
  - `response_url` 过期后，发送会失败并返回错误
- 出站 `markdown.content` 最长 20480 字节，超过会被截断（不是自动分片）
- 不要把 `token/encodingAESKey` 提交到仓库
