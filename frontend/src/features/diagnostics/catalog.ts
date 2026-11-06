// One registry drives persistent errors, search metadata and deployed documentation.
export interface ConfigurationIssue {
  code: string;
  title: [string, string];
  reason: [string, string];
  checks: [string[], string[]];
  keywords: string[];
}
const issue = (
  code: string,
  title: [string, string],
  reason: [string, string],
  checks: [string[], string[]],
  keywords: string[] = [],
): ConfigurationIssue => ({ code, title, reason, checks, keywords });
const access: [string[], string[]] = [
  [
    'Check your account role in the account menu.',
    'Ask a super administrator to check Organizations > channel assignments. Assigned channels are read-only for organization administrators.',
    'Return to the original form and retry once access is granted.',
  ],
  [
    '在账户菜单确认当前角色。',
    '请超级管理员检查“组织管理”中的渠道分配。组织管理员只能查看已分配渠道。',
    '权限修复后返回原表单重试。',
  ],
];
const models: [string[], string[]] = [
  [
    'In Providers & Models, check the protocol and an enabled, named model; wildcard entries are insufficient.',
    'In Organizations, check channel bindings and model access. In API Keys, check the Key’s provider/model scope.',
    'Retry one request using the exact allowed model ID and matching protocol.',
  ],
  [
    '在“上游渠道”检查协议和已启用的具体模型，通配符不能替代具体模型。',
    '在“组织管理”检查渠道绑定和模型权限，再在“API Key”检查 Key 的渠道及模型范围。',
    '使用准确的授权模型 ID 和匹配协议重新调用一次。',
  ],
];
const generic: [string[], string[]] = [
  [
    'Check the instance address and connection. Open the console again to confirm the session.',
    'Check whether the resource was already saved before submitting again. Ask an administrator to inspect service health and logs using the request ID, if available.',
    'Return to the original page after recovery and retry once. If unresolved, send the sanitized diagnostic preview to your instance administrator.',
  ],
  [
    '确认实例地址和网络连接，重新打开控制台确认登录状态。',
    '再次提交前，先在列表确认资源是否已保存。请管理员检查服务状态，并使用请求 ID（如有）查询日志。',
    '恢复后返回原页面重试一次。仍未解决时，将脱敏诊断预览提供给实例管理员。',
  ],
];
export const configurationIssues: ConfigurationIssue[] = [
  issue(
    'invalid_request',
    ['Check configuration fields', '检查配置字段'],
    [
      'The server rejected the submitted values; a specific field or root cause has not been confirmed.',
      '服务端拒绝了提交的数据，尚未确认具体字段或根因。',
    ],
    [
      [
        'Review highlighted fields, required names, organization, URL format, quota and expiration.',
        ...models[0],
      ],
      ['检查标红字段、必填名称、所属组织、地址格式、额度和有效期。', ...models[1]],
    ],
    ['validation', '必填', '校验', '地址', '额度', '有效期'],
  ),
  issue(
    'field_validation',
    ['Correct the highlighted field', '修正标红字段'],
    [
      'A local form rule failed. No request ID is available because this form was not submitted.',
      '表单本地校验未通过，尚未提交此表单，因此没有请求 ID。',
    ],
    [
      [
        'Correct each highlighted field and submit again. Credentials stay only in the current form.',
        'Check required organization, URL protocol, non-negative quota, future expiration and allowed model scope.',
      ],
      [
        '逐项修正标红字段后重新提交。凭证仅保留在当前表单。',
        '检查必选组织、地址协议、非负额度、未来有效期及授权模型范围。',
      ],
    ],
    ['字段', 'validation'],
  ),
  issue(
    'invalid_provider',
    ['Invalid channel configuration', '渠道配置无效'],
    ['Channel configuration did not pass validation.', '渠道配置未通过校验。'],
    models,
  ),
  issue(
    'provider_auth_failed',
    ['Upstream authentication rejected', '上游拒绝认证'],
    [
      'The upstream returned HTTP 401. It did not accept this credential.',
      '上游返回 HTTP 401，未接受当前凭证。',
    ],
    [
      [
        'In Providers & Models > Edit, replace expired or invalid credentials. Do not use a BOS gateway Key as an upstream credential.',
        'Confirm the credential can access the selected model with the upstream account owner, then run Test again.',
      ],
      [
        '在“上游渠道 > 编辑”替换失效或过期凭证，勿将 BOS 网关 Key 当作上游凭证。',
        '向上游账户持有人确认该凭证有模型权限，然后重新测试。',
      ],
    ],
    ['认证失败', 'authentication', '401', '凭证', '过期'],
  ),
  issue(
    'invalid_oauth_session',
    ['Restart channel sign-in', '重新发起渠道登录'],
    [
      'The authorization session is missing, expired or does not match the callback. No upstream HTTP status has been established.',
      '授权会话不存在、已过期或与回调不匹配；尚未确认上游 HTTP 状态。',
    ],
    [
      [
        'In Providers & Models, start sign-in again and complete the callback in the same authorization flow.',
        'Use the newly generated callback, not an old browser tab. Confirm the instance callback address with your administrator, then retry once.',
      ],
      [
        '在“上游渠道”重新发起登录，并在同一次授权流程中完成回调。',
        '使用新生成的回调，勿复用旧标签页。请管理员确认实例回调地址后重试一次。',
      ],
    ],
    ['OAuth', '授权会话', 'callback'],
  ),
  issue(
    'oauth_exchange_failed',
    ['Channel authorization exchange failed', '渠道授权交换失败'],
    [
      'Authorization credentials could not be exchanged or accepted. An upstream HTTP 401 has not been established.',
      '授权凭证交换或校验失败；尚未确认上游返回 HTTP 401。',
    ],
    [
      [
        'In Providers & Models, restart official-account sign-in and use a fresh authorization code.',
        'Ask the instance administrator to check OAuth client settings, callback address and server connectivity. Retry once after recovery.',
      ],
      [
        '在“上游渠道”重新发起官方账户登录，使用新的授权码。',
        '请实例管理员检查 OAuth 客户端设置、回调地址及服务端网络，修复后重试一次。',
      ],
    ],
    ['OAuth', '授权交换', 'authorization code'],
  ),
  issue(
    'account_identity_missing',
    ['Authorization account information is incomplete', '授权账户信息不完整'],
    [
      'The authorization result lacks a required account identity or refresh credential. The cause is not confirmed.',
      '授权结果缺少必要的账户标识或刷新凭证，尚未确认根因。',
    ],
    [
      [
        'In Providers & Models, sign in again with the intended official account and complete authorization.',
        'Ask the instance administrator to check account compatibility and the deployed OAuth configuration. Confirm the channel appears before creating another one.',
      ],
      [
        '在“上游渠道”使用目标官方账户重新登录并完成授权。',
        '请实例管理员检查账户兼容性及当前版本的 OAuth 配置；再次创建前先确认渠道列表。',
      ],
    ],
    ['OAuth', '账户标识', 'refresh credential'],
  ),
  issue(
    'provider_access_denied',
    ['Upstream access denied', '上游拒绝授权'],
    [
      'The upstream returned HTTP 403. The reason for its access policy is not yet known.',
      '上游返回 HTTP 403，具体访问策略原因尚未确认。',
    ],
    [
      [
        'Ask the upstream account owner to check model permissions, account status and IP restrictions.',
        'Review channel protocol/address and retry Test after access is restored.',
      ],
      [
        '请上游账户持有人检查模型权限、账户状态及 IP 限制。',
        '检查渠道协议和地址，权限恢复后重新测试。',
      ],
    ],
    ['授权失败', '403', 'authorization'],
  ),
  issue(
    'provider_rate_limited',
    ['Upstream rate limited', '上游限流'],
    [
      'The upstream returned HTTP 429. This alone does not establish insufficient balance.',
      '上游返回 HTTP 429，不能仅据此认定余额不足。',
    ],
    [
      [
        'Check upstream rate/concurrency limits and any retry-after guidance.',
        'Wait before a single retry, or lower concurrency in Providers & Models. Check balance only when the upstream supplies evidence.',
      ],
      [
        '检查上游速率、并发限制及建议的等待时间。',
        '等待后重试一次，或在“上游渠道”降低并发。仅在上游提供证据时进一步检查余额。',
      ],
    ],
    ['429', '限流', 'rate limit'],
  ),
  issue(
    'upstream_timeout',
    ['Upstream request timed out', '上游请求超时'],
    [
      'The upstream did not respond within the allowed time. Network or upstream load may be involved.',
      '上游未在限定时间内响应，可能与网络或上游负载有关。',
    ],
    [
      [
        'Check the channel base URL and server-to-upstream network, DNS and proxy.',
        'Ask the upstream owner to check service health. Return to Test and retry once.',
      ],
      [
        '检查渠道地址及服务端到上游的网络、DNS 和代理。',
        '请上游维护者检查服务状态，返回测试并重试一次。',
      ],
    ],
    ['超时', 'timeout'],
  ),
  issue(
    'provider_discovery_failed',
    ['Channel connection or model discovery failed', '渠道连接或模型发现失败'],
    [
      'The connection/catalog check failed; authentication, network and protocol causes have not been distinguished.',
      '连接或模型目录检查失败，尚未区分认证、网络或协议原因。',
    ],
    models,
    ['发现模型', 'discovery', 'connection'],
  ),
  issue(
    'unsupported_conversion',
    ['Check protocol compatibility', '检查协议兼容性'],
    [
      'The requested protocol or model endpoint is unsupported. A catalog failure does not prove inference is unavailable.',
      '请求协议或模型目录接口不受支持；目录失败不能证明推理接口不可用。',
    ],
    models,
    ['协议不兼容', 'protocol'],
  ),
  issue(
    'provider_unavailable',
    ['No available upstream channel', '无可用上游渠道'],
    ['No usable upstream was available for this operation.', '本次操作没有可用的上游渠道。'],
    models,
  ),
  issue(
    'invalid_model',
    ['Check model configuration', '检查模型配置'],
    ['The model configuration is invalid.', '模型配置无效。'],
    models,
  ),
  issue(
    'model_not_allowed',
    ['Model access is restricted', '模型权限受限'],
    ['The model is outside the effective access policy.', '该模型不在生效的授权范围内。'],
    models,
    ['分配模型', '模型权限'],
  ),
  issue(
    'quota_exceeded',
    ['Configured quota exceeded', '已达到配置额度'],
    [
      'A configured gateway quota was exceeded. This is not evidence of upstream account balance.',
      '已达到网关配置额度，这不代表上游账户余额不足。',
    ],
    [
      [
        'Check API Keys > quota and usage, including daily/weekly/monthly limits.',
        'Ask an authorized administrator to adjust quota or wait for its reset; retry one request afterwards.',
      ],
      [
        '在“API Key”检查额度和用量，包括日、周、月限制。',
        '请有权限的管理员调整额度，或等待重置后再调用一次。',
      ],
    ],
    ['额度不足', 'quota'],
  ),
  issue(
    'rate_limited',
    ['Gateway rate limit reached', '达到网关速率限制'],
    ['A gateway request limit was reached.', '已达到网关请求速率限制。'],
    [
      [
        'Check API Key rate limits and Rate Limits policies.',
        'Wait for the window to reset or ask an administrator to adjust limits. Retry once.',
      ],
      ['检查 Key 速率限制及“速率限制”策略。', '等待窗口重置或请管理员调整限制，再重试一次。'],
    ],
    ['限流', '429'],
  ),
  issue(
    'forbidden',
    ['Permission required', '需要操作权限'],
    ['Your current account is not authorized for this operation.', '当前账户无权执行此操作。'],
    access,
    ['无权限', 'permission'],
  ),
  issue(
    'security_blocked',
    ['Request blocked by a security policy', '请求被安全策略阻止'],
    [
      'A gateway security policy blocked this request. This does not establish an account permission failure.',
      '网关安全策略阻止了本次请求，不能据此认定账户无操作权限。',
    ],
    [
      [
        'Ask an authorized administrator to find the request in Security Events using its time and request ID when available.',
        'Review the matching rule and the permitted request format. Correct the request or have the policy owner approve an appropriate policy change before retrying.',
      ],
      [
        '请有权限的管理员根据时间和请求 ID（如有）在“安全事件”定位请求。',
        '检查命中规则及允许的请求格式，修正请求；如需调整策略，请策略负责人确认后再重试。',
      ],
    ],
    ['安全策略', 'security policy'],
  ),
  issue(
    'unauthorized_tool',
    ['Tool call is not authorized', '工具调用未获授权'],
    [
      'A tool call was rejected by the tool authorization policy. This does not establish that the console account lacks permissions.',
      '工具调用被工具授权策略拒绝，不能据此认定控制台账户无权限。',
    ],
    [
      [
        'Check that the request declares the intended tool and that the returned tool name matches that declaration.',
        'Ask the policy owner to inspect the tool security event. Correct the declared tools or obtain authorization, then retry once.',
      ],
      [
        '检查请求是否声明目标工具，以及返回的工具名是否与声明一致。',
        '请策略负责人检查工具安全事件；修正工具声明或取得授权后重试一次。',
      ],
    ],
    ['工具授权', 'tool authorization'],
  ),
  issue(
    'unauthorized',
    ['Sign in again', '重新登录'],
    [
      'The session or supplied gateway credential was not accepted.',
      '登录会话或提交的网关凭证未被接受。',
    ],
    [
      [
        'Sign in again for console operations. For an API call, check the full gateway Key, its status and expiration in API Keys.',
        'Keep non-sensitive form values before leaving; do not store credentials in browser persistence.',
      ],
      [
        '控制台操作请重新登录；API 调用请在“API Key”检查完整 Key、启用状态及有效期。',
        '离开前保留非敏感表单内容，勿将凭证写入浏览器持久存储。',
      ],
    ],
    ['登录失效', '令牌', 'token', '401'],
  ),
  issue(
    'not_found',
    ['Resource unavailable', '资源不可用'],
    ['The resource is missing or not visible to this account.', '资源不存在，或当前账户不可见。'],
    access,
  ),
  issue(
    'conflict',
    ['Configuration conflict', '配置冲突'],
    [
      'The requested change conflicts with existing state. A duplicate name is only one possible cause.',
      '请求变更与现有状态冲突，重名仅是可能原因之一。',
    ],
    [
      [
        'Refresh the resource list before creating another item. Check whether the name is already in use.',
        'Review organization channel/model permissions, then correct the original form and retry.',
      ],
      ['创建前刷新资源列表，检查名称是否已使用。', '检查组织的渠道及模型权限，修正原表单后重试。'],
    ],
    ['名称冲突', 'duplicate'],
  ),
  issue(
    'network_error',
    ['Network connection interrupted', '网络连接中断'],
    [
      'No usable server response was received. A submitted change may still have completed.',
      '未收到可用的服务端响应；已提交的变更可能仍已完成。',
    ],
    generic,
    ['断网', 'network'],
  ),
  issue(
    'request_timeout',
    ['Request timed out', '请求超时'],
    [
      'A complete response was not received within the time limit. A timeout alone does not establish the cause.',
      '未在等待时限内收到完整响应，仅凭超时无法确定具体原因。',
    ],
    [
      [
        'Retry loading the affected data. If it still times out, ask your instance administrator to check service health and request logs.',
        'Provide the time, failed operation and request ID, if available, to help locate the request.',
      ],
      [
        '重试加载对应数据；若仍超时，请实例管理员检查服务状态和请求日志。',
        '提供发生时间、失败操作及请求 ID（如有），便于定位具体请求。',
      ],
    ],
    ['超时', 'timeout'],
  ),
  issue(
    'submission_uncertain',
    ['Confirm the submission result first', '请先确认提交结果'],
    [
      'The connection ended before a write result was confirmed. Repeating the operation could create duplicates.',
      '写入结果确认前连接已结束，重复操作可能创建重复资源。',
    ],
    generic,
    ['提交结果不确定', 'timeout'],
  ),
  issue(
    'internal_error',
    ['Service could not complete the operation', '服务未能完成操作'],
    ['A service error occurred; the root cause is not confirmed.', '发生服务异常，尚未确认根因。'],
    generic,
    ['服务异常', '500'],
  ),
  issue(
    'unknown_error',
    ['Operation failed', '操作失败'],
    [
      'This failure has no registered explanation. Internal details are hidden.',
      '此异常暂无已登记说明，内部详情已隐藏。',
    ],
    generic,
    ['未知异常', 'unknown'],
  ),
];
// Aliases refer to registered server identifiers, never raw error-message matching.
const aliases: Record<string, string> = {
  worker_timeout: 'request_timeout',
  invalid_period: 'invalid_request',
  aggregation_failed: 'internal_error',
  delivery_failed: 'internal_error',
  attempts_exhausted: 'internal_error',
  configuration_load_failed: 'internal_error',
  signing_secret_unavailable: 'internal_error',
  invalid_webhook_url: 'invalid_request',
  delivery_interrupted: 'internal_error',
  connection_failed_or_address_rejected: 'network_error',

  invalid_mfa: 'unauthorized',
  invalid_otp: 'unauthorized',
  live_unavailable: 'unsupported_conversion',
  image_task_unavailable: 'provider_unavailable',
  method_not_allowed: 'unsupported_conversion',
  request_too_large: 'invalid_request',
  unsupported_media_type: 'unsupported_conversion',
  upgrade_required: 'unsupported_conversion',
  tool_call_inspection_unavailable: 'internal_error',
  tool_call_invalid: 'invalid_request',

  invalid_credentials: 'unauthorized',
  authentication_error: 'unauthorized',
  inactive_user: 'unauthorized',
  provider_capability_unavailable: 'unsupported_conversion',
  unsupported_upstream_feature: 'unsupported_conversion',
  upstream_error: 'provider_discovery_failed',
  provider_in_use: 'conflict',
  policy_not_allowed: 'model_not_allowed',
  quota_accounting_unavailable: 'internal_error',
  rate_limiter_unavailable: 'internal_error',
  counter_backend_unavailable: 'internal_error',
  security_inspection_unavailable: 'internal_error',
  storage_failed: 'internal_error',
  sync_failed: 'internal_error',
};
export const issueAliases = aliases;
export const issueByCode = (code: string) =>
  configurationIssues.find(item => item.code === (aliases[code] || code)) ||
  configurationIssues.find(item => item.code === 'unknown_error')!;
export const issueText = <T>(pair: [T, T], language: string): T =>
  pair[language === 'zh-CN' ? 1 : 0];
export const issueHref = (code: string) => `/docs/configuration-errors#${issueByCode(code).code}`;
export function classifyConfigurationError(error: any): string {
  const server = error?.response?.data?.error?.code;
  if (typeof server === 'string')
    return issueByCode(server).code === 'unknown_error' ? 'unknown_error' : server;
  const write = ['post', 'put', 'patch', 'delete'].includes(error?.config?.method?.toLowerCase());
  if (!error?.response && (error?.code === 'ECONNABORTED' || error?.code === 'ETIMEDOUT'))
    return write ? 'submission_uncertain' : 'request_timeout';
  if (!error?.response) return write ? 'submission_uncertain' : 'network_error';
  return (
    (
      {
        401: 'unauthorized',
        403: 'forbidden',
        404: 'not_found',
        409: 'conflict',
        429: 'rate_limited',
      } as Record<number, string>
    )[error.response.status] || 'unknown_error'
  );
}
// Only fixed operation labels are shown; never echo request URLs or query values.
export function configurationOperation(error: any, pathname: string, language: string): string {
  const url = typeof error?.config?.url === 'string' ? error.config.url.split('?')[0] : pathname;
  const method = String(error?.config?.method || '').toLowerCase();
  if (error?.operation === 'verify-call')
    return issueText(['Verify first call', '验证首次调用'], language);
  if (method === 'get' || method === 'head') {
    const label: [string, string] = /\/providers(?:\/|$)/.test(url)
      ? /\/models(?:\/|$)/.test(url)
        ? ['Load channel models', '加载渠道模型']
        : /\/oauth(?:\/|$)/.test(url)
          ? ['Load channel authorization status', '加载渠道授权状态']
          : ['Load channel information', '加载渠道信息']
      : /\/organizations(?:\/|$)/.test(url)
        ? ['Load organization access information', '加载组织及授权信息']
        : /\/(?:api-)?keys(?:\/|$)/.test(url)
          ? ['Load API Key information', '加载 API Key 信息']
          : ['Load data', '加载数据'];
    return issueText(label, language);
  }
  const label: [string, string] = /\/providers(?:\/|$)/.test(url)
    ? /\/test$/.test(url)
      ? ['Test channel connection', '验证渠道连接']
      : /\/models(?:\/|$)/.test(url)
        ? ['Configure channel models', '配置渠道模型']
        : /\/oauth(?:\/|$)/.test(url)
          ? ['Authorize channel', '授权渠道']
          : ['Configure channel', '配置渠道']
    : /\/organizations(?:\/|$)/.test(url)
      ? ['Configure organization access', '配置组织及授权']
      : /\/(?:api-)?keys(?:\/|$)/.test(url)
        ? method === 'post' && /\/(?:api-)?keys\/?$/.test(url)
          ? ['Create API Key', '创建 API Key']
          : ['Configure API Key', '配置 API Key']
        : ['Configuration operation', '配置操作'];
  return issueText(label, language);
}
export function diagnosticPreview(error: any, code: string) {
  // Strict allowlist: no URL/query/body/headers/error text, even for unknown errors.
  const rawID = error?.response?.data?.request_id || error?.response?.headers?.['x-request-id'];
  const requestID =
    typeof rawID === 'string' &&
    /^[a-zA-Z0-9._-]{1,128}$/.test(rawID) &&
    !/^(sk-|eyJ|Bearer)/i.test(rawID)
      ? rawID
      : undefined;
  const status = error?.response?.status;
  return JSON.stringify(
    {
      code: issueByCode(code).code,
      ...(requestID ? { request_id: requestID } : {}),
      ...(Number.isInteger(status) ? { status } : {}),
    },
    null,
    2,
  );
}
