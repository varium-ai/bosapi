# Codex OAuth 更新版本后的请求失败排查

排查日期：2026-11-11。环境：`https://test.bosapi.net`。
修复分支：`hotfix/codex_system_messages`。

## 结论和证据

版本同步已在测试环境生效：18:05 的启动日志先声明 `0.166.0`，随后自动更新到
`0.167.1`。更新客户端版本声明并不会自动更新网关的协议转换能力。

18:06 的两个 Chat 请求均收到 Codex 上游 HTTP 400，原因为
`System messages are not allowed`：

| 请求 ID | 渠道 ID | 模型 |
| --- | --- | --- |
| `req_fe71a4a6b8141b4540e549cf5a9c8137` | 4 | `gpt-6-luna` |
| `req_823c959127f2e0d6b49e89430d02e702` | 3 | `gpt-6.1-sol` |

使用同一网关密钥、同一个 `gpt-6.1-sol` 模型进行对照：

| 请求 | 测试环境结果 |
| --- | --- |
| Chat：`messages` 包含前置 `role: system` | 网关 HTTP 502；上游拒绝系统消息 |
| Responses：系统提示放在 `instructions`，`input` 只有 user | HTTP 200，文本 `OK`，收到 `response.completed`，总用量 20 tokens |

因此本次最小请求已证明该账号和模型可以完成推理。当前失败点在 Chat 到 Codex
Responses 的请求转换，不能继续归因于模型列表或旧版本声明。这个结论限于本次
测试的模型、账号和请求，并不保证所有模型及高级能力都可用。

成功的原生 Responses SSE 还包含 `response.metadata`。原有 Chat 响应转换器
将其视为不支持的事件，因此仅修复系统消息后，仍可能在返回途中中断。

## 修改方向与范围

1. Chat 转 Codex：将前置纯文本 system 消息合并到 `instructions`，并从
   `input` 移除，避免发送 Codex 不接受的角色，也避免重复注入系统提示。
   developer 消息继续保留原角色。
2. 原生 OAuth Responses：同样提升前置纯文本 system 输入，已有
   `instructions` 接在提升的文本之后；其余历史项保留，包括不透明推理内容和
   工具参数。无法无损转换的内容、元数据或出现在其他输入项之后的 system
   消息返回明确的字段错误，避免丢失数据或重排对话。
3. Codex 转 Chat：识别并跳过辅助 `response.metadata`；流式和非流式共用
   处理。仍须收到 `response.completed` 才能成功结束并记录终态用量。
4. API Key 渠道及其他厂商协议不在本次修改范围内。无法在 Chat 中表达的
   不透明推理输出仍保持原有限制。后续 token 限制的验证与修改见下文。

```text
Chat -> Codex:
  instructions = join(prefix_system_text, "\n\n")
  input = convert(conversation_without_prefix_system)
  send(instructions, input, stream=true, store=false)

native OAuth Responses -> Codex:
  validate all system input before mutating request
  require system items to be a text-only prefix without extra metadata
  instructions = join(promoted_text + existing_instructions, "\n\n")
  preserve every remaining input item

Codex SSE -> Chat:
  response.metadata: continue
  response.output_text.delta: emit/collect text
  response.completed: validate final output, record usage, finish
  unknown event / EOF without completion: explicit conversion error
```

## 回归验证

测试覆盖 Chat 与原生 Responses 的流式、非流式请求；系统提示只提升一次；
保留 developer、推理历史和大整数工具参数；拒绝有损转换；使用实测事件顺序
构造包含 metadata 的 SSE；验证正文、用量与流结束；验证 metadata 本身不能
代替完成事件，且未知事件不会被忽略。

这份记录中的远端对照测试针对修复前部署。修复后仍须更新测试环境，再重放
相同 Chat 请求，确认上游不再拒绝 system，且客户端收到完整的流式结束。

## 补充：保留 token 限制的兼容转换

同日继续验证发现，当前部署使用的 Codex 上游已接受 `max_output_tokens`：

| 最小原生 Responses 请求 | 结果 |
| --- | --- |
| `max_output_tokens: 32`，要求回答 OK | HTTP 200，完成响应保留该限制，回答 OK |
| `max_completion_tokens: 32` | 网关 HTTP 502，上游未接受该字段 |
| `max_output_tokens: 16`，要求输出 1 至 100 | `response.incomplete`，原因 `max_output_tokens`，实际 output_tokens 为 16 |

参考的 sub2api 提交 `530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484` 在
`openai_codex_transform.go` 中将 `max_output_tokens` 和
`max_completion_tokens` 列为 OAuth 不支持字段并移除。本次根据上述当前环境
实测采用参数转换，保留上游限制，不照搬该删除逻辑。

修改范围为 OAuth Chat 转 Codex：复用已有正整数校验与别名冲突检查，将
`max_tokens` / `max_completion_tokens` 映射为 `max_output_tokens`。
上游 budget 包括推理 token，不承诺单独的可见文本 token 上限。
仅将原因明确为 `max_output_tokens` 的 incomplete 终态转换为 Chat
`finish_reason: "length"`，保留可表达的已生成输出和用量。其他 incomplete
原因、不透明推理输出或无效的截断工具参数仍明确报错。

```text
limit = validated_alias(max_tokens, max_completion_tokens)
if limit exists: codex.max_output_tokens = limit
if terminal.type == response.incomplete:
  require terminal.status == incomplete
  require terminal.incomplete_details.reason == max_output_tokens
  validate and return available output + usage
  chat.finish_reason = length
  finish SSE with [DONE]
```

## 补充：更新后上游 200，Chat 仍返回 502

修复分支：`hotfix/codex_response_format`。

确认 11 月 11 日 19:18 启动的新容器为 `bosapi-bg-193c26c4815e`，已自动
同步版本到 `0.167.1`。19:19–19:20 的多个请求已越过上游请求校验，上游
返回 HTTP 200，但 Chat 网关在转换前报错：

```text
request_id=req_c98c80e3f04c64f755dde90c3d8996fc
upstream_status=200
failure_stage=response_conversion
error_param=response.content-type
error_reason=expected an SSE stream
```

同环境最小重放确认：原生 Responses 返回完整 SSE，而 Chat 返回上述 502。
再次用与 Chat 转换器相同的 `type: message`、`content: input_text` 数组输入
调用原生 Responses，约 3.5 秒返回 HTTP 200，正文以
`event: response.created` 开头，并有 `response.completed` 终态。
原生转发路径会设置自己的 SSE 响应头，因此不能据此断言上游实际声明的
Content-Type 值；旧日志没有记录该字段。

修改方向：解除 Chat 只依据响应头判断格式的限制。声明 SSE 时继续原有路径；
否则只读取有界缓冲中的首个 SSE 字段前缀（`event:`、`data:`、注释、
`id:`、`retry:`），识别后完整重放所有字节，交给同一事件解析器验证。
JSON/HTML 或任意纯文本不能通过格式识别；未知事件、非法 JSON 和缺失终态
仍失败。保留上游 body 关闭和首 token 计时，并记录经过脱敏处理的
`upstream_content_type`，不输出正文或其他响应头。

```text
if upstream Content-Type declares SSE:
  parse SSE as before
else if bounded first bytes identify an SSE field:
  replay buffered bytes + remaining body through first-token observer
  parse known events and require a valid terminal event
else:
  return explicit response format error
```

本地基础设施、接口两个包完整测试通过。新增测试覆盖缺失/错误 Content-Type
下的 Chat JSON 和 SSE 输出、分片读取、用量、结束标记、错误页拒绝、未知事件、
截断流、首 token 计时和 body 关闭。修复未部署；更新后的远端 Chat 实测仍是
上线后的验收步骤，不能将本地回归通过等同于远端请求已经恢复。
