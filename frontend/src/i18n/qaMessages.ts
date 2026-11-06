export const qaEnglish = {
  address_load_error:
    'Could not confirm the API address. Retry loading the settings before sharing connection details.',
  trace_empty_help:
    'Check the Request ID, the source deployment and the log retention period. No record does not establish a failed call.',
  smtp_test: 'Test saved SMTP connection',
  smtp_test_help:
    'Save changes first. Checks the connection and authentication only; no email is sent or delivery confirmed.',
  smtp_test_failed:
    'Connection test failed. Check the saved host, port, credentials and TLS support.',
  smtp_test_ok:
    'Connection and authentication succeeded over TLS. Email delivery has not been tested.',
  smtp_test_plain: 'Connection succeeded without encryption. Email delivery has not been tested.',

  route_target_required: 'Select at least one short or long request channel.',
  route_no_channels: 'Create a channel before selecting a route target.',
  configure_channels: 'Configure channels',
  route_save_error:
    'Could not save this route. Check the targets, model and rule name, then retry. Your draft is preserved.',
  address_check: 'Check the API address before handing over connection details',
  address_help:
    'The public API address has not been configured. Ask a super administrator to publish the address used by clients. If this console domain also exposes the gateway, its /v1 path can be used after verification; a separate proxy may use another address.',
  configure_address: 'Configure API address',
  no_access:
    'No callable models are available under the selected organization and channel restrictions. You may create the Key now, but configure channel access before calling it.',
  configure_access: 'Configure organization access',
  probe_latency: 'Latest health probe time',
  probe_help:
    'MySQL Ping / Redis PING from the latest sample; collected every {seconds} seconds. This is not completed-request latency.',
  install_guide: 'Host updater installation guide',
  recheck: 'Recheck service status',
  smtp_tls:
    'Port 465 uses TLS from connection start. Other ports (usually 587) upgrade via STARTTLS when supported; authentication requires STARTTLS. An internal relay without authentication may be unencrypted. Use the port provided by your mail service.',
  price_unknown: 'Price needs confirmation',
  price_default: 'Channel default price',
  price_model: 'Model override price',
  price_recorded: 'Recorded model price',
  price_note:
    'Costs are recorded using configured channel/model prices, not the upstream bill. An unconfirmed zero does not mean the upstream is free. The capability catalog does not supply prices.',
  price_confirm:
    'Configure both input and output prices; enter 0 explicitly to confirm a zero rate.',
  authorize_next:
    'Models are saved. Next, authorize them for the organization before creating or using a Key.',
  saved_discovery_failed:
    'The channel was saved, but model discovery failed. Retry discovery here; saving a second channel is unnecessary.',
};

export const qaChinese: typeof qaEnglish = {
  address_load_error: '暂时无法确认 API 地址，请重新加载配置后再交付连接资料。',
  trace_empty_help: '请核对 Request ID、请求所属环境及日志保留期限；查无记录不代表调用失败。',
  smtp_test: '检测已保存的 SMTP 连接',
  smtp_test_help: '请先保存修改；仅检查连接和认证，不发送邮件，也不代表投递成功。',
  smtp_test_failed: '连接检测失败，请检查已保存的主机、端口、凭据和 TLS 支持。',
  smtp_test_ok: 'TLS 连接和认证检查通过，尚未验证邮件投递。',
  smtp_test_plain: '连接检查通过，但连接未加密，尚未验证邮件投递。',

  route_target_required: '请至少选择一个短请求或长请求渠道。',
  route_no_channels: '请先创建渠道，再选择路由目标。',
  configure_channels: '配置渠道',
  route_save_error: '路由保存失败，请检查目标渠道、模型及规则名称后重试，当前输入已保留。',
  address_check: '交付连接资料前，请检查 API 地址',
  address_help:
    '尚未配置对外 API 地址，请联系超级管理员发布客户端使用的地址。若控制台域名也提供网关服务，可验证其 /v1 路径后使用；独立代理可能使用其他地址。',
  configure_address: '配置 API 地址',
  no_access: '当前组织及渠道限制下尚无可调用模型。可以先创建密钥，配置渠道和组织授权后才能调用。',
  configure_access: '配置组织授权',
  probe_latency: '最近一次健康探测耗时',
  probe_help:
    '最近一次采样的 MySQL Ping / Redis PING 耗时，每 {seconds} 秒采集一次，与已完成请求平均耗时不同。',
  install_guide: '宿主机更新服务安装指南',
  recheck: '重新检测服务状态',
  smtp_tls:
    '465 端口连接时即启用 TLS；其他端口（通常为 587）在服务器支持时升级为 STARTTLS，使用用户名认证时必须支持 STARTTLS。无认证的内部中继可能为明文，请使用邮件服务商提供的端口。',
  price_unknown: '价格待确认',
  price_default: '渠道默认定价',
  price_model: '模型独立定价',
  price_recorded: '模型已记录定价',
  price_note:
    '费用按渠道／模型配置的价格记录，不等于上游账单。未确认的零值不代表上游免费；能力库不提供价格。',
  price_confirm: '请同时配置输入和输出价格；明确填写 0 表示确认零价。',
  authorize_next: '模型已保存，下一步请为组织授权，再创建或使用密钥调用。',
  saved_discovery_failed: '渠道已保存，但模型发现失败，请在此重试发现，无需再次创建渠道。',
};
