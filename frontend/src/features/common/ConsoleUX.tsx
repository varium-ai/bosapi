import { clearConfigurationError } from '../diagnostics/reportConfigurationError';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, Collapse, type FormInstance, Modal, Space } from 'antd';
import { useI18n } from '../../i18n';
import { useLocation } from 'react-router-dom';
import { ExclamationCircleOutlined } from '@ant-design/icons';
import './discard-dialog.css';
import './console-ux.css';

const pageGuides: Record<string, string> = {
  providers: 'providers-models',
  organizations: 'organizations',
  keys: 'api-keys',
  'rate-limits': 'routing-limits',
  'security-events': 'security-admin',
  usage: 'usage-logs',
  requests: 'usage-logs',
  errors: 'usage-logs',
  'request-logs': 'usage-logs',
};

export function StatusTag({ value }: { value?: string }) {
  const { t } = useI18n();
  return (
    <span className="console-status" data-state={value}>
      {value ? t(`ux.${value}`, value) : t('ux.unavailable')}
    </span>
  );
}
export function PageHelp({ note }: { note?: string }) {
  const { t } = useI18n();
  const { pathname } = useLocation();
  const guide = pageGuides[pathname.split('/')[2]] || 'quickstart';
  return (
    <div className="ux-help">
      <span>{note && t(note)}</span>
      <a href={`/docs/${guide}`} target="_blank" rel="noopener noreferrer">
        {t('ux.help')} ↗
      </a>
    </div>
  );
}
export function AdvancedFields({
  children,
  defaultOpen,
}: {
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(!!defaultOpen);
  const container = useRef<HTMLDivElement>(null);
  const isOpen = useRef(open);
  isOpen.current = open;
  useEffect(() => {
    const element = container.current;
    if (!element) return;
    let frame = 0;
    const observer = new MutationObserver(records => {
      if (
        !records.some(
          record =>
            record.target instanceof HTMLElement &&
            record.target.classList.contains('ant-form-item-has-error'),
        )
      )
        return;
      const error = element.querySelector<HTMLElement>('.ant-form-item-has-error');
      if (!error || isOpen.current) return;
      setOpen(true);
      frame = requestAnimationFrame(() => {
        error.scrollIntoView({ block: 'center' });
        error
          .querySelector<HTMLElement>('input, textarea, [role=combobox]')
          ?.focus({ preventScroll: true });
      });
    });
    observer.observe(element, { subtree: true, attributes: true, attributeFilter: ['class'] });
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, []);
  return (
    <div ref={container}>
      <Collapse
        className="ux-advanced"
        activeKey={open ? ['advanced'] : []}
        onChange={keys => setOpen(keys.includes('advanced'))}
        items={[{ key: 'advanced', label: t('ux.advanced'), forceRender: true, children }]}
      />
    </div>
  );
}
export function LoadError({ retry }: { retry?: () => void }) {
  const { t } = useI18n();
  return (
    <Alert
      className="console-load-error"
      showIcon
      type="error"
      message={t('ux.load_error')}
      action={retry && <Button onClick={retry}>{t('common.retry')}</Button>}
    />
  );
}

export function SelectLoadError({ retry }: { retry: () => void }) {
  return (
    <div className="console-select-error" onMouseDown={event => event.preventDefault()}>
      <LoadError retry={retry} />
    </div>
  );
}
export function useDiscardChanges(form: FormInstance, close: () => void) {
  const { t } = useI18n();
  const finish = () => {
    clearConfigurationError();
    close();
  };
  return () => {
    if (!form.isFieldsTouched()) return finish();
    Modal.confirm({
      className: 'console-discard-dialog',
      centered: true,
      width: 432,
      icon: <ExclamationCircleOutlined />,
      title: t('ux.discard'),
      content: t('ux.discard_note'),
      okText: t('ux.discard_action'),
      cancelText: t('ux.keep_editing'),
      okButtonProps: { danger: true },
      okType: 'default',
      cancelButtonProps: { type: 'primary' },
      autoFocusButton: 'cancel',
      onOk: finish,
    });
  };
}
export function SectionLinks({ children }: { children: ReactNode }) {
  return (
    <Space wrap className="console-section-links">
      {children}
    </Space>
  );
}
