import type { DocPage } from '../docs/types';
import { configurationIssues, issueAliases, issueText } from './catalog';

export function configurationErrorDoc(language: string): DocPage {
  const zh = language === 'zh-CN';
  return {
    slug: 'configuration-errors',
    title: zh ? '配置错误排查' : 'Configuration troubleshooting',
    group: zh ? '帮助与排错' : 'Help & troubleshooting',
    icon: 'help',
    description: zh
      ? '渠道、组织、API Key 和首次调用的排查步骤及错误码。'
      : 'Channel, organization, API Key and first-call errors with recovery steps.',
    sections: configurationIssues.map(item => ({
      id: item.code,
      title: issueText(item.title, language),
      blocks: [
        {
          type: 'paragraph',
          text: `${zh ? '适用范围：当前部署版本的渠道、组织、API Key 配置及首次调用。错误码：' : 'Applies to channel, organization, API Key setup and first calls in this deployed version. Codes: '}${[item.code, ...Object.keys(issueAliases).filter(code => issueAliases[code] === item.code)].join(', ')}`,
        },
        {
          type: 'note',
          title: zh ? '现象与判断依据' : 'Symptoms and evidence',
          text: issueText(item.reason, language),
        },
        { type: 'list', ordered: true, items: issueText(item.checks, language) },
        {
          type: 'paragraph',
          text: zh
            ? '必要权限：组织管理员可管理本组织 Key 并查看已分配渠道；渠道变更、渠道分配和组织创建需超级管理员。'
            : 'Permissions: organization administrators manage their own Keys and inspect assigned channels. Provider changes, organization creation and channel assignments require a super administrator.',
        },
        {
          type: 'paragraph',
          text: zh
            ? '修复后返回原配置页面，确认资源已保存并重新测试。渠道测试通过仅证明连接检查通过；首次调用成功需在“验证调用”中检查服务端成功记录，或取得具有内容的有效模型响应。'
            : 'Return to the original configuration page after fixing the issue. Confirm the resource was saved and rerun Test. Channel tests establish connectivity only; a first call requires a successful server record or a valid model response containing output.',
        },
        {
          type: 'note',
          title: zh
            ? '仍未解决：联系实例管理员'
            : 'Still failing: contact your instance administrator',
          text: zh
            ? '提供操作步骤、发生时间、版本、错误码及请求 ID（如有），并先预览脱敏诊断。勿提供完整 Key、上游凭证、登录令牌或敏感请求内容。超时或断网后先确认列表中是否已保存，再决定是否重试。'
            : 'Provide reproduction steps, time, version, error code and request ID when available. Preview sanitized diagnostics first. Never include Keys, upstream credentials, session tokens or sensitive request content. After timeouts/network failures, confirm whether the resource exists before retrying.',
        },
      ],
    })),
  };
}

// Keep the long-lived first-key anchor compatible with the current four-step flow.
