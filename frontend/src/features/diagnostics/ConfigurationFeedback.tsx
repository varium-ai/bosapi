import { useEffect, useState } from 'react';
import { Alert, Button, Space, Typography } from 'antd';
import { useI18n } from '../../i18n';
import { useLocation } from 'react-router-dom';
import {
  classifyConfigurationError,
  configurationOperation,
  diagnosticPreview,
  issueByCode,
  issueHref,
  issueText,
} from './catalog';
import { copyText } from '../common/copyText';

export function ConfigurationFeedback() {
  const { language } = useI18n();
  const { pathname } = useLocation();
  const [failure, setFailure] = useState<{
    code: string;
    preview: string;
    operation: [string, string];
  }>();
  const [preview, setPreview] = useState(false);
  const [copied, setCopied] = useState(false);
  const zh = language === 'zh-CN';
  useEffect(() => {
    setFailure(undefined);
    const clear = () => setFailure(undefined);
    const receive = (event: Event) => {
      const error = (event as CustomEvent).detail;
      if (error?.config?._configurationPath && error.config._configurationPath !== pathname) return;
      const code = classifyConfigurationError(error);
      if (code === 'field_validation') return;
      setFailure({
        code,
        preview: diagnosticPreview(error, code),
        operation: [
          configurationOperation(error, pathname, 'en-US'),
          configurationOperation(error, pathname, 'zh-CN'),
        ],
      });
      setPreview(false);
      setCopied(false);
    };
    window.addEventListener('bos:configuration-error', receive);
    window.addEventListener('bos:configuration-clear', clear);
    return () => {
      window.removeEventListener('bos:configuration-error', receive);
      window.removeEventListener('bos:configuration-clear', clear);
    };
  }, [pathname]);
  if (!failure) return null;
  const item = issueByCode(failure.code);
  return (
    <aside
      role="alert"
      style={{
        position: 'fixed',
        bottom: 20,
        right: 20,
        width: 'min(520px, calc(100vw - 40px))',
        maxHeight: '70vh',
        overflow: 'auto',
        zIndex: 2100,
        boxShadow: '0 6px 28px #0002',
      }}
    >
      <Alert
        type="error"
        showIcon
        closable
        onClose={() => setFailure(undefined)}
        message={`${issueText(failure.operation, language)}${zh ? '：' : ': '}${issueText(item.title, language)}`}
        description={
          <>
            <p>{issueText(item.reason, language)}</p>
            <p>{issueText(item.checks, language)[0]}</p>
            <Typography.Text code>{failure.code}</Typography.Text>
            <Space wrap style={{ marginTop: 12 }}>
              <a href={issueHref(failure.code)} target="_blank" rel="noopener noreferrer">
                {zh
                  ? '查看排错文档（新窗口，保留当前操作）'
                  : 'Troubleshooting (opens separately; keep your form)'}
              </a>
              <Button size="small" onClick={() => setPreview(!preview)}>
                {zh ? '预览脱敏诊断' : 'Preview sanitized diagnostics'}
              </Button>
            </Space>
            {preview && (
              <>
                <pre style={{ whiteSpace: 'pre-wrap' }}>{failure.preview}</pre>
                <Button
                  size="small"
                  onClick={async () => setCopied(await copyText(failure.preview))}
                >
                  {copied ? (zh ? '已复制' : 'Copied') : zh ? '复制以上信息' : 'Copy preview'}
                </Button>
                <p>
                  {zh
                    ? '仍未解决时，将以上信息与复现步骤提供给本实例管理员。'
                    : 'If unresolved, send this preview and reproduction steps to your instance administrator.'}
                </p>
              </>
            )}
          </>
        }
      />
    </aside>
  );
}
