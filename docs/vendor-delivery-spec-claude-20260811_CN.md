# Claude 供应商交付规范（本地快照）

- 原始地址：<https://14.103.248.65/dashboard/vendor-delivery-spec?provider=claude>
- 快照日期：2026-08-11（Asia/Shanghai）
- 获取方式：使用 Defuddle 提取页面正文为 Markdown
- 说明：以下内容为供应商页面的本地留档；后续实现与验收以该快照作为格式基线。

---

## 1. 交付目标

请按本文格式交付原始请求与原始响应。每一次模型请求对应一条独立的 JSON 记录。

本文只规定交付格式和基础质量要求，不规定数据筛选、采购范围或质量分级规则。

## 2. 交付文件组织

本文不强制交付包的压缩格式、目录层级或文件命名方式。具体打包格式和传输方式可根据实际交付渠道确定。

数据文件可以采用以下三种方式：

- 每次请求保存为一个独立的 `.json` 文件。
- 每个 Session 保存为一个 `.jsonl` 文件，每一行对应一次模型请求。这是推荐方式。
- 多个 Session 保存在一个或多个 `.jsonl` 文件中，每一行对应一次模型请求，并通过记录中的 `session_id` 区分 Session。

无论采用哪种方式，都应满足：

- 每次模型请求对应一个完整、可独立解析的 JSON object。
- 每条记录包含该次请求的原始 request 和完整 response。
- JSONL 每一行只能包含一条请求记录，不使用跨行 JSON。
- 同一 Session 的请求能够通过 `session_id` 关联，并根据 `timestamp` 还原顺序。
- 不要混入日志、SSE 原文或其他非 JSON 内容。
- 不支持在一个 `.json` 文件中使用数组保存多条请求记录。

最终验收以每条请求 JSON 是否符合第 3 章的字段格式为准。

## 3. 单条请求 JSON

每条请求记录是一个 UTF-8 JSON object；单独保存时使用 `.json`，写入 JSONL 时每行保存一条记录。

```json
{
  "session_id": "session_01ABC",
  "request_id": "req_01ABC",
  "timestamp": "2026-05-07T16:15:32.123Z",
  "metadata": {
    "http_status": 200,
    "latency_ms": 1432,
    "user_agent": "claude-code/1.0"
  },
  "request": {},
  "response": {
    "response_data": {}
  }
}
```

| 字段 | 是否必填 | 说明 |
| --- | --- | --- |
| `session_id` | 必填 | 请求所属 Session 的稳定 ID，用于关联并还原完整轨迹。 |
| `request_id` | 建议 | 供应商侧或上游侧的单次请求 ID，用于排查重复和错误。 |
| `timestamp` | 必填 | 请求发生时间。使用 ISO8601，必须带时区，建议 UTC `Z` 后缀。 |
| `metadata` | 建议 | 排查字段，例如 `http_status`、`latency_ms`、`user_agent`、`upstream_request_id`。 |
| `request` | 必填 | Anthropic Messages API 的原始 request body。不要再额外包一层 `body`。 |
| `response` | 必填 | 本次请求的响应信息。正常情况下应包含 `response_data`。 |
| `response.status_code` | 建议 | HTTP 状态码。成功请求通常为 `200`。 |
| `response.response_data` | 成功请求必填 | 完整 decoded response body，不是 SSE 原文。 |
| `response.error` | 理论上不应出现 | 仅用于兼容偶发混入的失败请求；常规交付中不应包含失败请求。 |

## 4. 成功请求示例

```json
{
  "session_id": "session_01ABC",
  "request_id": "req_01DEF",
  "timestamp": "2026-05-07T16:15:32.123Z",
  "metadata": {
    "http_status": 200,
    "latency_ms": 1432,
    "user_agent": "claude-code/1.0"
  },
  "request": {
    "model": "claude-opus-4-7",
    "max_tokens": 16384,
    "messages": [
      {"role": "user", "content": [{"type": "text", "text": "Hello"}]}
    ]
  },
  "response": {
    "status_code": 200,
    "response_data": {
      "id": "msg_01DEF",
      "type": "message",
      "role": "assistant",
      "content": [
        {"type": "text", "text": "Hello."}
      ],
      "stop_reason": "end_turn"
    }
  }
}
```

## 5. 保真要求

- 除脱敏外，不应修改原始 `request` 内容。
- 如果偶发混入失败请求，不要丢失原始请求；响应错误信息放入 `response.error`，不要塞进 `response.response_data`。
- 不要把 SSE 原文、日志文本或 HTML 错误页放入 `response.response_data`。
- 响应中如包含 `thinking.signature` 或 `redacted_thinking.data`，必须原样保留。
- 如需修改，请先与我同步。

## 6. 质量要求

- 常规交付应只包含成功请求。非 2xx、上游错误、预算错误、系统过载等失败请求理论上不应出现。
- **Thinking 签名：** 建议至少 30% 的成功请求在当前响应中包含非空 `thinking.signature`。这是优先目标，不是硬性验收项；请按真实生产情况交付，不要为凑比例修改配置或构造数据。

## 7. 交付前检查

| 检查项 | 要求 |
| --- | --- |
| 交付包 | 压缩格式、目录层级和文件命名方式不作强制要求。 |
| 数据文件 | 使用独立 `.json` 或逐行记录的 `.jsonl`；不使用 JSON 数组保存多条请求。 |
| Session | `session_id` 必填，能够关联同一 Session 的请求并还原顺序。 |
| 时间 | `timestamp` 必填且带时区。 |
| 请求 | `request` 必填，保留原始 Anthropic request body。 |
| 响应 | `response.response_data` 必填，内容为成功请求的完整 decoded response body。 |
| 失败请求 | 理论上不应包含非 2xx、上游错误、预算错误、系统过载等失败请求；如偶发混入，使用 `response.error` 表达。 |
| 杂项内容 | 数据文件中不混入系统文件内容、临时日志、SSE 原文或其他非 JSON 内容。 |
