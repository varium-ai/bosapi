export const usageReportingEnglish = {
  title: 'Installation telemetry',
  notice:
    'Enabled by default on an installation without a saved choice. Your choice persists across restarts. This deployment sends only its random installation ID, build version, platform and daily running heartbeat to the configured receiver. No business content, user content or credentials are collected. Disable it here or through deployment configuration without affecting the gateway or local features.',
  disabled: 'Disabled',
  enabled: 'Enabled',
  forced_disabled: 'Disabled by deployment configuration',
  unavailable: 'Unavailable',
  switch: 'Enable installation telemetry',
  receiver: 'Receiver',
  missing: 'Not configured',
  calendar: 'Business calendar',
  frequency:
    'Installation metadata is queued when enabled. A current-day running heartbeat is generated once a day between 00:15 and 00:45 UTC+0; downtime is never backfilled as historical activity.',
  fields: 'Reported fields',
  fields_help:
    'Only schema version, random report/installation/epoch IDs, report kind, generation time, build version, OS and architecture. Daily heartbeats additionally include the current date and its time zone.',
  privacy:
    'No user ID, email, organization name, API key, request ID, model, provider name, cost, prompt, response, error content, IP or hostname is included. The random deployment ID links daily reports; it is a pseudonymous identifier.',
  retention:
    'Unsent reports are retained for up to 7 days, with at most 16 pending reports. Payloads are removed on success, permanent failure, expiry or disable. Local task metadata and operation records are retained for 30 days. The receiver operator supplies its own retention and deletion policy.',
  close_help:
    'Disabling cancels local delivery and clears pending report bodies. Other replicas stop within 30 seconds. Reports already received cannot be recalled. Re-enabling starts a new collection period without backfilling earlier periods.',
  limitation:
    'Telemetry never reads business usage logs or reports usage counts, Token counts or API categories. Only deployments with telemetry enabled and working connectivity are covered. No analytics dashboard is included in this client.',
  last_success: 'Last successful delivery',
  next: 'Next scheduled activity',
  pending: 'Pending reports',
  dropped: 'Reports dropped at queue limit',
  last_error: 'Last failure',
  none: 'None',
  preview: 'Preview report',
  preview_title: 'Report preview',
  template: 'Telemetry is off: this is an installation-only template. No business logs were read.',
  preview_help:
    'Installation-only preview. Nothing is queued or sent. Preview is limited to once every 10 seconds per replica.',
  enable_title: 'Enable installation telemetry?',
  enable_confirm: 'Enable',
  disable: 'Disable',
  saved: 'Telemetry switch saved persistently',
  failed: 'Usage reporting operation failed',
  reset: 'Reset deployment identity',
  reset_title: 'Reset this deployment identity?',
  reset_help:
    'Use after cloning a database into an independent deployment. Reporting must be disabled. Pending reports are cleared; the next enable starts a new collection period.',
  reset_done: 'Deployment identity reset',
  reason_forced_disabled:
    'Reporting is disabled by deployment configuration. Restart with an updated configuration to change this.',
  reason_endpoint_missing:
    'The receiving endpoint has not been included in this build. The project maintainer must set it before release.',
  reason_invalid_endpoint:
    'The configured receiving endpoint is invalid. HTTPS is required; credentials, query strings and fragments are prohibited.',
  reason_invalid_timeout: 'The reporting HTTP timeout must be between 1 and 10 seconds.',
  reason_invalid_force_disabled: 'The force-disable configuration must be exactly true or false.',
  reason_database_unavailable: 'Persistent MySQL storage is unavailable. Reporting has stopped.',
  reason_configuration_unavailable:
    'The persisted reporting configuration could not be read. Reporting has stopped.',
  error_network_error: 'Network error or timeout',
  error_receiver_rejected: 'Receiver permanently rejected the report',
  error_receiver_temporary: 'Receiver temporarily unavailable; retry scheduled',
  error_report_expired: 'Report expired',
  error_queue_limit: 'Oldest pending reports discarded at queue limit',
  error_window_incomplete: 'Skipped a window without confirmed log coverage',
  error_aggregation_failed: 'Aggregation failed; retry scheduled',
  error_invalid_payload: 'Invalid report payload',
};

export const usageReportingChinese: typeof usageReportingEnglish = {
  title: '安装量遥测',
  notice:
    '未保存过开关设置的部署默认开启安装量遥测，您的选择持久化保存，重启后保留。仅向配置的接收方发送随机部署标识、构建版本、平台及每日运行心跳。业务正文、用户内容和完整凭证均不采集。可在此页面或通过部署配置关闭，不影响网关、本地日志或其他本地功能。',
  disabled: '已关闭',
  enabled: '已开启',
  forced_disabled: '部署配置强制关闭',
  unavailable: '不可用',
  switch: '启用安装量遥测',
  receiver: '接收地址',
  missing: '未配置',
  calendar: '业务日历',
  frequency:
    '开启时立即排入部署元信息报告；每天 UTC+0 00:15～00:45 生成当日运行心跳，停机期间不补造历史在线记录。',
  fields: '上报字段',
  fields_help:
    '仅包含协议版本、随机报告／部署／启用周期标识、报告类型、生成时间、构建版本、操作系统和架构；每日心跳额外包含当日日期及时区。',
  privacy:
    '不发送用户 ID、邮箱、组织名称、API Key、请求 ID、模型、供应商名称、金额、Prompt、响应、错误正文、IP 或主机名。随机部署 ID 用于关联日报，属于假名标识。',
  retention:
    '未发送报告最长保留 7 天，最多 16 份；成功、永久失败、过期或关闭后清除正文。本地任务元数据及操作记录保留 30 天。接收方的保留期限及删除申请方式由接收服务维护方另行提供。',
  close_help:
    '关闭会取消本副本发送并清除待发送正文，其他副本最迟 30 秒内停止。已到达接收方的数据无法撤回。重新开启使用新周期，不补传之前的数据。',
  limitation:
    '遥测不读取业务用量日志，不发送业务计数、Token 数或接口分类。仅覆盖遥测已启用且成功联网的部署。本客户端不包含统计仪表盘。',
  last_success: '最近成功时间',
  next: '下次计划时间',
  pending: '待发送报告',
  dropped: '队列超限丢弃数',
  last_error: '最近失败',
  none: '暂无',
  preview: '预览报告',
  preview_title: '报告预览',
  template: '当前未启用：显示最小安装信息模板，不读取业务日志。',
  preview_help: '仅预览最小安装与运行信息，不入队、不联网。每副本每 10 秒最多预览一次。',
  enable_title: '开启安装量遥测？',
  enable_confirm: '开启',
  disable: '关闭',
  saved: '遥测开关已持久化保存',
  failed: '统计操作失败',
  reset: '重置部署身份',
  reset_title: '重置当前部署身份？',
  reset_help:
    '用于将克隆数据库作为独立部署。仅关闭时可操作，会清空待发送报告；下次开启使用新的统计周期。',
  reset_done: '部署身份已重置',
  reason_forced_disabled: '由部署环境配置强制关闭，需修改部署配置并重启后才能开启。',
  reason_endpoint_missing: '当前构建尚未内置接收地址，需要项目维护方填写固定地址后重新构建。',
  reason_invalid_endpoint: '接收地址配置无效：必须使用 HTTPS，且不能包含凭据、查询参数或片段。',
  reason_invalid_timeout: '统计 HTTP 超时必须在 1～10 秒之间。',
  reason_invalid_force_disabled: '强制关闭配置必须严格填写 true 或 false。',
  reason_database_unavailable: 'MySQL 持久化存储不可用，统计已停止。',
  reason_configuration_unavailable: '无法读取持久化统计配置，统计已停止。',
  error_network_error: '网络异常或超时',
  error_receiver_rejected: '接收方永久拒收，已停止重试',
  error_receiver_temporary: '接收方暂时不可用，已安排重试',
  error_report_expired: '报告已过期',
  error_queue_limit: '队列超限，已丢弃最旧待发送报告',
  error_window_incomplete: '无法确认日志覆盖完整，已跳过该窗口',
  error_aggregation_failed: '汇总失败，已安排重试',
  error_invalid_payload: '报告正文无效',
};
